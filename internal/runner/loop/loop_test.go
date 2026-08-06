package loop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/sandbox"
	"github.com/glemsom/eitri/internal/tokenizer"
	"github.com/glemsom/eitri/internal/tool"
	"github.com/voocel/litellm"
)

// lrFromMessages converts []litellm.Message to a *litellm.Request with those
// messages.
func lrFromMessages(msgs []litellm.Message, opts ...func(*litellm.Request)) *litellm.Request {
	litMsgs := make([]litellm.Message, len(msgs))
	copy(litMsgs, msgs)
	r := &litellm.Request{Messages: litMsgs}
	for _, o := range opts {
		o(r)
	}
	return r
}

// lrWithModel sets the Model field on a litellm.Request.
func lrWithModel(model string) func(*litellm.Request) {
	return func(r *litellm.Request) { r.Model = model }
}

// msgContent extracts text content from a litellm.Message by reading its
// TextBlock blocks. Empty if no text blocks are found.
func msgContent(msg litellm.Message) string {
	return message.FromLitellmMessage(msg).Content
}

// msgRole returns the role string of a litellm.Message.
func msgRole(msg litellm.Message) string {
	return string(msg.Role)
}

// msgToolCalls returns the tool calls from a litellm.Message.
func msgToolCalls(msg litellm.Message) []message.ToolCall {
	return message.FromLitellmMessage(msg).ToolCalls
}

// msgToolCallID returns the tool call ID from a litellm.Message.
func msgToolCallID(msg litellm.Message) string {
	return message.FromLitellmMessage(msg).ToolCallID
}

// ── Mock LLM provider ──────────────────────────────────────────────────────

// mockProvider simulates an LLM with configurable responses per turn.
// It implements litellm.Provider and produces litellm.Stream instances.
type mockProvider struct {
	mu        sync.Mutex
	name      string
	turns     []mockTurn
	current   int
	onRequest func(turn int, req *litellm.Request) error
}

type mockTurn struct {
	tokens       []tokenEvent
	toolCalls    []litellm.ToolUseBlock
	finishReason litellm.FinishReason
	usage        *litellm.Usage
	err          error
}

type tokenEvent struct {
	content     string
	isReasoning bool
}

// mockStream implements litellm.Stream with pre-built events.
type mockStream struct {
	events []litellm.Event
	index  int
}

func (s *mockStream) Next() (litellm.Event, error) {
	if s.index >= len(s.events) {
		return nil, io.EOF
	}
	e := s.events[s.index]
	s.index++
	return e, nil
}

func (s *mockStream) Close() error { return nil }

// repeatedArgsProvider emits repeated full JSON tool-argument chunks on first
// turn, then a final answer on second turn.
type repeatedArgsProvider struct {
	mu   sync.Mutex
	turn int
}

func (p *repeatedArgsProvider) Name() string { return "repeated-args" }

func (p *repeatedArgsProvider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented for repeatedArgsProvider")
}

