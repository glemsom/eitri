package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func toolForever(wantRetries int) *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults < wantRetries {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "done", FinishReason: "stop", Done: true},
		), nil
	})
}

func TestRunAgentMaxTurnsAutoDeniesWithoutHook(t *testing.T) {
	t.Parallel()
	e := New(toolForever(100), &mockTranscript{})
	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "loop"},
		AgentOptions{
			Tools:       []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
			Executor:    &mockToolRecorder{},
			MaxTurns:    2,
			CanContinue: nil,
		})
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("RunAgent() error = %v, want ErrMaxTurns (batch auto-deny)", err)
	}
}

func TestRunAgentMaxTurnsContinuesWhenHookGrants(t *testing.T) {
	t.Parallel()
	var granted int
	e := New(toolForever(5), &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "loop"},
		AgentOptions{
			Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
			Executor: &mockToolRecorder{},
			MaxTurns: 2,
			CanContinue: func() bool {
				granted++
				return true
			},
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil after continuation", err)
	}
	if res.Answer != "done" {
		t.Fatalf("Answer = %q, want %q after continuation", res.Answer, "done")
	}
	if granted == 0 {
		t.Fatalf("CanContinue never called; the cap was not exercised")
	}
}

func TestRunAgentMaxTurnsRefusesAfterGrantedBudgets(t *testing.T) {
	t.Parallel()
	var granted int
	e := New(toolForever(100), &mockTranscript{})
	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "loop"},
		AgentOptions{
			Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
			Executor: &mockToolRecorder{},
			MaxTurns: 2,
			CanContinue: func() bool {
				granted++
				return granted <= 1
			},
		})
	if !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("RunAgent() error = %v, want ErrMaxTurns once user declines continuation", err)
	}
}
