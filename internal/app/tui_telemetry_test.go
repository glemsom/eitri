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
	feedEngineEvents(e, te, tui.NewStreamer())

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
	feedEngineEvents(e, te, tui.NewStreamer())

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
// TUI streaming pane's channel, in stream order, while reasoning deltas are not
// leaked into the answer pane (issue #83).
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
	feedEngineEvents(e, te, stream)

	if _, err := e.Run(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// The two answer deltas arrive in stream order.
	want := []string{"Hello ", "**glad** to help."}
	for _, w := range want {
		u, ok := <-stream.Updates()
		if !ok {
			t.Fatalf("stream channel closed before %q", w)
		}
		if u.Delta != w {
			t.Fatalf("answer delta = %q, want %q", u.Delta, w)
		}
	}
	// Reasoning content must never surface on the answer pane channel.
	select {
	case u := <-stream.Updates():
		t.Fatalf("unexpected extra stream update: %+v (reasoning must not leak into answer pane)", u)
	default:
	}
}
