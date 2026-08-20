package app

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tui"
)

// scriptedInterleavedTurn scripts the acceptance-criteria stream: a reasoning
// chunk, a tool call, then a second turn with more reasoning and the answer, so
// the engine emits events in the interleaved order
// reasoning -> toolStart -> toolResult -> reasoning -> answer.
func scriptedInterleavedTurn() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				return provider.StreamFunc(
					provider.Chunk{ReasoningContent: "r2"},
					provider.Chunk{Content: "answer", FinishReason: "stop", Done: true},
				), nil
			}
		}
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "r1"},
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_e", Name: "edit", Arguments: `{"path":"/w/f.go"}`},
			}, Done: true},
		), nil
	})
}

func TestFeedEngineEventsMergedArrivalOrder(t *testing.T) {
	e := engine.New(scriptedInterleavedTurn(), mockTranscript{})
	merged := tui.NewEventFeed()
	feedEngineEvents(e, tui.NewTelemetry("deepseek-v4-flash", "low", true, 250),
		tui.NewStreamer(), tui.NewToolFeed(), tui.NewDeltaObserver(nil), merged)

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "go"},
		engine.AgentOptions{
			Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "edit"}}},
			Executor: engine.ExecutorFunc(func(_ context.Context, _, _ string) (engine.ToolExecResult, error) {
				return engine.ToolExecResult{Text: "Edit applied to /w/f.go"}, nil
			}),
			MaxTurns: 5,
		}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	var got []string
	drain := func() bool { // true when the buffer drained
		select {
		case ev := <-merged.Updates():
			switch {
			case ev.Stream != nil:
				if ev.Stream.Kind == tui.ReasoningStream {
					got = append(got, "reasoning:"+ev.Stream.Delta)
				} else {
					got = append(got, "answer:"+ev.Stream.Delta)
				}
			case ev.Tool != nil:
				if ev.Tool.Start != nil {
					got = append(got, "toolStart:"+ev.Tool.Start.Name)
				} else {
					got = append(got, "toolResult:"+ev.Tool.Result.Name)
				}
			}
			return false
		default:
			return true
		}
	}
	for !drain() {
	}

	want := []string{"reasoning:r1", "toolStart:edit", "toolResult:edit", "reasoning:r2", "answer:answer"}
	if len(got) != len(want) {
		t.Fatalf("merged event order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("merged event %d = %q, want %q (full order %v)", i, got[i], want[i], got)
		}
	}
}

func TestFeedEngineEventsMergedNilFeedSkipped(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "hi", FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	// A nil merged feed must not panic: the legacy stream/tool channels carry the events.
	feedEngineEvents(e, tui.NewTelemetry("deepseek-v4-flash", "low", true, 250),
		tui.NewStreamer(), tui.NewToolFeed(), tui.NewDeltaObserver(nil), nil)

	if _, err := e.Run(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
