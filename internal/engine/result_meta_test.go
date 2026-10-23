package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// TestRunAgentReportsTurnCount verifies the batch envelope's turn count: the
// number of provider request/response turns a run performed, not the loop
// counter that resets on a granted continuation.
func TestRunAgentReportsTurnCount(t *testing.T) {
	t.Parallel()
	turn := 0
	scripted := provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		turn++
		switch turn {
		case 1:
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_a", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
				}, Done: true},
			), nil
		default:
			return provider.StreamFunc(
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
	if res.Turns != 2 {
		t.Fatalf("Turns = %d, want 2 (tool turn + final turn)", res.Turns)
	}
	if res.Stopped {
		t.Fatal("Stopped = true, want false for a normal completion")
	}
}

func TestRunAgentMarksStoppedResult(t *testing.T) {
	t.Parallel()
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "partial", FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_1", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
			}, Done: true},
		), nil
	}), &mockTranscript{})

	ctx, cancel := context.WithCancel(context.Background())
	res, err := e.RunAgent(ctx, RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []any{"command"},
		}}}},
		Executor: ExecutorFunc(func(_ context.Context, _, _ string) (ToolExecResult, error) {
			cancel()
			return ToolExecResult{Text: "ok"}, nil
		}),
		MaxTurns: 5,
	})
	if err != ErrStopped {
		t.Fatalf("RunAgent() error = %v, want ErrStopped", err)
	}
	if !res.Stopped {
		t.Fatal("Stopped = false, want true for a stopped run")
	}
}

// TestRunAgentCountsOnlyExecutedToolCalls pins the meaning of Result.ToolCalls:
// work the executor performed. A call rejected during validation never reaches
// the executor, so the undeclared call in this turn must not be counted.
func TestRunAgentCountsOnlyExecutedToolCalls(t *testing.T) {
	t.Parallel()
	turn := 0
	scripted := provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		turn++
		if turn == 1 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", Done: true, ToolCalls: []provider.ToolCall{
					{ID: "call_ghost", Type: "function", Name: "undeclared", Arguments: `{}`},
					{ID: "call_bash", Type: "function", Name: "bash", Arguments: `{"command":"ls"}`},
				}},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "final answer", FinishReason: "stop", Done: true},
		), nil
	})

	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: &mockToolRecorder{},
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.ToolCalls != 1 {
		t.Fatalf("ToolCalls = %d, want 1 (the executed bash call, not the rejected undeclared one)", res.ToolCalls)
	}
	if res.Stopped {
		t.Fatal("Stopped = true, want false: a rejected call is not a stop")
	}
}
