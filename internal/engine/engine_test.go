package engine

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type mockTranscript struct {
	lines []string
}

func (m *mockTranscript) WriteTranscript(line []byte) error {
	m.lines = append(m.lines, string(line))
	return nil
}

func TestRunProducesFinalAnswer(t *testing.T) {
	t.Parallel()
	tr := &mockTranscript{}
	e := New(provider.NewFake("../provider/testdata/hello.sse"), tr)

	res, err := e.RunAgent(context.Background(), RunRequest{
		Model:  "deepseek-v4-flash",
		Prompt: "Say hello",
	}, AgentOptions{})
	if err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if res.Answer != "Hello world" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "Hello world")
	}
	if res.Reasoning != "think step by step" {
		t.Fatalf("Reasoning = %q, want %q", res.Reasoning, "think step by step")
	}
	if res.Usage == nil || res.Usage.PromptTokens != 12 {
		t.Fatalf("Usage = %+v, want prompt=12", res.Usage)
	}
	if len(tr.lines) == 0 {
		t.Fatal("transcript never written")
	}
}

func TestRunWritesAnswerToTranscript(t *testing.T) {
	t.Parallel()
	tr := &mockTranscript{}
	e := New(provider.NewFake("../provider/testdata/usage-final.sse"), tr)

	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}, AgentOptions{})
	if err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if res.Answer != "ack" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "ack")
	}
	found := false
	for _, l := range tr.lines {
		if contains(l, "ack") {
			found = true
		}
	}
	if !found {
		t.Fatalf("transcript lines %v do not contain the answer", tr.lines)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type capturedRequests struct {
	reqs []provider.Request
}

func TestRunThreadsThinkingAndEffort(t *testing.T) {
	t.Parallel()
	cap := &capturedRequests{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{
		Model:           "deepseek-v4-flash",
		Prompt:          "go",
		ThinkingEnabled: true,
		ReasoningEffort: "medium", // normalized to high by the provider
	}, AgentOptions{})
	if err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(cap.reqs))
	}
	if !cap.reqs[0].ThinkingEnabled {
		t.Error("engine did not set ThinkingEnabled on the request")
	}
	if cap.reqs[0].ReasoningEffort != "medium" {
		t.Errorf("engine ReasoningEffort = %q, want %q", cap.reqs[0].ReasoningEffort, "medium")
	}
}

func TestRunAgentRejectsDeclaredToolsWithoutExecutorBeforeProviderRequest(t *testing.T) {
	t.Parallel()
	requests := 0
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		requests++
		return provider.StreamFunc(provider.Chunk{Done: true}), nil
	}), &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
	})
	if err == nil || !contains(err.Error(), "tool executor") {
		t.Fatalf("RunAgent() error = %v, want missing tool executor error", err)
	}
	if requests != 0 {
		t.Fatalf("provider requests = %d, want 0", requests)
	}
}

func TestRunAgentPersistsReasoningOnToolTurns(t *testing.T) {
	t.Parallel()
	assistantTurn := 0
	var assistantReasons []string // reasoning_content the engine re-emits per assistant turn
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		for _, m := range req.Messages {
			if m.Role == provider.RoleAssistant {
				assistantReasons = append(assistantReasons, m.ReasoningContent)
			}
		}
		switch assistantTurn {
		case 0:
			assistantTurn++
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "turn-one reasoning"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_a", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
				}, Done: true},
			), nil
		default:
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "turn-two reasoning"},
				provider.Chunk{Content: "final answer", FinishReason: "stop", Done: true},
			), nil
		}
	})

	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
		}}}},
		Executor: &mockToolRecorder{},
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "final answer" {
		t.Fatalf("Answer = %q, want %q", res.Answer, "final answer")
	}
	if res.Reasoning != "turn-two reasoning" {
		t.Fatalf("Result.Reasoning = %q, want the final turn's reasoning", res.Reasoning)
	}
	found := slices.Contains(assistantReasons, "turn-one reasoning")
	if !found {
		t.Fatalf("assistant reasoning re-emitted = %v, want turn-one reasoning preserved on the tool turn", assistantReasons)
	}
}

func TestRunAgentWritesStoppedTranscriptBetweenToolCalls(t *testing.T) {
	t.Parallel()
	tr := &mockTranscript{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "partial", FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_1", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true},
		), nil
	}), tr)

	ctx, cancel := context.WithCancel(context.Background())

	_, err := e.RunAgent(ctx, RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
		}}}},
		Executor: ExecutorFunc(func(_ context.Context, _, _ string) (ToolExecResult, error) {
			cancel() // cancel after tool exec so the between-turns boundary sees stopped
			return ToolExecResult{Text: "ok"}, nil
		}),
		MaxTurns: 5,
	})

	if !errors.Is(err, ErrStopped) {
		t.Fatalf("error = %v, want ErrStopped", err)
	}
	if len(tr.lines) != 1 {
		t.Fatalf("transcript writes = %d, want 1; lines = %v", len(tr.lines), tr.lines)
	}
	if !contains(tr.lines[0], "[stopped]") {
		t.Errorf("transcript = %q, want [stopped] marker", tr.lines[0])
	}
	if !contains(tr.lines[0], "partial") {
		t.Errorf("transcript = %q, want partial content", tr.lines[0])
	}
}

func TestRunAgentCarriesConversationAcrossTurns(t *testing.T) {
	t.Parallel()
	turn := 0
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		turn++
		switch turn {
		case 1:
			return provider.StreamFunc(
				provider.Chunk{Content: "Understood, Glenn.", FinishReason: "stop", Done: true},
			), nil
		case 2:
			answer := "I don't know your name yet."
			if hasMessage(req.Messages, provider.RoleUser, "My name is Glenn") && hasMessage(req.Messages, provider.RoleAssistant, "Understood, Glenn.") {
				answer = "Your name is Glenn."
			}
			return provider.StreamFunc(
				provider.Chunk{Content: answer, FinishReason: "stop", Done: true},
			), nil
		default:
			return provider.StreamFunc(
				provider.Chunk{Content: "unexpected extra turn", FinishReason: "stop", Done: true},
			), nil
		}
	}), &mockTranscript{})

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "My name is Glenn",
		SessionKey: "session-1",
	}, AgentOptions{}); err != nil {
		t.Fatalf("first RunAgent() error = %v, want nil", err)
	}
	res, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "What is my name ?",
		SessionKey: "session-1",
	}, AgentOptions{})
	if err != nil {
		t.Fatalf("second RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "Your name is Glenn." {
		t.Fatalf("follow-up answer = %q, want %q (prior turn must stay in conversation)", res.Answer, "Your name is Glenn.")
	}
}

func hasMessage(msgs []provider.Message, role provider.Role, content string) bool {
	for _, m := range msgs {
		if m.Role == role && m.Content == content {
			return true
		}
	}
	return false
}

func TestClearSessionHistory(t *testing.T) {
	t.Parallel()
	e := New(provider.NewFake("../provider/testdata/hello.sse"), &mockTranscript{})

	e.storeSessionHistory("old", []provider.Message{{Role: provider.RoleUser, Content: "hi"}})
	if len(e.sessionHistory("old")) == 0 {
		t.Fatal("setup failed to store history")
	}

	e.ClearSessionHistory("old")

	if got := e.sessionHistory("old"); len(got) != 0 {
		t.Fatalf("ClearSessionHistory left %d messages, want 0", len(got))
	}
	if got := e.sessionHistory("new"); len(got) != 0 {
		t.Fatalf("ClearSessionHistory affected other key: %d messages", len(got))
	}
}
