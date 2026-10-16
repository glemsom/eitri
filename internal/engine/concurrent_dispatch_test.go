package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/provider"
)

// TestTurnToolCallsExecuteConcurrently proves a turn's tool calls genuinely
// overlap. Each executor waits at a barrier that only releases once every call
// has started, so a serial dispatch would deadlock until the context deadline;
// the peak-concurrency assertion then fails deterministically (peak == 1)
// instead of relying on timing.
func TestTurnToolCallsExecuteConcurrently(t *testing.T) {
	t.Parallel()
	const calls = 4
	ids := []string{"call_0", "call_1", "call_2", "call_3"}

	var active, peak atomic.Int32
	var arrived atomic.Int32
	release := make(chan struct{})
	exec := ExecutorFunc(func(ctx context.Context, name, _ string) (ToolExecResult, error) {
		cur := active.Add(1)
		for {
			prev := peak.Load()
			if cur <= prev || peak.CompareAndSwap(prev, cur) {
				break
			}
		}
		if arrived.Add(1) == calls {
			close(release)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return ToolExecResult{}, ctx.Err()
		}
		active.Add(-1)
		return ToolExecResult{Text: "result:" + name}, nil
	})

	toolCalls := make([]provider.ToolCall, calls)
	for i, id := range ids {
		toolCalls[i] = provider.ToolCall{ID: id, Name: "bash", Arguments: `{"command":"x"}`}
	}

	var followup []provider.Message
	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if len(toolResultContents(req.Messages)) == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: toolCalls, Done: true},
			), nil
		}
		followup = req.Messages
		return provider.StreamFunc(
			provider.Chunk{Content: "done"},
			provider.Chunk{FinishReason: "stop", Done: true},
		), nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(ctx, RunRequest{Model: "model", Prompt: "go"}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: exec,
		MaxTurns: 5,
	})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}
	if res.Answer != "done" {
		t.Fatalf("Answer = %q, want done", res.Answer)
	}
	if got := peak.Load(); got != calls {
		t.Fatalf("peak concurrent tool executions = %d, want %d: calls ran serially, not concurrently", got, calls)
	}

	var gotIDs []string
	for _, m := range followup {
		if m.Role == provider.RoleTool {
			gotIDs = append(gotIDs, m.ToolCallID)
		}
	}
	if len(gotIDs) != len(ids) {
		t.Fatalf("resubmitted tool results = %v, want %v", gotIDs, ids)
	}
	for i := range ids {
		if gotIDs[i] != ids[i] {
			t.Fatalf("tool results = %v, want call order %v", gotIDs, ids)
		}
	}
}

// TestTurnStopJoinsDispatchedToolCalls verifies a stop cancels already-dispatched
// calls and that RunAgent does not return until every tool goroutine has exited.
func TestTurnStopJoinsDispatchedToolCalls(t *testing.T) {
	t.Parallel()
	var active atomic.Int32
	started := make(chan struct{}, 2)
	exec := ExecutorFunc(func(ctx context.Context, name, _ string) (ToolExecResult, error) {
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return ToolExecResult{}, ctx.Err()
	})

	scripted := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		if len(toolResultContents(req.Messages)) == 0 {
			return provider.StreamFunc(
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_0", Name: "bash", Arguments: `{"command":"x"}`},
					{ID: "call_1", Name: "bash", Arguments: `{"command":"y"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-started
		cancel()
	}()

	e := New(scripted, &mockTranscript{})
	res, err := e.RunAgent(ctx, RunRequest{Model: "model", Prompt: "go"}, AgentOptions{
		Tools:    strictToolDefs(),
		Executor: exec,
		MaxTurns: 5,
	})
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("RunAgent() error = %v, want ErrStopped", err)
	}
	if !res.Stopped {
		t.Fatal("Result.Stopped = false, want true")
	}
	if n := active.Load(); n != 0 {
		t.Fatalf("%d tool goroutines still running after RunAgent returned", n)
	}
}
