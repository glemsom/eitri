package app

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tui"
)

// mockTranscript discards transcript writes for engine tests that don't assert
// the on-disk sink.
type mockTranscript struct{}

func (mockTranscript) WriteTranscript([]byte) error { return nil }

// TestFeedTelemetryBridgesUsageEvent asserts the engine's per-turn UsageEvent
// is forwarded through the telemetry bridge into the TUI status-strip channel
// (issue #86), read-only against the run.
func TestFeedTelemetryBridgesUsageEvent(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "hello"},
			provider.Chunk{Done: true, FinishReason: "stop", Usage: &provider.Usage{
				PromptCacheHitTokens: 90_000, PromptCacheMissTokens: 10_000, CompletionTokens: 5_000,
			}},
		), nil
	}), mockTranscript{})

	te := tui.NewTelemetry("deepseek-v4-flash", "low", true, 250)
	feedEngineEvents(e, te, tui.NewStreamer(), tui.NewToolFeed())

	if _, err := e.Run(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The turn boundary precedes usage in stream order.
	if u, ok := <-te.Updates(); !ok {
		t.Fatal("telemetry channel closed")
	} else if u.Kind != tui.TelemetryTurn {
		t.Fatalf("first update kind = %v, want TelemetryTurn", u.Kind)
	}

	u, ok := <-te.Updates()
	if !ok {
		t.Fatal("telemetry channel closed")
	}
	if u.Kind != tui.TelemetryUsage || u.Hit != 90_000 || u.Miss != 10_000 || u.Output != 5_000 {
		t.Fatalf("usage update = %+v, want hit=90000 miss=10000 out=5000", u)
	}
}

// TestFeedTelemetryBridgesTurnEvent asserts a turn Start boundary is forwarded
// as a TelemetryTurn update (issue #86), so the strip's turns/max stays fresh
// turn over turn.
func TestFeedTelemetryBridgesTurnEvent(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "ok"},
			provider.Chunk{Done: true, FinishReason: "stop"},
		), nil
	}), mockTranscript{})

	te := tui.NewTelemetry("deepseek-v4-flash", "low", true, 250)
	feedEngineEvents(e, te, tui.NewStreamer(), tui.NewToolFeed())

	// Two runs -> one turn-boundary update each, ahead of the usage update.
	for i := 0; i < 2; i++ {
		if _, err := e.Run(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		u, ok := <-te.Updates()
		if !ok {
			t.Fatal("telemetry channel closed")
		}
		if u.Kind != tui.TelemetryTurn {
			t.Fatalf("update kind = %v, want TelemetryTurn", u.Kind)
		}
	}
}

// TestFeedEngineEventsBridgesAnswerDelta asserts the engine's streamed
// AnswerStream deltas are forwarded through the single engine listener into the
// TUI streaming pane's channel as answer-kind updates, in stream order, while
// ReasoningStream deltas are forwarded as reasoning-kind updates — reasoning is
// never leaked into a stream update tagged as answer (issues #83, #85).
func TestFeedEngineEventsBridgesAnswerDelta(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "Hello "},
			provider.Chunk{ReasoningContent: "(thinking not for the answer pane)"},
			provider.Chunk{Content: "**glad** to help."},
			provider.Chunk{Done: true, FinishReason: "stop"},
		), nil
	}), mockTranscript{})

	te := tui.NewTelemetry("deepseek-v4-flash", "low", true, 250)
	stream := tui.NewStreamer()
	feedEngineEvents(e, te, stream, tui.NewToolFeed())

	if _, err := e.Run(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Drain the stream: collect the deltas delivered into the streaming pane,
	// tagging reasoning vs answer so the two channels never cross (issue #83
	// / #85 AC4).
	var answers, reasonings []string
loop:
	for {
		select {
		case u, ok := <-stream.Updates():
			if !ok {
				break loop
			}
			if u.Kind == tui.AnswerStream {
				answers = append(answers, u.Delta)
			} else if u.Kind == tui.ReasoningStream {
				reasonings = append(reasonings, u.Delta)
			}
		default:
			break loop
		}
	}

	wantAnswers := []string{"Hello ", "**glad** to help."}
	if len(answers) != len(wantAnswers) {
		t.Fatalf("answer deltas = %v, want %v", answers, wantAnswers)
	}
	for i := range wantAnswers {
		if answers[i] != wantAnswers[i] {
			t.Errorf("answer delta %d = %q, want %q", i, answers[i], wantAnswers[i])
		}
	}
	// The reasoning delta is delivered as its own reasoning-kind update, not
	// squeezed into the answer channel (issue #85 AC4).
	if len(reasonings) != 1 || reasonings[0] != "(thinking not for the answer pane)" {
		t.Errorf("reasoning deltas = %v, want the single thinking chunk", reasonings)
	}
}

// scriptedToolEditTurn answers with a single edit tool call, then confirms, so
// the app's engine listener can forward the tool events into the TUI tool feed.
func scriptedToolEditTurn() *provider.Scripted {
	return provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				return provider.StreamFunc(
					provider.Chunk{Content: "done", FinishReason: "stop", Done: true},
				), nil
			}
		}
		return provider.StreamFunc(
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_e", Name: "edit", Arguments: `{"path":"/w/f.go"}`},
			}, Done: true},
		), nil
	})
}

// TestFeedEngineEventsBridgesToolEvents asserts the engine's ToolCallEvent and
// ToolResultEvent are forwarded through the single engine listener into the TUI
// tool feed's channel as a paired Start+Result carrying the full result and the
// file line-delta metadata (issue #84), read-only against the run.
func TestFeedEngineEventsBridgesToolEvents(t *testing.T) {
	e := engine.New(scriptedToolEditTurn(), mockTranscript{})
	feed := tui.NewToolFeed()
	feedEngineEvents(e, tui.NewTelemetry("deepseek-v4-flash", "low", true, 250), tui.NewStreamer(), feed)

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "edit"},
		engine.AgentOptions{
			Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "edit"}}},
			Executor: engine.ExecutorFunc(func(_ context.Context, _, _ string) (string, error) {
				return "Edit applied to /w/f.go", nil
			}),
			MaxTurns: 5,
		}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	var start *tui.ToolStart
	var res *tui.ToolResult
	for {
		u, ok := <-feed.Updates()
		if !ok {
			t.Fatal("tool feed channel closed")
		}
		if u.Start != nil {
			start = u.Start
		}
		if u.Result != nil {
			res = u.Result
			break
		}
	}
	if start == nil || start.Name != "edit" {
		t.Fatalf("tool start = %+v, want edit", start)
	}
	if res == nil || res.Name != "edit" {
		t.Fatalf("tool result = %+v, want edit", res)
	}
	if res.Result == "" {
		t.Error("tool result must carry the full delivered result (expand path)")
	}
}
