package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/litellm"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/tool"
	vocellitellm "github.com/voocel/litellm"
)

// ── Mock LLM service ────────────────────────────────────────────────────────

// mockLLMService simulates an LLM with configurable responses per turn.
type mockLLMService struct {
	mu      sync.Mutex
	turns   []mockTurn
	current int
}

type mockTurn struct {
	tokens    []tokenEvent
	toolCalls []litellm.ToolCall
	err       error
}

type tokenEvent struct {
	content     string
	isReasoning bool
}

func (m *mockLLMService) Chat(ctx context.Context, req litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented for mock, use ChatStream")
}

func (m *mockLLMService) ChatStream(ctx context.Context, req litellm.Request) (<-chan litellm.StreamEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current >= len(m.turns) {
		ch := make(chan litellm.StreamEvent, 1)
		ch <- litellm.StreamEvent{Type: litellm.StreamEventTypeDone}
		close(ch)
		return ch, nil
	}

	turn := m.turns[m.current]
	m.current++

	ch := make(chan litellm.StreamEvent, 10)

	if turn.err != nil {
		ch <- litellm.StreamEvent{Type: litellm.StreamEventTypeError, Error: turn.err}
		close(ch)
		return ch, nil
	}

	// Send text content as token events
	for _, tok := range turn.tokens {
		ch <- litellm.StreamEvent{Type: litellm.StreamEventTypeToken, Content: tok.content, IsReasoning: tok.isReasoning}
	}

	// Send tool calls
	if len(turn.toolCalls) > 0 {
		ch <- litellm.StreamEvent{Type: litellm.StreamEventTypeToolCall, ToolCalls: turn.toolCalls}
	}

	// Send done
	ch <- litellm.StreamEvent{Type: litellm.StreamEventTypeDone}
	close(ch)

	return ch, nil
}

func newMockLLM(turns []mockTurn) *mockLLMService {
	return &mockLLMService{turns: turns}
}

// ── Simple mock tool ────────────────────────────────────────────────────────

type simpleMockTool struct {
	name        string
	description string
	callFunc    func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool)
}

func (m *simpleMockTool) Name() string        { return m.name }
func (m *simpleMockTool) Description() string { return m.description }
func (m *simpleMockTool) JSONSchema() vocellitellm.Schema {
	return vocellitellm.Schema(`{"type":"object","properties":{}}`)
}
func (m *simpleMockTool) Call(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
	if m.callFunc != nil {
		return m.callFunc(ctx, args)
	}
	return []vocellitellm.Block{vocellitellm.TextBlock{Text: "ok"}}, nil, false
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello! How can I help?"}}},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "hi"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 128000)
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

func TestRunAgent_MultiTurn_ToolCallThenResponse(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Turn 1: LLM returns tool call
	// Turn 2: LLM returns final response
	llm := newMockLLM([]mockTurn{
		{
			tokens: []tokenEvent{{content: "Let me check that..."}},
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "test_tool",
					Arguments: `{"input":"test"}`,
				},
			}},
		},
		{
			tokens: []tokenEvent{{content: "The result is 42."}},
		},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name:        "test_tool",
		description: "A test tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "42"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "what is the answer?"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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
	if req.Messages[1].Role != "assistant" {
		t.Errorf("message[1] role = %q, want %q", req.Messages[1].Role, "assistant")
	}
	if req.Messages[2].Role != "tool" {
		t.Errorf("message[2] role = %q, want %q", req.Messages[2].Role, "tool")
	}
	if req.Messages[2].Content != "42" {
		t.Errorf("message[2] content = %q, want %q", req.Messages[2].Content, "42")
	}
	if req.Messages[3].Role != "assistant" {
		t.Errorf("message[3] role = %q, want %q", req.Messages[3].Role, "assistant")
	}
	if req.Messages[3].Content != "The result is 42." {
		t.Errorf("message[3] content = %q, want %q", req.Messages[3].Content, "The result is 42.")
	}
}

