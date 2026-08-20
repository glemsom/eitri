package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type headRecorder struct {
	reqs []provider.Request
}

func TestRunOpensWithSystemPrompt(t *testing.T) {
	t.Parallel()
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

func TestRunJSONObjectModeOpensWithSystemPrompt(t *testing.T) {
	t.Parallel()
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

func TestRunSamplingPolicyOpensWithSystemPrompt(t *testing.T) {
	t.Parallel()
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

func TestRunAgentOpensWithSystemPrompt(t *testing.T) {
	t.Parallel()
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

func TestRunAgentKeepsStableHeadAcrossTurns(t *testing.T) {
	t.Parallel()
	turn := 0
	var heads [][]provider.Message
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		head := []provider.Message{req.Messages[0]}
		turn++
		heads = append(heads, head)
		if turn == 1 {
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
	for _, h := range heads {
		if h[0].Role != provider.RoleSystem || h[0].Content != SystemPromptContent() {
			t.Fatalf("turn head = %+v, want RoleSystem + embedded content at [0]", h[0])
		}
	}
	ref := heads[0][0].Content
	for i, h := range heads[1:] {
		if h[0].Content != ref {
			t.Errorf("turn %d head differs from turn 0 head (cache-prefix drift)", i+1)
		}
	}
}