func (p *repeatedArgsProvider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	turn := p.turn
	p.turn++
	if turn == 0 {
		return &mockStream{events: []litellm.Event{
			litellm.ToolUseStart{ID: "call_1", Name: "bash"},
			litellm.ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"command":"echo hi"}`)},
			litellm.ToolUseDelta{ID: "call_1", ArgumentsDelta: []byte(`{"command":"echo hi"}`)},
			litellm.ToolUseDone{ID: "call_1"},
			litellm.DoneEvent{FinishReason: litellm.FinishReasonStop},
		}}, nil
	}

	return &mockStream{events: []litellm.Event{
		litellm.ContentDelta{Text: "done"},
		litellm.DoneEvent{FinishReason: litellm.FinishReasonStop},
	}}, nil
}

func (m *mockProvider) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mock"
}

func (m *mockProvider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented for mock, use Stream")
}

func (m *mockProvider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	turnIndex := m.current
	if m.onRequest != nil {
		if err := m.onRequest(turnIndex, req); err != nil {
			return nil, err
		}
	}

	if m.current >= len(m.turns) {
		return &mockStream{events: []litellm.Event{litellm.DoneEvent{}}}, nil
	}

	turn := m.turns[m.current]
	m.current++

	if turn.err != nil {
		return &mockStream{events: []litellm.Event{litellm.ErrorEvent{Err: turn.err}}}, nil
	}

	var events []litellm.Event

	// Send text content as ContentDelta events
	for _, tok := range turn.tokens {
		if tok.isReasoning {
			events = append(events, litellm.ReasoningDelta{Text: tok.content})
		} else {
			events = append(events, litellm.ContentDelta{Text: tok.content})
		}
	}

	// Send tool calls as ToolUseStart + ToolUseDelta + ToolUseDone sequences
	for _, tc := range turn.toolCalls {
		events = append(events, litellm.ToolUseStart{
			ID:   tc.ID,
			Name: tc.Name,
		})
		if len(tc.Arguments) > 0 {
			events = append(events, litellm.ToolUseDelta{
				ID:             tc.ID,
				ArgumentsDelta: tc.Arguments,
			})
		}
		events = append(events, litellm.ToolUseDone{
			ID: tc.ID,
		})
	}

	// Send done
	finishReason := turn.finishReason
	if finishReason == "" {
		finishReason = litellm.FinishReasonStop
	}
	if turn.usage != nil {
		events = append(events, litellm.UsageEvent{Usage: *turn.usage})
	}
	events = append(events, litellm.DoneEvent{FinishReason: finishReason})

	return &mockStream{events: events}, nil
}

// newMockClient creates a *litellm.Client backed by a mockProvider.
func newMockClient(turns []mockTurn) *litellm.Client {
	return newMockClientWithRequestHook(turns, nil)
}

func newMockClientWithRequestHook(turns []mockTurn, onRequest func(turn int, req *litellm.Request) error) *litellm.Client {
	mock := &mockProvider{turns: turns, onRequest: onRequest}
	client, err := litellm.New(mock)
	if err != nil {
		panic(fmt.Sprintf("failed to create mock client: %v", err))
	}
	return client
}

// buildMockToolCall is a helper to create a litellm.ToolUseBlock for tests.
func buildMockToolCall(id, name, argsJSON string) litellm.ToolUseBlock {
	return litellm.ToolUseBlock{
		ID:        id,
		Name:      name,
		Arguments: json.RawMessage(argsJSON),
	}
}

// emsg creates an EitriMessage with the given role and text content.
func emsg(role, content string) message.EitriMessage {
	return message.EitriMessage{
		Message: litellm.Message{
			Role:   litellm.Role(role),
			Blocks: []litellm.Block{litellm.TextBlock{Text: content}},
		},
	}
}

// ── Simple mock tool ────────────────────────────────────────────────────────

type simpleMockTool struct {
	name        string
	description string
	callFunc    func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error)
}

func (m *simpleMockTool) Name() string        { return m.name }
func (m *simpleMockTool) Description() string { return m.description }
func (m *simpleMockTool) JSONSchema() litellm.Schema {
	return litellm.Schema(`{"type":"object","properties":{}}`)
}
func (m *simpleMockTool) Call(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	if m.callFunc != nil {
		return m.callFunc(ctx, args)
	}
	return tool.Success([]litellm.Block{litellm.TextBlock{Text: "ok"}}), nil
}

// ── Test helpers ────────────────────────────────────────────────────────────

// collectSSE collects all SSE events from a state until a done event.
func collectSSE(state *runstate.State) []runstate.SSEEvent {
	_, ch, ok := state.Subscribe()
	if !ok {
		return nil
	}
	var events []runstate.SSEEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

// sseEventTypes returns the types of events for assertion.
func sseEventTypes(events []runstate.SSEEvent) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func TestRunAgent_SingleTurn_NoToolCalls(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello! How can I help?"}}},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 128000,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)

	// Should have token events followed by done
	hasTokens := false
	hasDone := false
	for _, t := range types {
		if t == "token" {
			hasTokens = true
		}
		if t == "done" {
			hasDone = true
		}
	}
	if !hasTokens {
		t.Error("expected token events, got none")
	}
	if !hasDone {
		t.Errorf("expected done event, got %v", types)
	}
}

func TestRunAgent_OutputTokenCapExceeded_NoToolCallsFails(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Reasoning model burns its max_output_tokens budget on thinking and ends
	// with finish_reason="length", no content and no tool calls. This is the
	// "went nowhere" failure: the loop must NOT swallow it as a clean
	// completion.
	client := newMockClient([]mockTurn{
		{finishReason: litellm.FinishReasonLength},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "implement the next issue"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
	})
	if err == nil {
		t.Fatal("expected output-token-cap error, got nil (turn silently swallowed as completion)")
	}

	var lenErr *MaxOutputTokensExceededError
	if !errors.As(err, &lenErr) {
		t.Fatalf("error type = %T, want *MaxOutputTokensExceededError", err)
	}

	// The UI must signal the truncation, not a done event.
	events := collectSSE(sseState)
	types := sseEventTypes(events)
	for _, evType := range types {
		if evType == "done" {
			t.Errorf("unexpected done event for truncated turn: %v", types)
		}
	}
}

func TestRunAgent_SanitizesProviderToolUseIDsBeforeReplay(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	invalidProviderID := "IriJ27r12vncb2n073VHiHu6JKAfIlJO6Pcpi6FBXeXH2E/Gp5gt8hpfKcJ4w9JAISj1Lr9n9EKuvqjoB8wNR76fj/Jajk2fMED79a/yaPfPALHrQf0srtlb8awnKCwAznP9/XkTunRNk6SzBSlyAmHaOWUVXcfEO/c0Nh/UhhGnDy"

	client := newMockClientWithRequestHook([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall(invalidProviderID, "test_tool", `{"input":"test"}`),
			},
		},
		{tokens: []tokenEvent{{content: "ok"}}},
	}, func(turn int, req *litellm.Request) error {
		if turn != 1 {
			return nil
		}
		for i, msg := range req.Messages {
			for _, block := range msg.Blocks {
				switch b := block.(type) {
				case litellm.ToolUseBlock:
					if b.ID == invalidProviderID {
						return fmt.Errorf("messages[%d]: tool use id %q is invalid", i, b.ID)
					}
				case litellm.ToolResultBlock:
					if b.ToolUseID == invalidProviderID {
						return fmt.Errorf("messages[%d]: tool use id %q is invalid", i, b.ToolUseID)
					}
				}
			}
		}
		return nil
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{name: "test_tool", description: "A test tool"})

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "use tool"}}}},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		ContextWindow: 128000,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	if len(req.Messages) < 3 {
		t.Fatalf("messages = %d, want at least 3", len(req.Messages))
	}
	assistantID := msgToolCalls(req.Messages[1])[0].ID
	toolID := msgToolCallID(req.Messages[2])
	if assistantID == invalidProviderID || toolID == invalidProviderID {
		t.Fatalf("invalid provider tool ID replayed: assistant=%q tool=%q", assistantID, toolID)
	}
	if assistantID == "" || assistantID != toolID {
		t.Fatalf("sanitized IDs must be non-empty and matched: assistant=%q tool=%q", assistantID, toolID)
	}
}

func TestRunAgent_NormalizesRepeatedToolArgumentChunks(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	client, err := litellm.New(&repeatedArgsProvider{})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run bash"}}},
		},
		lrWithModel("gpt-5.4"),
	)
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	toolReg := tool.NewRegistry()
	toolReg.Register(tool.NewBashTool(workspace, time.Second, sandbox.Config{Profile: sandbox.ProfileNone}))

	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	if len(req.Messages) != 4 {
		t.Fatalf("req.Messages length = %d, want 4 (user + assistant + tool + final assistant)", len(req.Messages))
	}
	if got := msgContent(req.Messages[2]); got != "<stdout>\nhi\n</stdout>" {
		t.Fatalf("tool result content = %q, want %q", got, "<stdout>\nhi\n</stdout>")
	}
	if strings.Contains(msgContent(req.Messages[2]), "Tool error") {
		t.Fatalf("tool result unexpectedly contains error: %q", msgContent(req.Messages[2]))
	}
}

func TestRunAgent_MultiTurn_ToolCallThenResponse(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Turn 1: LLM returns tool call
	// Turn 2: LLM returns final response
	client := newMockClient([]mockTurn{
		{
			tokens: []tokenEvent{{content: "Let me check that..."}},
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "test_tool", `{"input":"test"}`),
			},
		},
		{
			tokens: []tokenEvent{{content: "The result is 42."}},
		},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name:        "test_tool",
		description: "A test tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("42")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "what is the answer?"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)

	// Check event types include token, tool_call, tool_result, done
	types := sseEventTypes(events)

	found := make(map[string]bool)
	for _, typ := range types {
		found[typ] = true
	}

	if !found["tool_call"] {
		t.Errorf("expected tool_call event, got %v", types)
	}
	if !found["tool_result"] {
		t.Errorf("expected tool_result event, got %v", types)
	}
	if !found["done"] {
		t.Errorf("expected done event, got %v", types)
	}

	// Verify tool result was included in conversation history
	// The loop should have added assistant msg + tool msg to req.Messages
	if len(req.Messages) != 4 {
		t.Fatalf("req.Messages length = %d, want 4 (user + assistant + tool + final assistant)", len(req.Messages))
	}

	// Check message order: user, assistant, tool, assistant
	if msgRole(req.Messages[1]) != "assistant" {
		t.Errorf("message[1] role = %q, want %q", msgRole(req.Messages[1]), "assistant")
	}
	if msgRole(req.Messages[2]) != "tool" {
		t.Errorf("message[2] role = %q, want %q", msgRole(req.Messages[2]), "tool")
	}
	if msgContent(req.Messages[2]) != "42" {
		t.Errorf("message[2] content = %q, want %q", msgContent(req.Messages[2]), "42")
	}
	if msgRole(req.Messages[3]) != "assistant" {
		t.Errorf("message[3] role = %q, want %q", msgRole(req.Messages[3]), "assistant")
	}
	if msgContent(req.Messages[3]) != "The result is 42." {
		t.Errorf("message[3] content = %q, want %q", msgContent(req.Messages[3]), "The result is 42.")
	}
}

func TestRunAgent_MultipleToolCallsPerTurn(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	var execOrder []string
	execMu := sync.Mutex{}

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "tool_a", `{}`),
				buildMockToolCall("call_2", "tool_b", `{}`),
			},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "tool_a",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			execMu.Lock()
			execOrder = append(execOrder, "a")
			execMu.Unlock()
			return tool.Success(tool.TextBlocks("a_result")), nil
		},
	})
	toolReg.Register(&simpleMockTool{
		name: "tool_b",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			execMu.Lock()
			execOrder = append(execOrder, "b")
			execMu.Unlock()
			return tool.Success(tool.TextBlocks("b_result")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run both tools"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	if len(execOrder) != 2 {
		t.Fatalf("execOrder length = %d, want 2", len(execOrder))
	}

	// Check sequential execution (a before b since tool_calls are ordered)
	if execOrder[0] != "a" || execOrder[1] != "b" {
		t.Errorf("execOrder = %v, want [a b]", execOrder)
	}
}

func TestRunAgent_ToolExecutionError_IsError(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "failing_tool", `{}`),
			},
		},
		{tokens: []tokenEvent{{content: "I see the error, let me handle it."}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "failing_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.ToolError(tool.TextBlocks("command not found")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run failing tool"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify the tool result message is in history
	// Expected: user, assistant(with tool call), tool(result), assistant(final)
	if len(req.Messages) != 4 {
		t.Fatalf("req.Messages length = %d, want 4 (user + assistant + tool + final assistant)", len(req.Messages))
	}

	if msgRole(req.Messages[2]) != "tool" {
		t.Errorf("message[2] role = %q, want %q", msgRole(req.Messages[2]), "tool")
	}
	if msgContent(req.Messages[2]) != "command not found" {
		t.Errorf("message[2] content = %q, want %q", msgContent(req.Messages[2]), "command not found")
	}
	// Final assistant message should reference the error
	if msgContent(req.Messages[3]) != "I see the error, let me handle it." {
		t.Errorf("message[3] content = %q, want %q", msgContent(req.Messages[3]), "I see the error, let me handle it.")
	}
}

func TestRunAgent_MaxTurnsExceeded(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// LLM keeps making tool calls — will exceed maxTurns
	client := newMockClient([]mockTurn{
		{toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "loop_tool", `{}`)}},
		{toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_2", "loop_tool", `{}`)}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "loop_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("ok")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "loop"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   1,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err == nil {
		t.Fatal("expected MaxTurnsExceededError, got nil")
	}

	var maxTurnsErr *MaxTurnsExceededError
	if !errors.As(err, &maxTurnsErr) {
		t.Fatalf("error type = %T, want *MaxTurnsExceededError", err)
	}
	if maxTurnsErr.Limit != 1 {
		t.Errorf("Limit = %d, want 1", maxTurnsErr.Limit)
	}
}

func TestRunAgent_ContextCancellation(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "thinking..."}}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(ctx, RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ── Blocking mock for cancellation tests ──────────────────────────────────
type blockingMockProvider2 struct {
	content string
	started chan struct{} // closed after first token is sent
}

func (m *blockingMockProvider2) Name() string { return "blocking-mock-2" }
func (m *blockingMockProvider2) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented, use Stream")
}
func (m *blockingMockProvider2) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	return &blockingMockStream2{content: m.content, started: m.started, closed: make(chan struct{})}, nil
}

type blockingMockStream2 struct {
	content string
	started chan struct{}
	sent    bool
	closed  chan struct{}
}

func (s *blockingMockStream2) Next() (litellm.Event, error) {
	if !s.sent {
		s.sent = true
		close(s.started)
		return litellm.ContentDelta{Text: s.content}, nil
	}
	// Block until stream is closed (context cancellation)
	<-s.closed
	return nil, io.EOF
}

func (s *blockingMockStream2) Close() error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	return nil
}

func TestRunAgent_PreservesPartialResultOnStreamCancellation(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	started := make(chan struct{})
	blockingProv := &blockingMockProvider2{
		content: "Partial response text...",
		started: started,
	}
	blockingClient, err := litellm.New(blockingProv)
	if err != nil {
		t.Fatalf("failed to create blocking mock client: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	// Start RunAgent in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunAgent(ctx, RunSpec{
			Client:     blockingClient,
			Request:    req,
			MaxTurns:   5,
			MaxHistory: 0,
			SSEWriter:  w,
			Tools:      nil,
		}, RunOpts{
			HistoryMgr:    NewRequestHistoryManager(req),
			Confirmer:     nil,
			UISessionMgr:  nil,
			SessionID:     "",
			ContextWindow: 0,
			CrashDumpFunc: nil,
			Turns:         nil,
		})
	}()

	// Wait for streaming to start (first token sent)
	<-started

	// Cancel context mid-stream
	cancel()

	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Verify partial result was appended to conversation history
	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2 (user + partial assistant)", len(req.Messages))
	}
	if msgRole(req.Messages[1]) != "assistant" {
		t.Errorf("message[1] role = %q, want %q", msgRole(req.Messages[1]), "assistant")
	}
	if !strings.Contains(msgContent(req.Messages[1]), "Partial response") {
		t.Errorf("message[1] content = %q, want to contain 'Partial response'", msgContent(req.Messages[1]))
	}
}

func TestRunAgent_StreamError(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{err: fmt.Errorf("rate limit exceeded")},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error = %q, want rate limit", err.Error())
	}
}

func TestRunAgent_NoTools(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "I am a helpful assistant."}}},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	// Should have token + done
	types := sseEventTypes(events)
	if len(types) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(types), types)
	}

	lastType := types[len(types)-1]
	if lastType != "done" {
		t.Errorf("last event type = %q, want %q", lastType, "done")
	}
}

// ————— Retry on transient ChatStream errors —————

// transientErrorLLM returns a transient error on the first Stream call,
// then delegates to a normal mock. Used to test retry logic.
type transientErrorLLM struct {
	mu           sync.Mutex
	calls        int
	transientErr error
	inner        *litellm.Client
}

func (m *transientErrorLLM) Name() string { return "transient-error-mock" }

func (m *transientErrorLLM) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return m.inner.Chat(ctx, *req)
}

func (m *transientErrorLLM) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	m.mu.Lock()
	n := m.calls
	m.calls++
	m.mu.Unlock()
	if n == 0 {
		return nil, m.transientErr
	}
	return m.inner.Stream(ctx, *req)
}

func TestRunAgent_RetryTransientChatStreamError(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	inner := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello after retry!"}}},
	})
	transientProv := &transientErrorLLM{
		transientErr: fmt.Errorf("Provider returned HTTP 500: Internal Server Error"),
		inner:        inner,
	}
	transientClient, err := litellm.New(transientProv)
	if err != nil {
		t.Fatalf("failed to create transient error mock client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	err = RunAgent(context.Background(), RunSpec{
		Client:     transientClient,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error after retry: %v", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)
	if len(types) < 2 || types[len(types)-1] != "done" {
		t.Fatalf("expected run to succeed after retry, events: %v", types)
	}
}

func TestRunAgent_DoesNotRetryHTTP400(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// inner mock will be called if retry happens (which would be the bug)
	transientProv := &transientErrorLLM{
		// Genuine bad request — model not found, not an upstream failure
		transientErr: litellm.NewHTTPError("mock", 400, "model \"unknown-model\" not found for provider"),
		inner: newMockClient([]mockTurn{
			{tokens: []tokenEvent{{content: "should not be reached"}}},
		}),
	}
	transientClient, err := litellm.New(transientProv)
	if err != nil {
		t.Fatalf("failed to create transient error mock client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	err = RunAgent(context.Background(), RunSpec{
		Client:     transientClient,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err == nil {
		t.Fatal("expected error for HTTP 400, got nil")
	}
	if !strings.Contains(err.Error(), "model") || !strings.Contains(err.Error(), "unknown-model") {
		t.Fatalf("error should mention model not found, got: %v", err)
	}

	// Verify no retry: inner mock never produced events.
	// transientErrorLLM returns error on first call, delegates to inner on subsequent.
	// If inner called, we'd see token/tool/done events.
	// The only SSE event should be the error event from the retry-exhausted path.
	types := sseEventTypes(collectSSE(sseState))
	if len(types) > 1 {
		t.Fatalf("expected only error event (no retry), got: %v", types)
	}
	if len(types) == 1 && types[0] != "error" {
		t.Fatalf("expected error event type, got: %s", types[0])
	}
}

func TestRunAgent_RetriesHTTP400WithUpstreamFailure(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	inner := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello after retry!"}}},
	})
	client, err := litellm.New(&transientErrorLLM{
		// Upstream request failure proxied as 400 — should be retried
		transientErr: fmt.Errorf("Provider returned HTTP 400: Error from provider (Console Go): Upstream request failed"),
		inner:        inner,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error after retry of upstream failure 400: %v", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)
	if len(types) < 2 || types[len(types)-1] != "done" {
		t.Fatalf("expected run to succeed after retry, events: %v", types)
	}
}

func TestRunAgent_RetryPolicyZeroAttemptsDoesNotRetry(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// inner mock would be called if any retry happened (which would be the bug)
	transientProv := &transientErrorLLM{
		transientErr: fmt.Errorf("Provider returned HTTP 500: Internal Server Error"),
		inner: newMockClient([]mockTurn{
			{tokens: []tokenEvent{{content: "should not be reached"}}},
		}),
	}
	client, err := litellm.New(transientProv)
	if err != nil {
		t.Fatalf("failed to create transient error mock client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	start := time.Now()
	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
		RetryPolicy:   &RetryPolicy{Attempts: 0},
	})
	if err == nil {
		t.Fatal("expected error for transient failure with zero retries, got nil")
	}
	if transientProv.calls != 1 {
		t.Errorf("Stream calls = %d, want 1 (no retry)", transientProv.calls)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("run took %v, want fast with zero-attempt policy (no 1s sleeps)", elapsed)
	}
	types := sseEventTypes(collectSSE(sseState))
	if len(types) != 1 || types[0] != "error" {
		t.Fatalf("expected single error event, got: %v", types)
	}
}

func TestRunAgent_RetryPolicyZeroBackoffRetriesFast(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	inner := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello after retry!"}}},
	})
	transientProv := &transientErrorLLM{
		transientErr: fmt.Errorf("Provider returned HTTP 500: Internal Server Error"),
		inner:        inner,
	}
	client, err := litellm.New(transientProv)
	if err != nil {
		t.Fatalf("failed to create transient error mock client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}},
		},
		lrWithModel("test-model"),
	)

	start := time.Now()
	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
		RetryPolicy:   &RetryPolicy{Attempts: 1, Backoff: 0},
	})
	if err != nil {
		t.Fatalf("RunAgent error after retry: %v", err)
	}
	if transientProv.calls != 2 {
		t.Errorf("Stream calls = %d, want 2 (one retry)", transientProv.calls)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("run took %v, want fast with zero backoff (no 1s sleeps)", elapsed)
	}

	types := sseEventTypes(collectSSE(sseState))
	if len(types) < 2 || types[len(types)-1] != "done" {
		t.Fatalf("expected run to succeed after retry, events: %v", types)
	}
}

func TestRunAgent_EmptyToolCallList(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Tool calls with zero length — treated as no tool calls
	client := newMockClient([]mockTurn{
		{
			tokens:    []tokenEvent{{content: "answer"}},
			toolCalls: []litellm.ToolUseBlock{}, // empty, not nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}
}

func TestRunAgent_ZeroMaxTurnsDefaultsToTen(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// LLM keeps returning tool calls. With maxTurns=0, defaults to 10.
	// We only provide 3 turns → should succeed (no max turns hit).
	mockTurns := []mockTurn{
		{toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "loop_tool", `{}`)}},
		{toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_2", "loop_tool", `{}`)}},
		{tokens: []tokenEvent{{content: "done"}}},
	}

	client := newMockClient(mockTurns)

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "loop_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("ok")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   0,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}
}

func TestRunAgent_ToolReturnsNoContent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "empty_tool", `{}`),
			},
		},
		{tokens: []tokenEvent{{content: "Tool returned nothing"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "empty_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(nil), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run empty tool"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Tool result (message[2]) should have empty content
	if len(req.Messages) >= 3 {
		toolMsg := req.Messages[2]
		if msgRole(toolMsg) != "tool" {
			t.Errorf("message[2] role = %q, want %q", msgRole(toolMsg), "tool")
		}
		if msgContent(toolMsg) != "" {
			t.Errorf("tool result content = %q, want empty", msgContent(toolMsg))
		}
	}
}

func TestRunAgent_UnknownTool_ContinuesLoop(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// LLM calls a hallucinated tool "replace" (doesn't exist in registry).
	// The loop should NOT terminate — it should feed the error back to the
	// LLM as a tool result, letting the LLM self-correct on the next turn.
	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "replace", `{"filePath":"LICENSE","oldString":"foo","newString":"bar"}`),
			},
		},
		{tokens: []tokenEvent{{content: "corrected: using edit tool instead"}}},
	})

	toolReg := tool.NewRegistry()
	// Only register "edit", not "replace"
	toolReg.Register(&simpleMockTool{
		name: "edit",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("ok")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "edit the file"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent should not return error for unknown tool, got: %v", err)
	}

	// Verify the LLM got a tool result with the error message
	if len(req.Messages) < 3 {
		t.Fatalf("expected at least 3 messages (user + assistant + tool result), got %d", len(req.Messages))
	}
	toolMsg := req.Messages[2]
	if msgRole(toolMsg) != "tool" {
		t.Errorf("message[2] role = %q, want %q", msgRole(toolMsg), "tool")
	}
	if !strings.Contains(msgContent(toolMsg), "Tool error") && !strings.Contains(msgContent(toolMsg), "unknown tool") {
		t.Errorf("tool result should contain error about unknown tool, got: %q", msgContent(toolMsg))
	}

	// Final message should be the LLM's self-correction response
	if len(req.Messages) >= 4 {
		finalMsg := req.Messages[len(req.Messages)-1]
		if msgRole(finalMsg) != "assistant" {
			t.Errorf("final message role = %q, want %q", msgRole(finalMsg), "assistant")
		}
	}
}

func TestRunAgent_Thinking(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		tokens          []tokenEvent
		wantThinkingCnt int
		wantTokenCnt    int
		wantContains    []string // substrings expected in accumulated assistant content
		wantNotContains []string // substrings NOT expected in accumulated assistant content
		query           string
	}{
		{
			name: "reasoning then text",
			tokens: []tokenEvent{
				{content: "Let me think about this step by step...", isReasoning: true},
				{content: "Here is the answer."},
			},
			wantThinkingCnt: 1,
			wantTokenCnt:    1,
			wantContains:    []string{"Here is the answer."},
			wantNotContains: []string{"Let me think about this"},
			query:           "what is the answer?",
		},
		{
			name: "interleaved reasoning and text",
			tokens: []tokenEvent{
				{content: "First reasoning...", isReasoning: true},
				{content: "Intermediate text. "},
				{content: "More reasoning...", isReasoning: true},
				{content: "Final answer."},
			},
			wantThinkingCnt: 2,
			wantTokenCnt:    2,
			wantContains:    []string{"Intermediate text. ", "Final answer."},
			wantNotContains: []string{"First reasoning", "More reasoning"},
			query:           "complex question",
		},
		{
			name: "pure reasoning only",
			tokens: []tokenEvent{
				{content: "Thinking step one...", isReasoning: true},
				{content: "Thinking step two...", isReasoning: true},
			},
			// Server-side batching coalesces consecutive thinking deltas into
			// a single thinking_delta event.
			wantThinkingCnt: 1,
			wantTokenCnt:    0,
			wantContains:    nil,
			wantNotContains: []string{"Thinking step one", "Thinking step two"},
			query:           "what is the answer?",
		},
		{
			name: "multiple reasoning blocks",
			tokens: []tokenEvent{
				{content: "Reason 1", isReasoning: true},
				{content: "Reason 2", isReasoning: true},
				{content: "Reason 3", isReasoning: true},
				{content: "Final text."},
			},
			// Server-side batching coalesces consecutive thinking deltas into
			// a single thinking_delta event.
			wantThinkingCnt: 1,
			wantTokenCnt:    1,
			wantContains:    []string{"Final text."},
			wantNotContains: []string{"Reason 1", "Reason 2", "Reason 3"},
			query:           "what is the answer?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sseState := runstate.New()
			w := runstate.NewWriter(sseState)

			client := newMockClient([]mockTurn{
				{tokens: tt.tokens},
			})

			req := lrFromMessages(
				[]litellm.Message{
					{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: tt.query}}},
				},
				lrWithModel("test-model"),
			)

			err := RunAgent(context.Background(), RunSpec{
				Client:     client,
				Request:    req,
				MaxTurns:   5,
				MaxHistory: 0,
				SSEWriter:  w,
				Tools:      nil,
			}, RunOpts{
				HistoryMgr:    NewRequestHistoryManager(req),
				Confirmer:     nil,
				UISessionMgr:  nil,
				SessionID:     "",
				ContextWindow: 0,
				CrashDumpFunc: nil,
				Turns:         nil,
			})
			if err != nil {
				t.Fatalf("RunAgent error: %v", err)
			}

			events := collectSSE(sseState)
			types := sseEventTypes(events)

			thinkingDeltaCount := 0
			tokenCount := 0
			for _, evt := range events {
				switch evt.Type {
				case "thinking_delta":
					thinkingDeltaCount++
					if evt.Content == "" {
						t.Error("thinking_delta event has empty content")
					}
				case "token":
					tokenCount++
				case "done":
					// done is always last, OK
				}
			}

			// Batching can only reduce event counts (never increase beyond the
			// number of writes), so assert at-least semantics.
			if thinkingDeltaCount < tt.wantThinkingCnt {
				t.Errorf("thinking_delta count = %d, want at least %d. Types: %v", thinkingDeltaCount, tt.wantThinkingCnt, types)
			}
			if tokenCount < tt.wantTokenCnt {
				t.Errorf("token count = %d, want at least %d. Types: %v", tokenCount, tt.wantTokenCnt, types)
			}

			if len(req.Messages) >= 2 {
				lastAssistant := req.Messages[len(req.Messages)-1]
				if msgRole(lastAssistant) == "assistant" {
					for _, want := range tt.wantContains {
						if !strings.Contains(msgContent(lastAssistant), want) {
							t.Errorf("assistant content = %q, want to contain %q", msgContent(lastAssistant), want)
						}
					}
					for _, notWant := range tt.wantNotContains {
						if strings.Contains(msgContent(lastAssistant), notWant) {
							t.Errorf("reasoning content %q leaked into accumulated assistant content: %q", notWant, msgContent(lastAssistant))
						}
					}
				}
			}
		})
	}
}

// ── Sliding window cap tests ────────────────────────────────────────────────

func TestTrimMessages_RemovesOldestWhenOverCap(t *testing.T) {
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a helpful assistant."}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "msg1"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "resp1"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "msg2"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "resp2"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "msg3"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "resp3"}}},
		},
	}

	trimMessages(req, 4) // cap at 4 non-system messages

	// System prompt must remain
	if len(req.Messages) < 1 || msgRole(req.Messages[0]) != "system" {
		t.Fatalf("system prompt missing or moved, got %+v", req.Messages)
	}

	// Total messages: system (1) + 4 non-system = 5
	if len(req.Messages) != 5 {
		t.Fatalf("len(Messages) = %d, want 5 (system + 4 non-system)", len(req.Messages))
	}

	// Oldest 2 non-system messages removed (msg1/resp1), remaining: msg2/resp2/msg3/resp3
	expected := []string{"msg2", "resp2", "msg3", "resp3"}
	for i, exp := range expected {
		idx := 1 + i // skip system
		if msgContent(req.Messages[idx]) != exp {
			t.Errorf("Messages[%d].Content = %q, want %q", idx, msgContent(req.Messages[idx]), exp)
		}
	}
}

func TestTrimMessages_WithinCapUnchanged(t *testing.T) {
	msgs := []litellm.Message{
		{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}},
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "u1"}}},
		{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "a1"}}},
	}
	req := lrFromMessages(msgs)

	trimMessages(req, 5)

	if len(req.Messages) != 3 {
		t.Errorf("len = %d, want 3 (unchanged)", len(req.Messages))
	}
}

func TestTrimMessages_ZeroOrNegativeIsNoop(t *testing.T) {
	msgs := []litellm.Message{
		{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}},
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "u1"}}},
		{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "a1"}}},
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "u2"}}},
		{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "a2"}}},
	}

	// maxHistory = 0 (no limit)
	req0 := lrFromMessages(msgs)
	trimMessages(req0, 0)
	if len(req0.Messages) != 5 {
		t.Errorf("maxHistory=0: len = %d, want 5", len(req0.Messages))
	}

	// maxHistory = -1 (no limit)
	reqNeg := lrFromMessages(msgs)
	trimMessages(reqNeg, -1)
	if len(reqNeg.Messages) != 5 {
		t.Errorf("maxHistory=-1: len = %d, want 5", len(reqNeg.Messages))
	}
}

func TestTrimMessages_NoSystemPromptIsFine(t *testing.T) {
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "u1"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "a1"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "u2"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "a2"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "u3"}}},
		},
	}

	trimMessages(req, 2)

	// Should keep only the last 2 non-system messages
	if len(req.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(req.Messages))
	}
	if msgContent(req.Messages[0]) != "a2" {
		t.Errorf("Messages[0].Content = %q, want %q", msgContent(req.Messages[0]), "a2")
	}
	if msgContent(req.Messages[1]) != "u3" {
		t.Errorf("Messages[1].Content = %q, want %q", msgContent(req.Messages[1]), "u3")
	}
}

func TestRunAgent_SlidingWindowTrimDuringMultiTurn(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// 3 turns: tool call → tool result → final answer
	client := newMockClient([]mockTurn{
		{
			tokens: []tokenEvent{{content: "thinking..."}},
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "test_tool", `{}`),
			},
		},
		{tokens: []tokenEvent{{content: "final answer"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "test_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("tool result")), nil
		},
	})

	// Start with 5 existing messages + system prompt, cap at 3
	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "old1"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "old1r"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "old2"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "old2r"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "old3"}}},
			{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "old3r"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run tool"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 3,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify: system prompt preserved, old1/old1r trimmed, old2/old2r trimmed,
	// old3/old3r/run tool/assistant+tool+final kept but capped at 3 non-system
	// With cap=3: only the last 3 non-system messages survive
	// The run produces: user("run tool") → assistant(tool call) → tool(result) → assistant("final answer")
	// After all appends and trims, we expect: system + last 3 non-system
	sysFound := false
	nonSys := 0
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			sysFound = true
		} else {
			nonSys++
		}
	}

	if !sysFound {
		t.Error("system prompt was removed by trimming")
	}
	if nonSys > 3 {
		t.Errorf("non-system messages = %d, want at most 3", nonSys)
	}
	if !sysFound && nonSys > 3 {
		t.Logf("Messages: %+v", req.Messages)
	}
}

func TestRunAgent_MaxHistoryZeroNoTrimming(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "sys"}}},
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Should have system + user + assistant (3 total)
	if len(req.Messages) != 3 {
		t.Errorf("len(Messages) = %d, want 3 (no trimming)", len(req.Messages))
	}
}

func TestRunAgent_RenderMermaidDiagramEmitsComponent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "render_mermaid_diagram", `{"code":"graph TD; A-->B;"}`),
			},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "render_mermaid_diagram",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("Rendered MermaidDiagram")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "render a diagram"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	foundComponent := false
	for _, evt := range events {
		if evt.Type == "component" {
			foundComponent = true
			break
		}
	}
	if !foundComponent {
		t.Errorf("expected component event for render_mermaid_diagram, got types: %v", sseEventTypes(events))
	}
}

func TestRunAgent_RenderQuickRepliesDoesNotEmitComponent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{
				buildMockToolCall("call_1", "render_quick_replies", `{"options":["yes","no"]}`),
			},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "render_quick_replies",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("Rendered QuickReplies")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "show quick replies"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "component" {
			t.Errorf("unexpected component event for render_quick_replies (should be inline), got event: %+v", evt)
			break
		}
	}
}

func TestRunAgent_RenderToolErrorSkipsComponent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "render_mermaid_diagram", `{"code":"graph TD; A-->B;"}`)},
		},
		{tokens: []tokenEvent{{content: "error occurred"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "render_mermaid_diagram",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.ToolError(tool.TextBlocks("something went wrong")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "render a diagram"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "component" {
			t.Error("component event should NOT be emitted when render tool returns error")
			break
		}
	}
}

func TestRunAgent_UnknownToolSkipsComponent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "some_other_tool", `{}`)},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "some_other_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("ok")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run tool"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "component" {
			t.Error("component event should NOT be emitted for non-render tools")
			break
		}
	}
}

// ── Context update tests ──────────────────────────────────────────────────────

func TestContextUpdate_SingleTurnNoTools(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-1"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are a helpful assistant.")
	sessionMgr.AppendUser(sessionID, "hi")

	req := &litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     sessionID,
		ContextWindow: 128000,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)
	ctxUpdateCount := 0
	for _, typ := range types {
		if typ == "context_update" {
			ctxUpdateCount++
		}
	}
	if ctxUpdateCount != 1 {
		t.Errorf("context_update count = %d, want 1 (before done). Types: %v", ctxUpdateCount, types)
	}
	// Last event should be done
	if len(types) > 0 && types[len(types)-1] != "done" {
		t.Errorf("last event type = %q, want %q", types[len(types)-1], "done")
	}
	// Verify context_update appears before done
	ctxUpdateIdx := -1
	doneIdx := -1
	for i, typ := range types {
		switch typ {
		case "context_update":
			ctxUpdateIdx = i
		case "done":
			doneIdx = i
		}
	}
	if ctxUpdateIdx < 0 || doneIdx < 0 || ctxUpdateIdx > doneIdx {
		t.Errorf("context_update (idx=%d) should come before done (idx=%d)", ctxUpdateIdx, doneIdx)
	}
}

func TestContextUpdate_MultiTurnWithToolCalls(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			tokens:    []tokenEvent{{content: "let me check"}},
			toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "test_tool", `{}`)},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "test_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("result")), nil
		},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-2"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "run tool")

	req := &litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     sessionID,
		ContextWindow: 128000,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)
	ctxUpdateCount := 0
	for _, typ := range types {
		if typ == "context_update" {
			ctxUpdateCount++
		}
	}
	// Turn 1: tool call turn -> 1 context_update
	// Turn 2: final turn -> 1 context_update
	// Total: 2
	if ctxUpdateCount != 2 {
		t.Errorf("context_update count = %d, want 2 (1 per turn). Types: %v", ctxUpdateCount, types)
	}
}

func TestContextUpdate_ZeroContextWindowSkipsBroadcast(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-3"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "hi")

	req := &litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     sessionID,
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "context_update" {
			t.Error("unexpected context_update when contextWindow=0")
			break
		}
	}
}

func TestContextUpdate_MaxTurnsExceededIncludesFinalUpdate(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "loop_tool", `{}`)}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "loop_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("ok")), nil
		},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-4"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "run tool")

	req := &litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   1,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     sessionID,
		ContextWindow: 128000,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err == nil {
		t.Fatal("expected MaxTurnsExceededError, got nil")
	}
	var maxTurnsErr *MaxTurnsExceededError
	if !errors.As(err, &maxTurnsErr) {
		t.Fatalf("error type = %T, want *MaxTurnsExceededError", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)
	ctxUpdateCount := 0
	for _, typ := range types {
		if typ == "context_update" {
			ctxUpdateCount++
		}
	}
	// 1 turn that makes a tool call -> 1 context_update after history appended
	// Then max turns exceeded: 1 context_update before error broadcast
	// Total: 2
	if ctxUpdateCount != 2 {
		t.Errorf("context_update count = %d, want 2 (before tool turn + before error). Types: %v", ctxUpdateCount, types)
	}
}

func TestContextUpdate_NoSessionManagerSkipsBroadcast(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
		lrWithModel("test-model"),
	)

	// No sessionMgr passed
	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "context_update" {
			t.Error("unexpected context_update when sessionMgr is nil")
			break
		}
	}
}

func TestContextUpdate_DataHasExpectedFields(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "answer"}}},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-5"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are a helpful assistant.")
	sessionMgr.AppendUser(sessionID, "hello")

	req := &litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     sessionID,
		ContextWindow: 128000,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	var ctxUpdate *tokenizer.ContextUpdate
	for _, evt := range events {
		if evt.Type == "context_update" {
			if data, ok := evt.Data.(*tokenizer.ContextUpdate); ok {
				ctxUpdate = data
				break
			}
		}
	}
	if ctxUpdate == nil {
		t.Fatal("expected context_update event with ContextUpdate data")
	}
	if ctxUpdate.ContextWindow <= 0 {
		t.Errorf("ContextWindow = %d, want > 0", ctxUpdate.ContextWindow)
	}
	if ctxUpdate.TotalTokens < 0 {
		t.Errorf("TotalTokens = %d, want >= 0", ctxUpdate.TotalTokens)
	}
	if ctxUpdate.SystemTokens <= 0 {
		t.Errorf("SystemTokens = %d, want > 0 (system prompt present)", ctxUpdate.SystemTokens)
	}
	if ctxUpdate.HistoryTokens <= 0 {
		t.Errorf("HistoryTokens = %d, want > 0 (user message present)", ctxUpdate.HistoryTokens)
	}
	if ctxUpdate.CompletionTokens != 0 {
		t.Errorf("CompletionTokens = %d, want 0 (set by caller when known)", ctxUpdate.CompletionTokens)
	}
}

func TestContextUpdate_UsesCalibratedStore(t *testing.T) {
	t.Parallel()

	// A long system prompt so token estimates are well above the minimum of 1.
	const systemPrompt = "You are a helpful assistant. You always answer concisely. "
	// Calibrate the model toward CPT 8.0 (default is 4.0) so the same text
	// estimates to roughly half the default token count.
	store := tokenizer.NewCalibrationStore()
	for i := 0; i < 50; i++ {
		store.Update("test-model", 8.0)
	}

	run := func(useCalibration bool) *tokenizer.ContextUpdate {
		t.Helper()
		sseState := runstate.New()
		w := runstate.NewWriter(sseState)

		client := newMockClient([]mockTurn{
			{tokens: []tokenEvent{{content: "answer"}}},
		})

		sessionMgr := history.NewSessionManager(0)
		sessionID := "test-session-calibrated"
		sessionMgr.Create(sessionID)
		sessionMgr.SetSystemPrompt(sessionID, systemPrompt)
		sessionMgr.AppendUser(sessionID, "hello")

		req := &litellm.Request{Model: "test-model"}

		opts := RunOpts{
			HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
			Confirmer:     nil,
			UISessionMgr:  nil,
			SessionID:     sessionID,
			ContextWindow: 128000,
			CrashDumpFunc: nil,
			Turns:         nil,
		}
		if useCalibration {
			opts.CalibrationStore = store
			opts.ModelName = "test-model"
		}

		err := RunAgent(context.Background(), RunSpec{
			Client:     client,
			Request:    req,
			MaxTurns:   5,
			MaxHistory: 0,
			SSEWriter:  w,
			Tools:      nil,
		}, opts)
		if err != nil {
			t.Fatalf("RunAgent error: %v", err)
		}

		events := collectSSE(sseState)
		for _, evt := range events {
			if evt.Type == "context_update" {
				if data, ok := evt.Data.(*tokenizer.ContextUpdate); ok {
					return data
				}
			}
		}
		t.Fatal("expected context_update event with ContextUpdate data")
		return nil
	}

	calibrated := run(true)
	defaulted := run(false)

	// The same system prompt must estimate to fewer tokens when the calibrated
	// ratio (8.0) is in effect than when the default 4.0 fallback is used.
	if calibrated.SystemTokens >= defaulted.SystemTokens {
		t.Errorf("calibrated SystemTokens (%d) should be less than default (%d)",
			calibrated.SystemTokens, defaulted.SystemTokens)
	}
	if calibrated.TotalTokens >= defaulted.TotalTokens {
		t.Errorf("calibrated TotalTokens (%d) should be less than default (%d)",
			calibrated.TotalTokens, defaulted.TotalTokens)
	}
}

func TestCancelDuringThinking_PreservesAlternation(t *testing.T) {
	t.Parallel()

	// When streaming is cancelled mid-turn (e.g. user clicks stop during thinking),
	// an assistant message must be preserved in history to maintain user→assistant→user
	// message alternation. Without this, the next user prompt creates consecutive
	// user messages which some providers reject as malformed (HTTP 400).

	started := make(chan struct{})
	blockingProv := &blockingMockProvider2{
		content: "thinking...",
		started: started,
	}
	blockingClient, err := litellm.New(blockingProv)
	if err != nil {
		t.Fatalf("failed to create blocking mock client: %v", err)
	}

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-cancel-think"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "analyze code")

	ctx, cancel := context.WithCancel(context.Background())

	req := &litellm.Request{Model: "test-model"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunAgent(ctx, RunSpec{
			Client:     blockingClient,
			Request:    req,
			MaxTurns:   5,
			MaxHistory: 0,
			SSEWriter:  w,
			Tools:      nil,
		}, RunOpts{
			HistoryMgr:    NewSessionHistoryManager(sessionMgr, sessionID),
			Confirmer:     nil,
			UISessionMgr:  nil,
			SessionID:     sessionID,
			ContextWindow: 0,
			CrashDumpFunc: nil,
			Turns:         nil,
		})
	}()

	<-started // Wait for first token to be sent
	cancel()  // Cancel mid-stream (simulates user clicking stop)

	err = <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// An assistant message must be saved even for mid-stream cancel
	hist := sessionMgr.History(sessionID)
	if len(hist) < 3 {
		t.Fatalf("history has %d messages after cancel, want at least 3 (sys + user + assistant)", len(hist))
	}
	if hist[len(hist)-1].Role != "assistant" {
		t.Errorf("last message role = %q, want %q", hist[len(hist)-1].Role, "assistant")
	}

	// Simulate new prompt after cancel
	sessionMgr.AppendUser(sessionID, "new question")
	hist2 := sessionMgr.History(sessionID)

	// Verify no consecutive user messages — would cause 400 errors with some providers
	lastRole := litellm.Role("")
	for _, msg := range hist2 {
		if msg.Role == "user" && lastRole == "user" {
			t.Errorf("Consecutive user messages found — provider would reject as malformed")
			break
		}
		if msg.Role != "system" {
			lastRole = msg.Role
		}
	}
}

// ── Confirmation flow tests ────────────────────────────────────────────────

// needsConfirmTool returns NeedsConfirm on the first call, then
// succeeds on subsequent calls. Used to test the approve path.
type needsConfirmTool struct {
	mu      sync.Mutex
	callNum int
	name    string
	result  string
}

func (t *needsConfirmTool) Name() string        { return t.name }
func (t *needsConfirmTool) Description() string { return "A tool that needs confirmation" }
func (t *needsConfirmTool) JSONSchema() litellm.Schema {
	return litellm.Schema(`{"type":"object","properties":{}}`)
}
func (t *needsConfirmTool) Call(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
	t.mu.Lock()
	n := t.callNum
	t.callNum++
	t.mu.Unlock()
	if n == 0 {
		// First call — needs confirmation
		return tool.NeedsConfirmPath(nil, "/tmp/test.txt", "Allow reading /tmp/test.txt?"), nil
	}
	// Subsequent call — succeeds
	return tool.Success([]litellm.Block{litellm.TextBlock{Text: t.result}}), nil
}

func (t *needsConfirmTool) AppendAllowedPaths(path string) {}

func TestRunAgent_ConfirmationApprovePath(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// LLM: first turn calls needs_confirm_tool, second turn finishes
	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "needs_confirm_tool", `{}`)},
		},
		{tokens: []tokenEvent{{content: "Done reading the file."}}},
	})

	toolReg := tool.NewRegistry()
	confirmTool := &needsConfirmTool{name: "needs_confirm_tool", result: "file content"}
	toolReg.Register(confirmTool)

	// Stub approves
	confirmer := NewTestConfirmerStub(&ConfirmationResult{Path: "/tmp/test.txt", Approved: true}, nil)

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "read file"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     confirmer,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify tool result in conversation history
	if len(req.Messages) < 3 {
		t.Fatalf("req.Messages length = %d, want >= 3 (user + assistant + tool)", len(req.Messages))
	}
	toolMsg := req.Messages[2]
	if msgRole(toolMsg) != "tool" {
		t.Errorf("message[2] role = %q, want %q", msgRole(toolMsg), "tool")
	}
	if msgContent(toolMsg) != "file content" {
		t.Errorf("tool result content = %q, want %q", msgContent(toolMsg), "file content")
	}

	// Verify SSE events include needs_confirmation
	events := collectSSE(sseState)
	foundNeedsConf := false
	for _, evt := range events {
		if evt.Type == "needs_confirmation" {
			foundNeedsConf = true
			break
		}
	}
	if !foundNeedsConf {
		t.Errorf("expected needs_confirmation SSE event, got types: %v", sseEventTypes(events))
	}
}

func TestRunAgent_ConfirmationDenyPath(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// alwaysNeedsConfirmTool always returns NeedsConfirm
	alwaysNeedsTool := &simpleMockTool{
		name: "needs_confirm_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.NeedsConfirmPath(nil, "/tmp/secret.txt", "Allow?"), nil
		},
	}

	client := newMockClient([]mockTurn{
		{
			toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "needs_confirm_tool", `{}`)},
		},
		{tokens: []tokenEvent{{content: "Access denied, will skip."}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(alwaysNeedsTool)

	// Stub denies
	confirmer := NewTestConfirmerStub(&ConfirmationResult{Path: "/tmp/secret.txt", Approved: false}, nil)

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "read secret"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     confirmer,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify access denied message in conversation history
	if len(req.Messages) < 3 {
		t.Fatalf("req.Messages length = %d, want >= 3 (user + assistant + tool)", len(req.Messages))
	}
	toolMsg := req.Messages[2]
	if msgRole(toolMsg) != "tool" {
		t.Errorf("message[2] role = %q, want %q", msgRole(toolMsg), "tool")
	}
	if !strings.Contains(msgContent(toolMsg), "Access denied") {
		t.Errorf("tool result content = %q, want to contain 'Access denied'", msgContent(toolMsg))
	}

	// Verify SSE events include needs_confirmation
	events := collectSSE(sseState)
	foundNeedsConf := false
	for _, evt := range events {
		if evt.Type == "needs_confirmation" {
			foundNeedsConf = true
			break
		}
	}
	if !foundNeedsConf {
		t.Errorf("expected needs_confirmation SSE event, got types: %v", sseEventTypes(events))
	}
}

// ── Tool definition hoisting regression tests ──────────────────────────────

// trackingRegistry implements toolLister and records LitellmTools call count.
type trackingRegistry struct {
	inner *tool.Registry
	calls int
}

func (r *trackingRegistry) LitellmTools() []litellm.Tool {
	r.calls++
	return r.inner.LitellmTools()
}

func TestRunAgent_ToolDefsHoistedOnce(t *testing.T) {
	t.Parallel()
	// Verify that LitellmTools returns correct tool definitions.
	inner := tool.NewRegistry()
	inner.Register(&simpleMockTool{name: "t1", description: "a test tool"})
	inner.Register(&simpleMockTool{name: "t2", description: "another test tool"})

	tracked := &trackingRegistry{inner: inner}

	defs := tracked.LitellmTools()
	if tracked.calls != 1 {
		t.Errorf("LitellmTools call count = %d, want 1", tracked.calls)
	}

	// Verify correct number of tool defs returned
	if len(defs) != 2 {
		t.Errorf("toolDefs count = %d, want 2", len(defs))
	}

	// Verify names and descriptions
	byName := make(map[string]litellm.Tool)
	for _, d := range defs {
		byName[d.Name] = d
	}
	if d, ok := byName["t1"]; !ok {
		t.Errorf("missing tool 't1' in defs")
	} else if d.Description != "a test tool" {
		t.Errorf("t1 description = %q, want 'a test tool'", d.Description)
	}
	if d, ok := byName["t2"]; !ok {
		t.Errorf("missing tool 't2' in defs")
	} else if d.Description != "another test tool" {
		t.Errorf("t2 description = %q, want 'another test tool'", d.Description)
	}
}

func TestRunAgent_ToolDefsAttachedEachTurn(t *testing.T) {
	t.Parallel()
	// Verify that tools are still dispatched correctly even when hoisted once.
	// Tools are computed once before the loop, then reused on every turn.
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "test_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) {
			return tool.Success(tool.TextBlocks("result")), nil
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "run tool"}}},
		},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client: newMockClient([]mockTurn{
			{toolCalls: []litellm.ToolUseBlock{buildMockToolCall("call_1", "test_tool", `{}`)}},
			{tokens: []tokenEvent{{content: "done"}}},
		}),
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      toolReg,
	}, RunOpts{
		HistoryMgr:    NewRequestHistoryManager(req),
		Confirmer:     nil,
		UISessionMgr:  nil,
		SessionID:     "",
		ContextWindow: 0,
		CrashDumpFunc: nil,
		Turns:         nil,
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify that the tool was dispatched and the run completed successfully.
	// The tool call was made and result "result" was fed back to the LLM.
	// After the run, the assistant message with the tool call and the tool
	// result should be in history.
	if len(req.Messages) < 3 {
		t.Fatalf("expected at least 3 messages (user + assistant + tool), got %d", len(req.Messages))
	}
	lastMsg := req.Messages[len(req.Messages)-1]
	if msgRole(lastMsg) != "assistant" {
		t.Errorf("last message role = %q, want %q", msgRole(lastMsg), "assistant")
	}
	if msgContent(lastMsg) != "done" {
		t.Errorf("last message content = %q, want %q", msgContent(lastMsg), "done")
	}
}

// ── Crash dump tests ────────────────────────────────────────────────────────

func TestRunAgent_PanicCallsCrashDumpFunc(t *testing.T) {
	t.Parallel()
	// A mock LLM that panics during ChatStream should trigger crashDumpFunc
	// via RunAgent's deferred recover.

	panickingProvider := &panicLLM{}
	panickingClient, err := litellm.New(panickingProvider)
	if err != nil {
		t.Fatalf("failed to create panic client: %v", err)
	}

	var capturedErr string
	var capturedStack []byte
	crashCalled := false
	crashDumpFunc := func(err error, stack []byte) {
		crashCalled = true
		capturedErr = err.Error()
		capturedStack = stack
	}

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
		lrWithModel("test-model"),
	)

	requirePanic(t, func() {
		_ = RunAgent(context.Background(), RunSpec{
			Client:     panickingClient,
			Request:    req,
			MaxTurns:   5,
			MaxHistory: 0,
			SSEWriter:  w,
			Tools:      nil,
		}, RunOpts{
			HistoryMgr:    NewRequestHistoryManager(req),
			Confirmer:     nil,
			UISessionMgr:  nil,
			SessionID:     "",
			ContextWindow: 0,
			CrashDumpFunc: crashDumpFunc,
			Turns:         nil,
		})
	})

	if !crashCalled {
		t.Fatal("crashDumpFunc was not called on panic")
	}
	if capturedErr == "" {
		t.Error("capturedErr is empty")
	}
	if len(capturedStack) == 0 {
		t.Error("capturedStack is empty")
	}
	if !strings.Contains(capturedErr, "boom") {
		t.Errorf("capturedErr = %q, want to contain 'boom'", capturedErr)
	}
}

func TestRunAgent_PanicNilCrashDumpFuncDoesNotPanic(t *testing.T) {
	t.Parallel()
	// When crashDumpFunc is nil, a panic in RunAgent should still re-panic
	// (not silently swallow), but we verify the deferred recover doesn't panic
	// due to nil func call.

	panickingProvider := &panicLLM{}
	panickingClient, err := litellm.New(panickingProvider)
	if err != nil {
		t.Fatalf("failed to create panic client: %v", err)
	}

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
		},
		lrWithModel("test-model"),
	)

	requirePanic(t, func() {
		_ = RunAgent(context.Background(), RunSpec{
			Client:     panickingClient,
			Request:    req,
			MaxTurns:   5,
			MaxHistory: 0,
			SSEWriter:  w,
			Tools:      nil,
		}, RunOpts{
			HistoryMgr:    NewRequestHistoryManager(req),
			Confirmer:     nil,
			UISessionMgr:  nil,
			SessionID:     "",
			ContextWindow: 0,
			CrashDumpFunc: nil,
			Turns:         nil,
		})
	})
}

// panicLLM panics on every Stream call.
type panicLLM struct {
	litellm.Provider
}

func (m *panicLLM) Name() string { return "panic-llm" }

func (m *panicLLM) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented")
}

func (m *panicLLM) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	panic("boom: something went wrong")
}

// requirePanic asserts that fn panics.
func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic, got none")
		}
	}()
	fn()
}

// ── Calibration tests ───────────────────────────────────────────────────────

func TestUpdateCalibration_NilStore(t *testing.T) {
	t.Parallel()
	updateCalibration(nil, "gpt-4", []litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}}}, &litellm.Usage{InputTokens: 10})
}

func TestUpdateCalibration_EmptyModel(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	updateCalibration(store, "", []litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}}}, &litellm.Usage{InputTokens: 10})
	if cpt := store.Lookup(""); cpt != tokenizer.DefaultCPT {
		t.Errorf("expected default CPT, got %f", cpt)
	}
}

func TestUpdateCalibration_NilUsage(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	updateCalibration(store, "gpt-4", []litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}}}, nil)
	if cpt := store.Lookup("gpt-4"); cpt != tokenizer.DefaultCPT {
		t.Errorf("expected default CPT, got %f", cpt)
	}
}

func TestUpdateCalibration_ZeroPromptTokens(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	updateCalibration(store, "gpt-4", []litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}}}, &litellm.Usage{InputTokens: 0})
	if cpt := store.Lookup("gpt-4"); cpt != tokenizer.DefaultCPT {
		t.Errorf("expected default CPT, got %f", cpt)
	}
}

func TestUpdateCalibration_EmptyMessages(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	updateCalibration(store, "gpt-4", nil, &litellm.Usage{InputTokens: 10})
	if cpt := store.Lookup("gpt-4"); cpt != tokenizer.DefaultCPT {
		t.Errorf("expected default CPT, got %f", cpt)
	}
}

func TestUpdateCalibration_ComputesCorrectCPT(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	messages := []litellm.Message{
		{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a helpful assistant."}}}, // 28 chars
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "What is the weather?"}}},           // 20 chars
		{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "Let me check."}}},             // 13 chars
	}
	// Input text length = 28 + 20 + 13 = 61 chars
	// InputTokens = 10
	// CPT = 61 / 10 = 6.1
	usage := &litellm.Usage{InputTokens: 10}

	updateCalibration(store, "gpt-4", messages, usage)

	cpt := store.Lookup("gpt-4")
	expected := tokenizer.EMAAlpha*6.1 + (1-tokenizer.EMAAlpha)*tokenizer.DefaultCPT
	expected = float64(int(expected*100)) / 100 // match rounding
	if cpt != expected {
		t.Errorf("Lookup() = %f, want %f (observed=6.1, old=%f)", cpt, expected, tokenizer.DefaultCPT)
	}
}

func TestUpdateCalibration_CountsToolResultContent(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()

	// Realistic request shape: a large tool result (e.g. a file read or a
	// sub-agent transcript) dominates the payload, but the system prompt and
	// user text are small. The provider tokenizes the tool result, so the
	// observed chars-per-token must reflect it. Regression: input length used
	// to count TextBlocks only, so a 90KB tool result was measured as ~8KB,
	// collapsing the calibrated CPT to ~0.3 and inflating every later context
	// estimate by an order of magnitude (issue: context panel showed 173k for
	// a ~28k-token conversation).
	systemText := strings.Repeat("You are a review agent. ", 400)        // ~10KB
	toolText := strings.Repeat("package main\n\nfunc main() {}\n", 3000) // ~72KB
	messages := []litellm.Message{
		{Role: litellm.Role("system"), Blocks: []litellm.Block{litellm.TextBlock{Text: systemText}}},
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "review this"}}},
		{Role: litellm.Role("tool"), Blocks: []litellm.Block{litellm.ToolResultBlock{ToolUseID: "call_1", Content: []litellm.Block{litellm.TextBlock{Text: toolText}}}}},
	}

	inputLen := len(systemText) + len("review this") + len(toolText)
	// Provider truth: ~3.5 chars/token for this payload.
	inputTokens := inputLen / 3
	// Repeat so the EMA converges toward the observed ratio; a single update
	// is masked by the 4.0 default (alpha=0.3).
	for i := 0; i < 10; i++ {
		updateCalibration(store, "gpt-4", messages, &litellm.Usage{InputTokens: inputTokens})
	}

	// Sanity: calibrated CPT must track the true ratio, not collapse toward 0.
	cpt := store.Lookup("gpt-4")
	if cpt < 2.5 {
		t.Errorf("Lookup() = %f, want >= 2.5 (tool result content was excluded from input length)", cpt)
	}
}

func TestUpdateCalibration_RejectsImplausibleCPT(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	messages := []litellm.Message{
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}},
	}

	// 2 chars / 50 tokens = 0.04 chars/token — physically impossible, must be
	// a measurement mismatch. The store must stay at the default so later
	// context estimates are not poisoned.
	updateCalibration(store, "gpt-4", messages, &litellm.Usage{InputTokens: 50})
	if cpt := store.Lookup("gpt-4"); cpt != tokenizer.DefaultCPT {
		t.Errorf("Lookup() = %f, want default %f (implausible observation was accepted)", cpt, tokenizer.DefaultCPT)
	}
}

func TestUpdateCalibration_MultipleUpdates(t *testing.T) {
	t.Parallel()
	store := tokenizer.NewCalibrationStore()
	messages := []litellm.Message{
		{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "Hello, world!"}}},
	}

	// First update: len=13, tokens=3 → CPT≈4.33
	updateCalibration(store, "gpt-4", messages, &litellm.Usage{InputTokens: 3})
	cpt1 := store.Lookup("gpt-4")
	if cpt1 == tokenizer.DefaultCPT {
		t.Error("CPT should have diverged from default after first update")
	}

	// Second update: same messages, tokens=5 → CPT=2.6
	updateCalibration(store, "gpt-4", messages, &litellm.Usage{InputTokens: 5})
	cpt2 := store.Lookup("gpt-4")
	if cpt2 == cpt1 {
		t.Error("CPT should have changed after second update")
	}
}

// TestRunAgent_CalibrationUpdate verifies that the CalibrationStore is NOT
// updated when the provider does not return usage data in the Done event.
func TestRunAgent_CalibrationUpdate(t *testing.T) {
	t.Parallel()

	store := tokenizer.NewCalibrationStore()

	client := newMockClient([]mockTurn{
		{
			tokens: []tokenEvent{{content: "Hello! How can I help you?"}},
		},
	})

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "Say hello"}}},
		},
		lrWithModel("gpt-4"),
	)

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   1,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:       NewRequestHistoryManager(req),
		Confirmer:        nil,
		UISessionMgr:     nil,
		SessionID:        "",
		ContextWindow:    0,
		CrashDumpFunc:    nil,
		Turns:            nil,
		CalibrationStore: store,
		ModelName:        "gpt-4",
	})
	if err != nil {
		t.Fatalf("RunAgent failed: %v", err)
	}

	// The mock sends StreamEventTypeDone without Usage data, so calibration
	// should NOT have been updated (store.Lookup returns default).
	cpt := store.Lookup("gpt-4")
	if cpt != tokenizer.DefaultCPT {
		t.Errorf("CPT should remain default when provider returns no usage, got %f", cpt)
	}
}

// mockLLMWithUsageProvider implements litellm.Provider returning usage data.
type mockLLMWithUsageProvider struct {
	msg   string
	usage *litellm.Usage
}

func (m *mockLLMWithUsageProvider) Name() string { return "mock-usage-provider" }

func (m *mockLLMWithUsageProvider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	var usage litellm.Usage
	if m.usage != nil {
		usage = *m.usage
	}
	return &litellm.Response{
		Blocks: []litellm.Block{litellm.TextBlock{Text: m.msg}},
		Usage:  usage,
	}, nil
}

func (m *mockLLMWithUsageProvider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	// Build a simple stream with content tokens and a DoneEvent carrying usage
	events := []litellm.Event{
		litellm.ContentDelta{Text: m.msg},
	}
	if m.usage != nil {
		events = append(events, litellm.UsageEvent{Usage: *m.usage})
	}
	events = append(events, litellm.DoneEvent{})
	return &mockStream{events: events}, nil
}

// TestRunAgent_CalibrationUpdateWithUsage verifies that the CalibrationStore
// is updated when the provider includes usage data in the stream.
func TestRunAgent_CalibrationUpdateWithUsage(t *testing.T) {
	t.Parallel()

	store := tokenizer.NewCalibrationStore()

	client, err := litellm.New(&mockLLMWithUsageProvider{
		msg:   "Hello!",
		usage: &litellm.Usage{InputTokens: 5, OutputTokens: 10, TotalTokens: 15},
	})
	if err != nil {
		t.Fatalf("failed to create mock client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{
			{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "Say hello"}}},
		},
		lrWithModel("gpt-4"),
	)

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   1,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:       NewRequestHistoryManager(req),
		Confirmer:        nil,
		UISessionMgr:     nil,
		SessionID:        "",
		ContextWindow:    0,
		CrashDumpFunc:    nil,
		Turns:            nil,
		CalibrationStore: store,
		ModelName:        "gpt-4",
	})
	if err != nil {
		t.Fatalf("RunAgent failed: %v", err)
	}

	// Input text: "Say hello" = 9 chars
	// PromptTokens: 5
	// Observed CPT: 9/5 = 1.8
	cpt := store.Lookup("gpt-4")
	expected := tokenizer.EMAAlpha*1.8 + (1-tokenizer.EMAAlpha)*tokenizer.DefaultCPT
	expected = float64(int(expected*100)) / 100
	if cpt != expected {
		t.Errorf("CPT = %f, want %f", cpt, expected)
	}
}

// ensure tokenizer import is used
var _ = tokenizer.NewCalibrationStore

// blockingStream is a litellm.Stream that blocks in Next() until the context
// is cancelled — simulating a provider that streams reasoning forever without
// ever yielding a content delta, tool call, or done event (the turn-stall case).
type blockingStream struct {
	ctx context.Context
}

func (s *blockingStream) Next() (litellm.Event, error) {
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

func (s *blockingStream) Close() error { return nil }

// stallProvider returns a Stream that blocks on the context passed to Stream().
type stallProvider struct{}

func (p *stallProvider) Name() string { return "stall" }

func (p *stallProvider) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented for stallProvider")
}

func (p *stallProvider) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	return &blockingStream{ctx: ctx}, nil
}

// TestIsReasoningModel documents the gate that keeps Eitri aligned with litellm's
// openai provider isReasoningModel (gpt-5*). Only those models accept a client-.
// side Thinking budget; deepseek/o1/qwen reasoning is server-side default and
// must NOT have Thinking set (litellm rejects it with
// "thinking is only supported for reasoning chat models").
func TestIsReasoningModel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		model string
		want  bool
	}{
		{"gpt-5", true},
		{"gpt-5.1", true},
		{"deepseek-v4-flash", false},
		{"deepseek-reasoner", false},
		{"o1", false},
		{"o3-mini", false},
		{"qwen3", false},
		{"minimax", false},
		{"gpt-4o", false},
		{"claude-3", false},
	}
	for _, tc := range cases {
		if got := IsReasoningModel(tc.model); got != tc.want {
			t.Errorf("IsReasoningModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

// TestRunAgent_TurnTimeout verifies that a turn that stalls (streams reasoning
// forever without content/tool/done) is cut off by the per-turn timeout and
// surfaces a TurnTimeoutError instead of hanging.
func TestRunAgent_TurnTimeout(t *testing.T) {
	t.Parallel()

	client, err := litellm.New(&stallProvider{})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}}},
		lrWithModel("deepseek-v4-flash"),
	)
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   3,
		MaxHistory: 0,
		SSEWriter:  w,
	}, RunOpts{
		HistoryMgr:  NewRequestHistoryManager(req),
		TurnTimeout: 200 * time.Millisecond,
	})

	var toErr *TurnTimeoutError
	if !errors.As(err, &toErr) {
		t.Fatalf("RunAgent error = %v, want TurnTimeoutError", err)
	}
	if toErr.Timeout != 200*time.Millisecond {
		t.Errorf("TurnTimeoutError.Timeout = %v, want 200ms", toErr.Timeout)
	}

	// A stall error must be surfaced via SSE as an error event.
	events := sseEventTypes(collectSSE(sseState))
	found := false
	for _, e := range events {
		if e == "error" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected an SSE error event, got %v", events)
	}
}

// doneEventFrom returns the done SSE event from a collected event list.
func doneEventFrom(events []runstate.SSEEvent) *runstate.SSEEvent {
	for i := range events {
		if events[i].Type == "done" {
			return &events[i]
		}
	}
	return nil
}

func TestRunAgent_DoneCarriesProviderUsage(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	client := newMockClient([]mockTurn{
		{
			tokens:       []tokenEvent{{content: "hello world"}},
			finishReason: litellm.FinishReasonStop,
			usage:        &litellm.Usage{InputTokens: 100, OutputTokens: 25, TotalTokens: 125, CacheReadTokens: 40},
		},
	})

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}}},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr: NewRequestHistoryManager(req),
		Confirmer:  nil,
		SessionID:  "",
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	done := doneEventFrom(collectSSE(sseState))
	if done == nil {
		t.Fatal("expected a done event")
	}
	if done.Usage == nil {
		t.Fatal("expected provider usage on the done event")
	}
	if done.Usage.PromptTokens != 100 || done.Usage.CompletionTokens != 25 || done.Usage.TotalTokens != 125 {
		t.Fatalf("done usage = %+v, want prompt=100 completion=25 total=125 (provider-reported, not estimate)", done.Usage)
	}
}

func TestRunAgent_DoneFallsBackToEstimateWhenNoUsage(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// No UsageEvent in the stream — the provider returned no usage.
	client := newMockClient([]mockTurn{
		{tokens: []tokenEvent{{content: "hello world"}}},
	})

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "hi"}}}},
		lrWithModel("test-model"),
	)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr: NewRequestHistoryManager(req),
		Confirmer:  nil,
		SessionID:  "",
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	done := doneEventFrom(collectSSE(sseState))
	if done == nil {
		t.Fatal("expected a done event")
	}
	if done.Usage == nil {
		t.Fatal("expected estimated usage on the done event")
	}
	// No store: EstimateUsage uses 4 chars/token over "hello world" (11 chars)
	// → total 2, split 1 prompt + 1 completion.
	if done.Usage.TotalTokens != 2 || done.Usage.PromptTokens != 1 || done.Usage.CompletionTokens != 1 {
		t.Fatalf("done usage = %+v, want estimated total=2 prompt=1 completion=1", done.Usage)
	}
}

func TestRunAgent_AttemptNumberPropagatesThroughRetryLoop(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	inner := newMockClient([]mockTurn{
		{
			tokens:       []tokenEvent{{content: "Hello after retry!"}},
			finishReason: litellm.FinishReasonStop,
			usage:        &litellm.Usage{InputTokens: 10, OutputTokens: 3, TotalTokens: 13},
		},
	})
	transient := &transientErrorLLM{
		transientErr: fmt.Errorf("Provider returned HTTP 500: Internal Server Error"),
		inner:        inner,
	}
	// attemptCaptureLLM records the TraceMeta attached to the request context on
	// each Stream call, so the test can verify the retry loop increments the
	// attempt number per attempt.
	capture := &attemptCaptureLLM{inner: transient}
	client, err := litellm.New(capture)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}}},
		lrWithModel("test-model"),
	)

	err = RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   5,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:  NewRequestHistoryManager(req),
		Confirmer:   nil,
		SessionID:   "",
		RetryPolicy: &RetryPolicy{Attempts: 1, Backoff: 0},
	})
	if err != nil {
		t.Fatalf("RunAgent error after retry: %v", err)
	}

	if len(capture.attempts) != 2 {
		t.Fatalf("Stream calls = %d, want 2 (initial + one retry)", len(capture.attempts))
	}
	if capture.attempts[0] != 0 || capture.attempts[1] != 1 {
		t.Fatalf("attempt numbers = %v, want [0 1]", capture.attempts)
	}
	// The final attempt's meta carries the provider-parsed measurements.
	meta := capture.metas[len(capture.metas)-1]
	if meta.Attempt() != 1 {
		t.Fatalf("final meta Attempt = %d, want 1", meta.Attempt())
	}
	if meta.FinishReason() != string(litellm.FinishReasonStop) {
		t.Fatalf("meta FinishReason = %q, want %q", meta.FinishReason(), litellm.FinishReasonStop)
	}
	if u := meta.Usage(); u == nil || u.PromptTokens != 10 || u.CompletionTokens != 3 {
		t.Fatalf("meta Usage = %+v, want prompt=10 completion=3", u)
	}

	types := sseEventTypes(collectSSE(sseState))
	if len(types) < 2 || types[len(types)-1] != "done" {
		t.Fatalf("expected run to succeed after retry, events: %v", types)
	}
}

// attemptCaptureLLM records the TraceMeta (and its attempt number) attached to
// the request context on every Stream call, then delegates to an inner
// litellm.Provider.
type attemptCaptureLLM struct {
	mu       sync.Mutex
	inner    litellm.Provider
	attempts []int
	metas    []*debug.TraceMeta
}

func (m *attemptCaptureLLM) Name() string { return "attempt-capture" }

func (m *attemptCaptureLLM) Chat(ctx context.Context, req *litellm.Request) (*litellm.Response, error) {
	return m.inner.Chat(ctx, req)
}

func (m *attemptCaptureLLM) Stream(ctx context.Context, req *litellm.Request) (litellm.Stream, error) {
	m.mu.Lock()
	if meta := debug.TraceMetaFromContext(ctx); meta != nil {
		m.attempts = append(m.attempts, meta.Attempt())
		m.metas = append(m.metas, meta)
	}
	m.mu.Unlock()
	return m.inner.Stream(ctx, req)
}

// sseOpenAIStreamChunk is a single OpenAI-style SSE chunk for a streaming
// chat completion.
func sseOpenAIStreamChunk(delta, finishReason string, usage string) string {
	return `data: {"id":"x","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"content":"` + delta + `"},"finish_reason":` + finishReason + `}]` + usage + `}` + "\n\n"
}

// recordedLLMServer returns an httptest.Server that streams a canned OpenAI
// SSE chat completion (with a small delay so TTFB/TTFT are measurable) and
// counts the number of requests it received. When firstAttemptFails is true
// the first request returns HTTP 500 (a transient, retryable error).
func recordedLLMServer(t *testing.T, firstAttemptFails bool) (*httptest.Server, *debug.Recorder, *int) {
	t.Helper()
	rec := debug.NewRecorder(20)
	calls := 0
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		this := calls
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if firstAttemptFails && this == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"upstream burst limit reached"}}`)
			return
		}
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, sseOpenAIStreamChunk("hi", "null", ""))
		fmt.Fprint(w, sseOpenAIStreamChunk("", `"stop"`, ""))
		fmt.Fprint(w, sseOpenAIStreamChunk("", "null", `,"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}`))
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)

	return srv, rec, &calls
}

