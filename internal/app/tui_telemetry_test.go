package app

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tui"
)

type mockTranscript struct{}

func (mockTranscript) WriteTranscript([]byte) error { return nil }

func TestFeedTelemetryBridgesUsageEvent(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "hello"},
			provider.Chunk{Done: true, FinishReason: "stop", Usage: &provider.Usage{
				PromptTokens: 100_000, PromptCacheHitTokens: 90_000, PromptCacheMissTokens: 10_000, CompletionTokens: 5_000,
			}},
		), nil
	}), mockTranscript{})

	te := tui.NewTelemetry("deepseek-v4-flash", "low", true, 250)
	feedEngineEvents(e, te, tui.NewEventFeed())

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}, engine.AgentOptions{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if u, ok := <-te.Updates(); !ok {
		t.Fatal("telemetry channel closed")
	} else if u.Kind != tui.TelemetryTurn {
		t.Fatalf("first update kind = %v, want TelemetryTurn", u.Kind)
	}

	u, ok := <-te.Updates()
	if !ok {
		t.Fatal("telemetry channel closed")
	}
	if u.Kind != tui.TelemetryUsage || u.Hit != 90_000 || u.Miss != 10_000 || u.Output != 5_000 || u.Ctx != 100_000 {
		t.Fatalf("usage update = %+v, want hit=90000 miss=10000 out=5000 ctx=100000", u)
	}
}

func TestFeedTelemetryBridgesTurnEvent(t *testing.T) {
	e := engine.New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{Content: "ok"},
			provider.Chunk{Done: true, FinishReason: "stop"},
		), nil
	}), mockTranscript{})

	te := tui.NewTelemetry("deepseek-v4-flash", "low", true, 250)
	feedEngineEvents(e, te, tui.NewEventFeed())

	for i := 0; i < 2; i++ {
		if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}, engine.AgentOptions{}); err != nil {
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
	events := tui.NewEventFeed()
	feedEngineEvents(e, te, events)

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "hi"}, engine.AgentOptions{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var answers, reasonings []string
loop:
	for {
		select {
		case ev, ok := <-events.Updates():
			if !ok {
				break loop
			}
			if ev.Stream == nil {
				continue // tool observations and telemetry are not stream deltas
			}
			if ev.Stream.Kind == tui.AnswerStream {
				answers = append(answers, ev.Stream.Delta)
			} else if ev.Stream.Kind == tui.ReasoningStream {
				reasonings = append(reasonings, ev.Stream.Delta)
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
	if len(reasonings) != 1 || reasonings[0] != "(thinking not for the answer pane)" {
		t.Errorf("reasoning deltas = %v, want the single thinking chunk", reasonings)
	}
}

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

func TestFeedEngineEventsBridgesToolEvents(t *testing.T) {
	e := engine.New(scriptedToolEditTurn(), mockTranscript{})
	merged := tui.NewEventFeed()
	feedEngineEvents(e, tui.NewTelemetry("deepseek-v4-flash", "low", true, 250), merged)

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "edit"},
		engine.AgentOptions{
			Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "edit"}}},
			Executor: engine.ExecutorFunc(func(_ context.Context, _, _ string) (engine.ToolExecResult, error) {
				return engine.ToolExecResult{Text: "Edit applied to /w/f.go"}, nil
			}),
			MaxTurns: 5,
		}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	// Tool observations arrive on the single merged feed, in arrival order.
	var start *tui.ToolStart
	var res *tui.ToolResult
	for {
		ev, ok := <-merged.Updates()
		if !ok {
			t.Fatal("merged feed channel closed")
		}
		if ev.Tool == nil {
			continue // stream deltas and telemetry events carry no tool observation
		}
		if ev.Tool.Start != nil {
			start = ev.Tool.Start
		}
		if ev.Tool.Result != nil {
			res = ev.Tool.Result
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
