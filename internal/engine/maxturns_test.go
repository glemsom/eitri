package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// toolForever emits a bash tool call for the first wantRetries turns, then a
// final answer. toolResults is derived from the current message history, so it
// counts how many tool-call turns have already happened.
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

// TestRunAgentMaxTurnsAutoDeniesWithoutHook verifies the cap is honored at the
// engine seam: with MaxTurns reached and no continuation hook (the batch
// default), RunAgent returns ErrMaxTurns instead of looping forever. This is
// the batch "auto-deny changes" path.
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

// TestRunAgentMaxTurnsContinuesWhenHookGrants verifies the interactive path:
// when CanContinue grants another budget at the cap, the run proceeds past the
// cap instead of erroring ("prompt to continue").
func TestRunAgentMaxTurnsContinuesWhenHookGrants(t *testing.T) {
	t.Parallel()
	// toolForever(5): five tool turns then a final answer. MaxTurns=2 plus an
	// always-granting hook lets the run cross the cap and finish.
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

// TestRunAgentMaxTurnsRefusesAfterGrantedBudgets verifies the interactive path
// also ends: once the hook stops granting (user declined), the run terminates
// with ErrMaxTurns rather than continuing.
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