// recordedClient builds a litellm.Client whose HTTP transport records traces
// into rec under the given session, pointing at the provided SSE server.
func recordedClient(t *testing.T, srv *httptest.Server, rec *debug.Recorder, sessionID string) *litellm.Client {
	t.Helper()
	client, err := provider.NewLitellmClient(provider.LitellmConfig{
		ProviderID:   "custom_openai",
		Model:        "test-model",
		BaseURL:      srv.URL,
		APIKey:       "test-key",
		RoundTripper: debug.NewRecordingRoundTripper(nil, rec, sessionID, "custom_openai"),
	})
	if err != nil {
		t.Fatalf("failed to create recorded client: %v", err)
	}
	return client
}

func TestRunAgent_EmitsLLMCallCorrelationEvent(t *testing.T) {
	srv, rec, _ := recordedLLMServer(t, false)
	client := recordedClient(t, srv, rec, "sess-1")

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}}},
		lrWithModel("test-model"),
	)
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   1,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:  NewRequestHistoryManager(req),
		SessionID:   "sess-1",
		RunID:       "run-1",
		RetryPolicy: &RetryPolicy{Attempts: 0, Backoff: 0},
	})
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// The timeline-side correlation event must carry the trace ID.
	var call *runstate.SSEEvent
	for _, evt := range collectSSE(sseState) {
		if evt.Type == "llm_call" {
			e := evt
			call = &e
			break
		}
	}
	if call == nil {
		t.Fatal("expected an llm_call event for the turn")
	}
	info, ok := call.Data.(*runstate.LLMCallInfo)
	if !ok {
		t.Fatalf("llm_call Data = %T, want *runstate.LLMCallInfo", call.Data)
	}
	if info.TraceID == "" {
		t.Fatal("llm_call TraceID is empty")
	}
	if info.Attempt != 0 {
		t.Errorf("llm_call Attempt = %d, want 0 (single attempt)", info.Attempt)
	}
	if info.Attempts != 1 {
		t.Errorf("llm_call Attempts = %d, want 1", info.Attempts)
	}
	if info.TTFBMs <= 0 {
		t.Errorf("llm_call TTFBMs = %d, want > 0", info.TTFBMs)
	}
	if info.TTFTMs <= 0 {
		t.Errorf("llm_call TTFTMs = %d, want > 0", info.TTFTMs)
	}
	if info.DurationMs <= 0 {
		t.Errorf("llm_call DurationMs = %d, want > 0", info.DurationMs)
	}

	// The recorded trace must carry the run/turn correlation IDs and the
	// time-to-first-token derived from request start → first token.
	traces := rec.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]
	if string(tr.ID) != info.TraceID {
		t.Errorf("trace ID %q does not match llm_call trace ID %q", tr.ID, info.TraceID)
	}
	if tr.RunID != "run-1" {
		t.Errorf("trace RunID = %q, want run-1", tr.RunID)
	}
	if tr.Turn != 1 {
		t.Errorf("trace Turn = %d, want 1", tr.Turn)
	}
	if tr.Attempt != 0 {
		t.Errorf("trace Attempt = %d, want 0", tr.Attempt)
	}
	if tr.TTFTMs <= 0 {
		t.Errorf("trace TTFTMs = %d, want > 0", tr.TTFTMs)
	}
	if tr.TTFBMs <= 0 {
		t.Errorf("trace TTFBMs = %d, want > 0", tr.TTFBMs)
	}
}

