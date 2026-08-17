package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// headRecorder captures every provider request and, for the multi-turn loop,
// the exact byte sequence of each request's head (system + tools + verbatim
// prior turns) so a test can assert the cache-prefix invariant.
type headRecorder struct {
	reqs []provider.Request
}

// TestRunOpensWithSystemPrompt asserts the non-tool run path opens its message
// list with the embedded system prompt at [0]: the
// provider must see RoleSystem first, whose content is byte-identical to the
// embedded source.
func TestRunOpensWithSystemPrompt(t *testing.T) {
	cap := &headRecorder{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	if _, err := e.Run(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(cap.reqs))
	}
	assertSystemPromptHead(t, cap.reqs[0].Messages)
}

// assertSystemPromptHead fails when msgs does not open with the embedded system
// prompt at [0] followed by the caller's user turn.
func assertSystemPromptHead(t *testing.T, msgs []provider.Message) {
	t.Helper()
	if len(msgs) < 2 {
		t.Fatalf("message list = %d entries, want >= 2 (system head + user turn)", len(msgs))
	}
	head := msgs[0]
	if head.Role != provider.RoleSystem {
		t.Fatalf("message[0].Role = %q, want %q", head.Role, provider.RoleSystem)
	}
	if head.Content != SystemPromptContent() {
		t.Fatalf("message[0].Content != embedded system prompt:\ngot =%q\nwant=%q", head.Content, SystemPromptContent())
	}
}

// TestRunJSONObjectModeOpensWithSystemPrompt asserts the JSON Object Mode
// special turn opens with the embedded system prompt at [0], so
// every run path shares the same byte-stable request head.
func TestRunJSONObjectModeOpensWithSystemPrompt(t *testing.T) {
	cap := &headRecorder{}
	p := &capableScripted{
		Scripted: provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
			cap.reqs = append(cap.reqs, req)
			return provider.StreamFunc(provider.Chunk{Content: `{"ok":1}`}, provider.Chunk{FinishReason: "stop", Done: true}), nil
		}),
		supported: []provider.GenerationControl{provider.GenerationControlJSONObjectMode},
	}
	e := New(p, &mockTranscript{})

	if _, err := e.RunJSONObjectMode(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}); err != nil {
		t.Fatalf("RunJSONObjectMode() error = %v, want nil", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(cap.reqs))
	}
	assertSystemPromptHead(t, cap.reqs[0].Messages)
}

// TestRunSamplingPolicyOpensWithSystemPrompt asserts the Sampling Policy
// special turn opens with the embedded system prompt at [0].
func TestRunSamplingPolicyOpensWithSystemPrompt(t *testing.T) {
	cap := &headRecorder{}
	p := &capableScripted{
		Scripted: provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
			cap.reqs = append(cap.reqs, req)
			return provider.StreamFunc(provider.Chunk{Content: "draft"}, provider.Chunk{FinishReason: "stop", Done: true}), nil
		}),
		supported: []provider.GenerationControl{provider.GenerationControlSamplingPolicy},
	}
	e := New(p, &mockTranscript{})

	if _, err := e.RunSamplingPolicy(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, provider.SamplingPolicy{Mode: provider.SamplingTemperature, Value: 0.7}); err != nil {
		t.Fatalf("RunSamplingPolicy() error = %v, want nil", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(cap.reqs))
	}
	assertSystemPromptHead(t, cap.reqs[0].Messages)
}

// TestRunAgentOpensWithSystemPrompt asserts the tool-capable agent loop opens
// each request with the embedded system prompt at [0].
func TestRunAgentOpensWithSystemPrompt(t *testing.T) {
	cap := &headRecorder{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		cap.reqs = append(cap.reqs, req)
		return provider.StreamFunc(provider.Chunk{Content: "answer", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:      []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
		ToolChoice: "auto",
		Executor:   &mockToolRecorder{},
		MaxTurns:   5,
	}); err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(cap.reqs) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(cap.reqs))
	}
	assertSystemPromptHead(t, cap.reqs[0].Messages)
}

// TestRunAgentKeepsStableHeadAcrossTurns drives a multi-turn tool-call loop
// and asserts the request head (system + tools + verbatim prior turns) is
// byte-identical across turns — the prompt-cache invariant the economics hinge
// on. The prior-turn payload appends after the stable
// head, so only the messages [2:] may change; the head itself must not.
func TestRunAgentKeepsStableHeadAcrossTurns(t *testing.T) {
	turn := 0
	var heads [][]provider.Message
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		// The stable request head opens with the system prompt at [0]; snapshot
		// it verbatim so the cache-prefix invariant is asserted across turns.
		head := []provider.Message{req.Messages[0]}
		turn++
		heads = append(heads, head)
		if turn == 1 {
			// First turn calls a tool so the loop spans multiple requests.
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "r"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_t", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "final answer", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
		}}}},
		ToolChoice: "auto",
		Executor:   &mockToolRecorder{},
		MaxTurns:   5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if len(heads) < 2 {
		t.Fatalf("agent turns = %d, want at least 2 multi-turn heads", len(heads))
	}
	// Each turn's head must open with the same embedded system prompt.
	for _, h := range heads {
		if h[0].Role != provider.RoleSystem || h[0].Content != SystemPromptContent() {
			t.Fatalf("turn head = %+v, want RoleSystem + embedded content at [0]", h[0])
		}
	}
	// The head across turns must be byte-identical (cache-prefix invariant).
	ref := heads[0][0].Content
	for i, h := range heads[1:] {
		if h[0].Content != ref {
			t.Errorf("turn %d head differs from turn 0 head (cache-prefix drift)", i+1)
		}
	}
}
