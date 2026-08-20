package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

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

func TestRunAgentMaintainsByteIdenticalCacheHead(t *testing.T) {
	t.Parallel()
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

	for i, req := range c.requests {
		if !req.SetCacheKey || req.SessionKey != "sess-abc" {
			t.Errorf("turn %d not opted into session cache: SetCacheKey=%v SessionKey=%q", i, req.SetCacheKey, req.SessionKey)
		}
	}

	head1 := headMessages(c.requests[0].Messages)
	head2 := headMessages(c.requests[1].Messages)
	shared := min(len(head1), len(head2))
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
	if len(head2) != len(head1)+1 {
		t.Errorf("head grew by %d messages, want exactly +1 (assistant turn appended at tail)", len(head2)-len(head1))
	}
}

func TestRunAgentPropagatesPromptCacheUsage(t *testing.T) {
	t.Parallel()
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

func headMessages(msgs []provider.Message) []provider.Message {
	for i, m := range msgs {
		if m.Role == provider.RoleTool {
			return msgs[:i]
		}
	}
	return msgs
}