func TestRunAgent_RetryRecordsTracesAndAttemptCount(t *testing.T) {
	srv, rec, calls := recordedLLMServer(t, true)
	client := recordedClient(t, srv, rec, "sess-1")

	req := lrFromMessages(
		[]litellm.Message{{Role: litellm.Role("user"), Blocks: []litellm.Block{litellm.TextBlock{Text: "test"}}}},
		lrWithModel("test-model"),
	)
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	err := RunAgent(context.Background(), RunSpec{
		Client:     client,
		Request:    req,
		MaxTurns:   1,
		MaxHistory: 0,
		SSEWriter:  w,
		Tools:      nil,
	}, RunOpts{
		HistoryMgr:  NewRequestHistoryManager(req),
		SessionID:   "sess-1",
		RunID:       "run-1",
		RetryPolicy: &RetryPolicy{Attempts: 1, Backoff: 0},
	})
	if err != nil {
		t.Fatalf("RunAgent error after retry: %v", err)
	}

	if *calls != 2 {
		t.Fatalf("LLM calls = %d, want 2 (initial + retry)", *calls)
	}

	// Two traces for the same (run, turn): the first failed, the second
	// succeeded. Both must carry the run/turn correlation IDs.
	traces := rec.List(0, "", "")
	if len(traces) != 2 {
		t.Fatalf("got %d traces, want 2 (one per attempt)", len(traces))
	}
	for _, tr := range traces {
		if tr.RunID != "run-1" {
			t.Errorf("trace RunID = %q, want run-1", tr.RunID)
		}
		if tr.Turn != 1 {
			t.Errorf("trace Turn = %d, want 1", tr.Turn)
		}
	}
	if traces[0].Attempt != 0 {
		t.Errorf("first trace Attempt = %d, want 0", traces[0].Attempt)
	}
	if traces[1].Attempt != 1 {
		t.Errorf("second trace Attempt = %d, want 1", traces[1].Attempt)
	}
	if traces[0].Status != http.StatusInternalServerError {
		t.Errorf("first trace Status = %d, want 500", traces[0].Status)
	}
	if traces[1].Status != http.StatusOK {
		t.Errorf("second trace Status = %d, want 200", traces[1].Status)
	}

	// The llm_call event must reference the successful (second) trace and
	// report the total attempt count so the report can surface retries.
	var call *runstate.SSEEvent
	for _, evt := range collectSSE(sseState) {
		if evt.Type == "llm_call" {
			e := evt
			call = &e
			break
		}
	}
	if call == nil {
		t.Fatal("expected an llm_call event for the turn")
	}
	info, ok := call.Data.(*runstate.LLMCallInfo)
	if !ok {
		t.Fatalf("llm_call Data = %T, want *runstate.LLMCallInfo", call.Data)
	}
	if info.TraceID != string(traces[1].ID) {
		t.Errorf("llm_call TraceID = %q, want the successful attempt's trace %q", info.TraceID, traces[1].ID)
	}
	if info.Attempt != 1 {
		t.Errorf("llm_call Attempt = %d, want 1", info.Attempt)
	}
	if info.Attempts != 2 {
		t.Errorf("llm_call Attempts = %d, want 2", info.Attempts)
	}
}