func TestRunAgent_MultipleToolCallsPerTurn(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	var execOrder []string
	execMu := sync.Mutex{}

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{
				{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "tool_a", Arguments: `{}`}},
				{ID: "call_2", Type: "function", Function: litellm.FunctionCall{Name: "tool_b", Arguments: `{}`}},
			},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "tool_a",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			execMu.Lock()
			execOrder = append(execOrder, "a")
			execMu.Unlock()
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "a_result"}}, nil, false
		},
	})
	toolReg.Register(&simpleMockTool{
		name: "tool_b",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			execMu.Lock()
			execOrder = append(execOrder, "b")
			execMu.Unlock()
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "b_result"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "run both tools"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{
				{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "failing_tool", Arguments: `{}`}},
			},
		},
		{tokens: []tokenEvent{{content: "I see the error, let me handle it."}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "failing_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "command not found"}}, nil, true
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "run failing tool"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify the tool result message is in history
	// Expected: user, assistant(with tool call), tool(result), assistant(final)
	if len(req.Messages) != 4 {
		t.Fatalf("req.Messages length = %d, want 4 (user + assistant + tool + final assistant)", len(req.Messages))
	}

	if req.Messages[2].Role != "tool" {
		t.Errorf("message[2] role = %q, want %q", req.Messages[2].Role, "tool")
	}
	if req.Messages[2].Content != "command not found" {
		t.Errorf("message[2] content = %q, want %q", req.Messages[2].Content, "command not found")
	}
	// Final assistant message should reference the error
	if req.Messages[3].Content != "I see the error, let me handle it." {
		t.Errorf("message[3] content = %q, want %q", req.Messages[3].Content, "I see the error, let me handle it.")
	}
}

func TestRunAgent_EditToolEmitsFileEditCardComponent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "edit",
					Arguments: `{"path":"test.txt","old_text":"foo","new_text":"bar"}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "edit",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{
				vocellitellm.TextBlock{Text: "Edited file: test.txt\nOLD:\nfoo\nNEW:\nbar"},
			}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "edit the file"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	types := sseEventTypes(events)

	foundComponent := false
	for _, evt := range events {
		if evt.Type == "component" {
			foundComponent = true
			break
		}
	}
	if !foundComponent {
		t.Errorf("expected a component event for edit tool, got types: %v", types)
	}
}

func TestRunAgent_EditToolEmitsFullFileDiff(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "edit",
					Arguments: `{"path":"test.txt","old_text":"foo","new_text":"bar"}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	// Simulate real edit tool behavior: returns FULL_OLD_CONTENT/FULL_NEW_CONTENT blocks
	// which get wrapped in ToolResultBlock by Dispatch.
	fullOld := "line1\nline2\nline3\nfoo\nline5\nline6\nline7"
	fullNew := "line1\nline2\nline3\nbar\nline5\nline6\nline7"
	toolReg.Register(&simpleMockTool{
		name: "edit",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{
				vocellitellm.TextBlock{Text: "FULL_OLD_CONTENT:" + fullOld},
				vocellitellm.TextBlock{Text: "FULL_NEW_CONTENT:" + fullNew},
				vocellitellm.TextBlock{Text: "Edited file: test.txt"},
			}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "edit the file"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	var compData map[string]interface{}
	for _, evt := range events {
		if evt.Type == "component" {
			// Component SSE event has Data containing {kind, name, data}
			if d, ok := evt.Data.(map[string]interface{}); ok {
				if name, _ := d["name"].(string); name == "FileEditCard" {
					if inner, ok := d["data"].(map[string]interface{}); ok {
						compData = inner
						break
					}
				}
			}
		}
	}
	if compData == nil {
		t.Fatal("expected FileEditCard component event, none found")
	}
	// The component data should contain the full file content, not just the snippet
	gotOld, _ := compData["old"].(string)
	gotNew, _ := compData["new"].(string)
	if gotOld != fullOld {
		t.Errorf("component data 'old' = %q (len=%d), want full file content %q (len=%d)", gotOld, len(gotOld), fullOld, len(fullOld))
	}
	if gotNew != fullNew {
		t.Errorf("component data 'new' = %q (len=%d), want full file content %q (len=%d)", gotNew, len(gotNew), fullNew, len(fullNew))
	}
}

func TestRunAgent_EditToolErrorSkipsFileEditCard(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "edit",
					Arguments: `{"path":"test.txt","old_text":"foo","new_text":"bar"}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "I see the error"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "edit",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "file not found"}}, nil, true
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "edit the file"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "component" {
			t.Error("component event should NOT be emitted when edit tool returns error")
			break
		}
	}
}

func TestRunAgent_NonEditToolSkipsFileEditCard(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "write",
					Arguments: `{"path":"test.txt","content":"hello"}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "write",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "Written file: test.txt"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "write the file"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	for _, evt := range events {
		if evt.Type == "component" {
			t.Error("component event should NOT be emitted for non-edit tools")
			break
		}
	}
}

func TestRunAgent_MaxTurnsExceeded(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// LLM keeps making tool calls — will exceed maxTurns
	llm := newMockLLM([]mockTurn{
		{toolCalls: []litellm.ToolCall{{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "loop_tool", Arguments: `{}`}}}},
		{toolCalls: []litellm.ToolCall{{ID: "call_2", Type: "function", Function: litellm.FunctionCall{Name: "loop_tool", Arguments: `{}`}}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "loop_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "ok"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "loop"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 1, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "thinking..."}}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "test"},
		},
	}

	err := RunAgent(ctx, llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ── Blocking mock for cancellation tests ──────────────────────────────────

// blockingMockLLM sends one token event, signals started, then blocks
// until the context is cancelled. Useful for testing partial result preservation.
type blockingMockLLM struct {
	content string
	started chan struct{} // closed after first token is sent
}

func (m *blockingMockLLM) Chat(ctx context.Context, req litellm.Request) (*litellm.Response, error) {
	return nil, fmt.Errorf("Chat not implemented, use ChatStream")
}

func (m *blockingMockLLM) ChatStream(ctx context.Context, req litellm.Request) (<-chan litellm.StreamEvent, error) {
	ch := make(chan litellm.StreamEvent, 1)
	ch <- litellm.StreamEvent{Type: litellm.StreamEventTypeToken, Content: m.content}
	close(m.started)
	return ch, nil
}

func TestRunAgent_PreservesPartialResultOnStreamCancellation(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	started := make(chan struct{})
	llm := &blockingMockLLM{
		content: "Partial response text...",
		started: started,
	}

	ctx, cancel := context.WithCancel(context.Background())

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "test"},
		},
	}

	// Start RunAgent in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- RunAgent(ctx, llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
	}()

	// Wait for streaming to start (first token sent)
	<-started

	// Cancel context mid-stream
	cancel()

	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Verify partial result was appended to conversation history
	if len(req.Messages) != 2 {
		t.Fatalf("req.Messages length = %d, want 2 (user + partial assistant)", len(req.Messages))
	}
	if req.Messages[1].Role != "assistant" {
		t.Errorf("message[1] role = %q, want %q", req.Messages[1].Role, "assistant")
	}
	if !strings.Contains(req.Messages[1].Content, "Partial response") {
		t.Errorf("message[1] content = %q, want to contain 'Partial response'", req.Messages[1].Content)
	}
}

func TestRunAgent_StreamError(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	llm := newMockLLM([]mockTurn{
		{err: fmt.Errorf("rate limit exceeded")},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "test"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "I am a helpful assistant."}}},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "hello"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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

// transientErrorLLM returns a transient error on the first ChatStream call,
// then delegates to a normal mock. Used to test retry logic.
type transientErrorLLM struct {
	mu           sync.Mutex
	calls        int
	transientErr error
	inner        litellm.LLMService
}

func (m *transientErrorLLM) Chat(ctx context.Context, req litellm.Request) (*litellm.Response, error) {
	return m.inner.Chat(ctx, req)
}

func (m *transientErrorLLM) ChatStream(ctx context.Context, req litellm.Request) (<-chan litellm.StreamEvent, error) {
	m.mu.Lock()
	n := m.calls
	m.calls++
	m.mu.Unlock()
	if n == 0 {
		return nil, m.transientErr
	}
	return m.inner.ChatStream(ctx, req)
}

func TestRunAgent_RetryTransientChatStreamError(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	inner := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello after retry!"}}},
	})
	llm := &transientErrorLLM{
		transientErr: fmt.Errorf("Provider returned HTTP 500: Internal Server Error"),
		inner:        inner,
	}

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "test"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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
	llm := &transientErrorLLM{
		transientErr: fmt.Errorf("Provider returned HTTP 400: Error from provider (Console Go): Upstream request failed"),
		inner: newMockLLM([]mockTurn{
			{tokens: []tokenEvent{{content: "should not be reached"}}},
		}),
	}

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "test"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err == nil {
		t.Fatal("expected error for HTTP 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("error should mention 400, got: %v", err)
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

func TestRunAgent_EmptyToolCallList(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// Tool calls with zero length — treated as no tool calls
	llm := newMockLLM([]mockTurn{
		{
			tokens: []tokenEvent{{content: "answer"}},
			toolCalls: []litellm.ToolCall{}, // empty, not nil
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "hi"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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
		{toolCalls: []litellm.ToolCall{{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "loop_tool", Arguments: `{}`}}}},
		{toolCalls: []litellm.ToolCall{{ID: "call_2", Type: "function", Function: litellm.FunctionCall{Name: "loop_tool", Arguments: `{}`}}}},
		{tokens: []tokenEvent{{content: "done"}}},
	}

	llm := newMockLLM(mockTurns)

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "loop_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "ok"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "run"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 0, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}
}

func TestRunAgent_ToolReturnsNoContent(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{
				{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "empty_tool", Arguments: `{}`}},
			},
		},
		{tokens: []tokenEvent{{content: "Tool returned nothing"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "empty_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "run empty tool"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Tool result (message[2]) should have empty content
	if len(req.Messages) >= 3 {
		toolMsg := req.Messages[2]
		if toolMsg.Role != "tool" {
			t.Errorf("message[2] role = %q, want %q", toolMsg.Role, "tool")
		}
		if toolMsg.Content != "" {
			t.Errorf("tool result content = %q, want empty", toolMsg.Content)
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
	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{
				{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "replace", Arguments: `{"filePath":"LICENSE","oldString":"foo","newString":"bar"}`}},
			},
		},
		{tokens: []tokenEvent{{content: "corrected: using edit tool instead"}}},
	})

	toolReg := tool.NewRegistry()
	// Only register "edit", not "replace"
	toolReg.Register(&simpleMockTool{
		name: "edit",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "ok"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "edit the file"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent should not return error for unknown tool, got: %v", err)
	}

	// Verify the LLM got a tool result with the error message
	if len(req.Messages) < 3 {
		t.Fatalf("expected at least 3 messages (user + assistant + tool result), got %d", len(req.Messages))
	}
	toolMsg := req.Messages[2]
	if toolMsg.Role != "tool" {
		t.Errorf("message[2] role = %q, want %q", toolMsg.Role, "tool")
	}
	if !strings.Contains(toolMsg.Content, "Tool error") && !strings.Contains(toolMsg.Content, "unknown tool") {
		t.Errorf("tool result should contain error about unknown tool, got: %q", toolMsg.Content)
	}

	// Final message should be the LLM's self-correction response
	if len(req.Messages) >= 4 {
		finalMsg := req.Messages[len(req.Messages)-1]
		if finalMsg.Role != "assistant" {
			t.Errorf("final message role = %q, want %q", finalMsg.Role, "assistant")
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
			wantThinkingCnt: 2,
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
			wantThinkingCnt: 3,
			wantTokenCnt:    1,
			wantContains:    []string{"Final text."},
			wantNotContains: []string{"Reason 1", "Reason 2", "Reason 3"},
			query:           "what is the answer?",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sseState := runstate.New()
			w := runstate.NewWriter(sseState)

			llm := newMockLLM([]mockTurn{
				{tokens: tt.tokens},
			})

			req := litellm.Request{
				Model: "test-model",
				Messages: []litellm.Message{
					{Role: "user", Content: tt.query},
				},
			}

			err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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

			if thinkingDeltaCount != tt.wantThinkingCnt {
				t.Errorf("thinking_delta count = %d, want %d. Types: %v", thinkingDeltaCount, tt.wantThinkingCnt, types)
			}
			if tokenCount != tt.wantTokenCnt {
				t.Errorf("token count = %d, want %d. Types: %v", tokenCount, tt.wantTokenCnt, types)
			}

			if len(req.Messages) >= 2 {
				lastAssistant := req.Messages[len(req.Messages)-1]
				if lastAssistant.Role == "assistant" {
					for _, want := range tt.wantContains {
						if !strings.Contains(lastAssistant.Content, want) {
							t.Errorf("assistant content = %q, want to contain %q", lastAssistant.Content, want)
						}
					}
					for _, notWant := range tt.wantNotContains {
						if strings.Contains(lastAssistant.Content, notWant) {
							t.Errorf("reasoning content %q leaked into accumulated assistant content: %q", notWant, lastAssistant.Content)
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
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "msg1"},
			{Role: "assistant", Content: "resp1"},
			{Role: "user", Content: "msg2"},
			{Role: "assistant", Content: "resp2"},
			{Role: "user", Content: "msg3"},
			{Role: "assistant", Content: "resp3"},
		},
	}

	trimMessages(req, 4) // cap at 4 non-system messages

	// System prompt must remain
	if len(req.Messages) < 1 || req.Messages[0].Role != "system" {
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
		if req.Messages[idx].Content != exp {
			t.Errorf("Messages[%d].Content = %q, want %q", idx, req.Messages[idx].Content, exp)
		}
	}
}

func TestTrimMessages_WithinCapUnchanged(t *testing.T) {
	msgs := []litellm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
	}
	req := &litellm.Request{Messages: msgs}

	trimMessages(req, 5)

	if len(req.Messages) != 3 {
		t.Errorf("len = %d, want 3 (unchanged)", len(req.Messages))
	}
}

func TestTrimMessages_ZeroOrNegativeIsNoop(t *testing.T) {
	msgs := []litellm.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
	}

	// maxHistory = 0 (no limit)
	req0 := &litellm.Request{Messages: append([]litellm.Message{}, msgs...)}
	trimMessages(req0, 0)
	if len(req0.Messages) != 5 {
		t.Errorf("maxHistory=0: len = %d, want 5", len(req0.Messages))
	}

	// maxHistory = -1 (no limit)
	reqNeg := &litellm.Request{Messages: append([]litellm.Message{}, msgs...)}
	trimMessages(reqNeg, -1)
	if len(reqNeg.Messages) != 5 {
		t.Errorf("maxHistory=-1: len = %d, want 5", len(reqNeg.Messages))
	}
}

func TestTrimMessages_NoSystemPromptIsFine(t *testing.T) {
	req := &litellm.Request{
		Messages: []litellm.Message{
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "u2"},
			{Role: "assistant", Content: "a2"},
			{Role: "user", Content: "u3"},
		},
	}

	trimMessages(req, 2)

	// Should keep only the last 2 non-system messages
	if len(req.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Content != "a2" {
		t.Errorf("Messages[0].Content = %q, want %q", req.Messages[0].Content, "a2")
	}
	if req.Messages[1].Content != "u3" {
		t.Errorf("Messages[1].Content = %q, want %q", req.Messages[1].Content, "u3")
	}
}

func TestRunAgent_SlidingWindowTrimDuringMultiTurn(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// 3 turns: tool call → tool result → final answer
	llm := newMockLLM([]mockTurn{
		{
			tokens: []tokenEvent{{content: "thinking..."}},
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "test_tool",
					Arguments: `{}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "final answer"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "test_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "tool result"}}, nil, false
		},
	})

	// Start with 5 existing messages + system prompt, cap at 3
	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "old1"},
			{Role: "assistant", Content: "old1r"},
			{Role: "user", Content: "old2"},
			{Role: "assistant", Content: "old2r"},
			{Role: "user", Content: "old3"},
			{Role: "assistant", Content: "old3r"},
			{Role: "user", Content: "run tool"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 3, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "render_mermaid_diagram",
					Arguments: `{"code":"graph TD; A-->B;"}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "render_mermaid_diagram",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "Rendered MermaidDiagram"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "render a diagram"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "render_quick_replies",
					Arguments: `{"options":["yes","no"]}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "render_quick_replies",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "Rendered QuickReplies"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "show quick replies"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "render_mermaid_diagram",
					Arguments: `{"code":"graph TD; A-->B;"}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "error occurred"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "render_mermaid_diagram",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "something went wrong"}}, nil, true
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "render a diagram"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "some_other_tool",
					Arguments: `{}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "some_other_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "ok"}}, nil, false
		},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "run tool"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-1"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are a helpful assistant.")
	sessionMgr.AppendUser(sessionID, "hi")

	req := litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newSessionHistoryManager(sessionMgr, nil, sessionID), nil, nil, sessionID, 128000)
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

	llm := newMockLLM([]mockTurn{
		{
			tokens: []tokenEvent{{content: "let me check"}},
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "test_tool",
					Arguments: `{}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "done"}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "test_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "result"}}, nil, false
		},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-2"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "run tool")

	req := litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newSessionHistoryManager(sessionMgr, nil, sessionID), nil, nil, sessionID, 128000)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-3"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "hi")

	req := litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newSessionHistoryManager(sessionMgr, nil, sessionID), nil, nil, sessionID, 0)
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

	llm := newMockLLM([]mockTurn{
		{toolCalls: []litellm.ToolCall{{ID: "call_1", Type: "function", Function: litellm.FunctionCall{Name: "loop_tool", Arguments: `{}`}}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(&simpleMockTool{
		name: "loop_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return []vocellitellm.Block{vocellitellm.TextBlock{Text: "ok"}}, nil, false
		},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-4"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "run tool")

	req := litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), llm, &req, 1, 0, w, toolReg, newSessionHistoryManager(sessionMgr, nil, sessionID), nil, nil, sessionID, 128000)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "Hello!"}}},
	})

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "hi"},
		},
	}

	// No sessionMgr passed
	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newRequestHistoryManager(&req), nil, nil, "", 0)
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

	llm := newMockLLM([]mockTurn{
		{tokens: []tokenEvent{{content: "answer"}}},
	})

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-session-5"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are a helpful assistant.")
	sessionMgr.AppendUser(sessionID, "hello")

	req := litellm.Request{
		Model: "test-model",
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, nil, newSessionHistoryManager(sessionMgr, nil, sessionID), nil, nil, sessionID, 128000)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	events := collectSSE(sseState)
	var ctxUpdate *runstate.ContextUpdate
	for _, evt := range events {
		if evt.Type == "context_update" {
			if data, ok := evt.Data.(*runstate.ContextUpdate); ok {
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


func TestCancelDuringThinking_PreservesAlternation(t *testing.T) {
	t.Parallel()

	// When streaming is cancelled mid-turn (e.g. user clicks stop during thinking),
	// an assistant message must be preserved in history to maintain user→assistant→user
	// message alternation. Without this, the next user prompt creates consecutive
	// user messages which some providers reject as malformed (HTTP 400).

	started := make(chan struct{})
	llm := &blockingMockLLM{
		content: "thinking...",
		started: started,
	}

	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	sessionMgr := history.NewSessionManager(0)
	sessionID := "test-cancel-think"
	sessionMgr.Create(sessionID)
	sessionMgr.SetSystemPrompt(sessionID, "You are helpful.")
	sessionMgr.AppendUser(sessionID, "analyze code")

	ctx, cancel := context.WithCancel(context.Background())

	req := litellm.Request{Model: "test-model"}

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunAgent(ctx, llm, &req, 5, 0, w, nil, newSessionHistoryManager(sessionMgr, nil, sessionID), nil, nil, sessionID, 0)
	}()

	<-started // Wait for first token to be sent
	cancel()  // Cancel mid-stream (simulates user clicking stop)

	err := <-errCh
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
	lastRole := ""
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

// needsConfirmTool returns ErrNeedsConfirmation on the first call, then
// succeeds on subsequent calls. Used to test the approve path.
type needsConfirmTool struct {
	mu      sync.Mutex
	callNum int
	name    string
	result  string
}

func (t *needsConfirmTool) Name() string        { return t.name }
func (t *needsConfirmTool) Description() string { return "A tool that needs confirmation" }
func (t *needsConfirmTool) JSONSchema() vocellitellm.Schema {
	return vocellitellm.Schema(`{"type":"object","properties":{}}`)
}
func (t *needsConfirmTool) Call(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
	t.mu.Lock()
	n := t.callNum
	t.callNum++
	t.mu.Unlock()
	if n == 0 {
		// First call — needs confirmation
		return nil, &tool.ErrNeedsConfirmation{Path: "/tmp/test.txt", Message: "Allow reading /tmp/test.txt?"}, false
	}
	// Subsequent call — succeeds
	return []vocellitellm.Block{vocellitellm.TextBlock{Text: t.result}}, nil, false
}

func (t *needsConfirmTool) AppendAllowedPaths(path string) {}

func TestRunAgent_ConfirmationApprovePath(t *testing.T) {
	t.Parallel()
	sseState := runstate.New()
	w := runstate.NewWriter(sseState)

	// LLM: first turn calls needs_confirm_tool, second turn finishes
	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "needs_confirm_tool",
					Arguments: `{}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "Done reading the file."}}},
	})

	toolReg := tool.NewRegistry()
	confirmTool := &needsConfirmTool{name: "needs_confirm_tool", result: "file content"}
	toolReg.Register(confirmTool)

	// Stub approves
	confirmer := newTestConfirmerStub(&ConfirmationResult{Path: "/tmp/test.txt", Approved: true}, nil)

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "read file"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), confirmer, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify tool result in conversation history
	if len(req.Messages) < 3 {
		t.Fatalf("req.Messages length = %d, want >= 3 (user + assistant + tool)", len(req.Messages))
	}
	toolMsg := req.Messages[2]
	if toolMsg.Role != "tool" {
		t.Errorf("message[2] role = %q, want %q", toolMsg.Role, "tool")
	}
	if toolMsg.Content != "file content" {
		t.Errorf("tool result content = %q, want %q", toolMsg.Content, "file content")
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

	// alwaysNeedsConfirmTool always returns ErrNeedsConfirmation
	alwaysNeedsTool := &simpleMockTool{
		name: "needs_confirm_tool",
		callFunc: func(ctx context.Context, args json.RawMessage) ([]vocellitellm.Block, error, bool) {
			return nil, &tool.ErrNeedsConfirmation{Path: "/tmp/secret.txt", Message: "Allow?"}, false
		},
	}

	llm := newMockLLM([]mockTurn{
		{
			toolCalls: []litellm.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: litellm.FunctionCall{
					Name:      "needs_confirm_tool",
					Arguments: `{}`,
				},
			}},
		},
		{tokens: []tokenEvent{{content: "Access denied, will skip."}}},
	})

	toolReg := tool.NewRegistry()
	toolReg.Register(alwaysNeedsTool)

	// Stub denies
	confirmer := newTestConfirmerStub(&ConfirmationResult{Path: "/tmp/secret.txt", Approved: false}, nil)

	req := litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: "user", Content: "read secret"},
		},
	}

	err := RunAgent(context.Background(), llm, &req, 5, 0, w, toolReg, newRequestHistoryManager(&req), confirmer, nil, "", 0)
	if err != nil {
		t.Fatalf("RunAgent error: %v", err)
	}

	// Verify access denied message in conversation history
	if len(req.Messages) < 3 {
		t.Fatalf("req.Messages length = %d, want >= 3 (user + assistant + tool)", len(req.Messages))
	}
	toolMsg := req.Messages[2]
	if toolMsg.Role != "tool" {
		t.Errorf("message[2] role = %q, want %q", toolMsg.Role, "tool")
	}
	if !strings.Contains(toolMsg.Content, "Access denied") {
		t.Errorf("tool result content = %q, want to contain 'Access denied'", toolMsg.Content)
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


