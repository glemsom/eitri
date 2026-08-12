package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// captureBodies returns a provider that records the exact request of every
// turn it receives, then drives a two-turn tool round trip before a final
// answer. tools stay fixed and only the tail (tool messages) grows, so the
// request head must be byte-identical across turns — the whole point of
// prompt-caching discipline (docs/spec.md §4, ticket T6 #31).
type captureHandler struct {
	requests []provider.Request
}

func (c *captureHandler) stream(ctx context.Context, req provider.Request) (provider.Stream, error) {
	c.requests = append(c.requests, req)
	var toolResults int
	for _, m := range req.Messages {
		if m.Role == provider.RoleTool {
			toolResults++
		}
	}
	switch {
	case toolResults == 0:
		return provider.StreamFunc(
			provider.Chunk{Content: "", ReasoningContent: "calling a tool"},
			provider.Chunk{
				FinishReason: "tool_calls",
				ToolCalls:    []provider.ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"x"}`}},
				Done:         true,
				Usage:        &provider.Usage{PromptTokens: 100, CompletionTokens: 1, PromptCacheHitTokens: 90, PromptCacheMissTokens: 10},
			},
		), nil
	default:
		return provider.StreamFunc(
			provider.Chunk{Content: "", ReasoningContent: "final answer"},
			provider.Chunk{Content: "done", FinishReason: "stop", Done: true, Usage: &provider.Usage{PromptTokens: 110, CompletionTokens: 4, PromptCacheHitTokens: 100, PromptCacheMissTokens: 10}},
		), nil
	}
}

// TestRunAgentMaintainsByteIdenticalCacheHead drives a multi-turn tool loop
// through the engine seam with a session cache key and asserts the emitted
// request head (the stable prefix before the growing tool tail) is
// byte-identical across turns — the prompt-cache invariant (docs/spec.md §4).
func TestRunAgentMaintainsByteIdenticalCacheHead(t *testing.T) {
	c := &captureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	if _, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "read the file",
		SessionKey: "sess-abc",
	}, AgentOptions{
		Tools:      strictToolDefs(),
		ToolChoice: "auto",
		Executor:   &mockToolRecorder{},
		MaxTurns:   10,
	}); err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}

	if len(c.requests) != 2 {
		t.Fatalf("captured %d requests, want 2 turns", len(c.requests))
	}

	// Every turn opts the session into deepseek's prompt cache.
	for i, req := range c.requests {
		if !req.SetCacheKey || req.SessionKey != "sess-abc" {
			t.Errorf("turn %d not opted into session cache: SetCacheKey=%v SessionKey=%q", i, req.SetCacheKey, req.SessionKey)
		}
	}

	// The byte-identical prefix invariant: any message that appears in both
	// turns must be byte-identical, and the only growth is at the tail. Turn 2
	// re-emits turn 1's history verbatim plus a new assistant tool-call turn,
	// so the shared prefix of the two requests must match 1:1.
	head1 := headMessages(c.requests[0].Messages)
	head2 := headMessages(c.requests[1].Messages)
	shared := len(head1)
	if len(head1) > len(head2) {
		shared = len(head2)
	}
	if shared == 0 {
		t.Fatal("no shared request head to compare")
	}
	for i := 0; i < shared; i++ {
		b1, _ := json.Marshal(head1[i])
		b2, _ := json.Marshal(head2[i])
		if string(b1) != string(b2) {
			t.Errorf("request head message %d not byte-identical:\nturn1=%s\nturn2=%s", i, b1, b2)
		}
	}
	// Turn 2's head must be strictly longer (the assistant tool-call turn was
	// appended at the tail), never rewritten in place.
	if len(head2) != len(head1)+1 {
		t.Errorf("head grew by %d messages, want exactly +1 (assistant turn appended at tail)", len(head2)-len(head1))
	}
}

// TestRunAgentPropagatesPromptCacheUsage verifies the per-turn usage
// (including deepseek cache hit/miss tokens) parsed at the provider seam
// reaches the engine Result.
func TestRunAgentPropagatesPromptCacheUsage(t *testing.T) {
	c := &captureHandler{}
	e := New(provider.NewScripted(c.stream), nil)

	res, err := e.RunAgent(context.Background(), RunRequest{
		Model:      "deepseek-v4-flash",
		Prompt:     "read the file",
		SessionKey: "sess-abc",
	}, AgentOptions{
		Tools:      strictToolDefs(),
		ToolChoice: "auto",
		Executor:   &mockToolRecorder{},
		MaxTurns:   10,
	})
	if err != nil {
		t.Fatalf("RunAgent error = %v, want nil", err)
	}
	if res.Usage == nil {
		t.Fatal("final usage not propagated to Result")
	}
	if res.Usage.PromptCacheHitTokens != 100 || res.Usage.PromptCacheMissTokens != 10 {
		t.Fatalf("final cache usage = hit=%d miss=%d, want hit=100 miss=10", res.Usage.PromptCacheHitTokens, res.Usage.PromptCacheMissTokens)
	}
}

// headMessages returns the stable request-head prefix: every message before
// the first role:"tool" result. Tool results are the only part of the request
// that is allowed to grow, so the remainder must be byte-identical across
// turns for the prompt cache to keep hitting.
func headMessages(msgs []provider.Message) []provider.Message {
	for i, m := range msgs {
		if m.Role == provider.RoleTool {
			return msgs[:i]
		}
	}
	return msgs
}
