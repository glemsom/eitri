package app

import (
	"context"
	"github.com/glemsom/eitri/internal/tui/telemetry"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
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
	feedEngineEvents(e, telemetry.NewTelemetry("deepseek-v4-flash", "low", true, 250),
		merged)

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

func TestPushEventPreservesFullBufferEvents(t *testing.T) {
	ch := make(chan tui.Event, 1)
	ch <- tui.Event{RunID: 1, TurnStart: true}
	done := make(chan struct{})
	go func() {
		pushEvent(ch, tui.Event{RunID: 2, Tool: &tui.ToolUpdate{Start: &tui.ToolStart{Name: "bash"}}})
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("pushEvent returned while channel was full; event was dropped")
	case <-time.After(20 * time.Millisecond):
	}
	if got := <-ch; got.RunID != 1 {
		t.Fatalf("first event = %+v, want buffered run 1 event", got)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pushEvent did not deliver after buffer freed")
	}
	if got := <-ch; got.RunID != 2 || got.Tool == nil || got.Tool.Start == nil {
		t.Fatalf("second event = %+v, want preserved tool event", got)
	}
}

func TestFeedEngineEventsMergedCarriesAnswerDelta(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "hi", FinishReason: "stop", Done: true}), nil
	}), mockTranscript{})

	// The merged feed is the engine-to-TUI stream: every answer delta lands on
	// the single FIFO feed the TUI model reads, with no legacy side channels.
	merged := tui.NewEventFeed()
	feedEngineEvents(e, telemetry.NewTelemetry("deepseek-v4-flash", "low", true, 250),
		merged)

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}, engine.AgentOptions{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var got []string
loop:
	for {
		select {
		case ev, ok := <-merged.Updates():
			if !ok {
				break loop
			}
			if ev.Stream != nil && ev.Stream.Kind == tui.AnswerStream {
				got = append(got, ev.Stream.Delta)
			}
		default:
			break loop
		}
	}
	want := []string{"hi"}
	if len(got) != len(want) {
		t.Fatalf("answer deltas on the merged feed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("answer delta %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestToolTimeoutResolvesOnlyForBash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tool string
		args string
		want time.Duration
	}{
		{"bash default", "bash", `{"command":"ls"}`, tools.BashTimeoutDefault},
		{"bash explicit", "bash", `{"command":"ls","timeout":30}`, 30 * time.Second},
		{"bash clamped", "bash", `{"command":"ls","timeout":4000}`, tools.BashTimeoutMax},
		{"bash invalid json", "bash", `{`, 0},
		{"bash bad timeout type", "bash", `{"command":"ls","timeout":"soon"}`, 0},
		{"non-bash has no bound", "open_in_browser", `{"url":"https://example.com"}`, 0},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := toolTimeout(c.tool, c.args); got != c.want {
				t.Fatalf("toolTimeout(%q, %q) = %v, want %v", c.tool, c.args, got, c.want)
			}
		})
	}
}

func TestEngineToolCallEventCarriesBashTimeoutToTUI(t *testing.T) {
	bash := provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
			}
		}
		return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "bash", Arguments: `{"command":"echo hi"}`},
		}, Done: true}), nil
	})
	e := engine.New(bash, mockTranscript{})
	merged := tui.NewEventFeed()
	feedEngineEvents(e, telemetry.NewTelemetry("m", "low", true, 250), merged)

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "m", Prompt: "go"}, engine.AgentOptions{
		Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
		Executor: engine.ExecutorFunc(func(_ context.Context, _, _ string) (engine.ToolExecResult, error) {
			return engine.ToolExecResult{Text: "hi"}, nil
		}),
		MaxTurns: 5,
	}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	// The bash call carried no timeout argument, so the TUI sees the default bound.
	for {
		select {
		case ev := <-merged.Updates():
			if ev.Tool != nil && ev.Tool.Start != nil && ev.Tool.Start.Name == "bash" {
				if ev.Tool.Start.Timeout != tools.BashTimeoutDefault {
					t.Fatalf("bash start timeout = %v, want %v", ev.Tool.Start.Timeout, tools.BashTimeoutDefault)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no bash tool-start event reached the feed")
		}
	}
}
