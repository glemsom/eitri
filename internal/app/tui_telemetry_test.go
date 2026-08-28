package app

import (
	"context"
	"testing"
	"time"

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

// TestFeedTelemetryUsageOncePerCycle guards against over-counting when a streaming gateway
// attaches a (cumulative) usage object to every SSE chunk: telemetry must receive exactly one
// UsageEvent per provider cycle, carrying the final/last usage — not one per chunk (see engine
// RunAgent, which emits the UsageEvent once after the stream loop).
func TestFeedTelemetryUsageOncePerCycle(t *testing.T) {
	mkUsage := func(prompt, hit, miss, out int) *provider.Usage {
		return &provider.Usage{
			PromptTokens:          prompt,
			PromptCacheHitTokens:  hit,
			PromptCacheMissTokens: miss,
			CompletionTokens:      out,
		}
	}
	// Each cycle streams several chunks, every one carrying a growing cumulative usage (last
	// chunk wins). cycle 1 is a tool call, cycle 2 the tool-result resubmission.
	e := engine.New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		toolTurn := false
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolTurn = true
				break
			}
		}
		if toolTurn {
			return provider.StreamFunc(
				provider.Chunk{Content: "final", Usage: mkUsage(4000, 3072, 928, 300)},
				provider.Chunk{Usage: mkUsage(4609, 3072, 1537, 435)},
				provider.Chunk{Done: true, FinishReason: "stop"},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "one", Usage: mkUsage(1000, 900, 100, 20)},
			provider.Chunk{Content: "two", Usage: mkUsage(2000, 1800, 200, 55)},
			provider.Chunk{Content: "three", Usage: mkUsage(3132, 3072, 60, 92)},
			provider.Chunk{Done: true, FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "c", Name: "bash", Arguments: `{}`},
			}},
		), nil
	}), mockTranscript{})

	te := tui.NewTelemetry("deepseek-v4-flash", "low", true, 250)
	feedEngineEvents(e, te, tui.NewEventFeed())

	if _, err := e.RunAgent(context.Background(), engine.RunRequest{Model: "deepseek-v4-flash", Prompt: "go"},
		engine.AgentOptions{
			Tools: []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
			Executor: engine.ExecutorFunc(func(_ context.Context, _, _ string) (engine.ToolExecResult, error) {
				return engine.ToolExecResult{Text: "ok"}, nil
			}),
			MaxTurns: 5,
		}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	var usage []tui.TelemetryUpdate
	timeout := time.After(5 * time.Second)
	for len(usage) < 2 {
		select {
		case u, ok := <-te.Updates():
			if !ok {
				t.Fatal("telemetry channel closed")
			}
			if u.Kind == tui.TelemetryUsage {
				usage = append(usage, u)
			}
		case <-timeout:
			t.Fatalf("timed out collecting telemetry; got %d UsageEvents", len(usage))
		}
	}
	if len(usage) != 2 {
		t.Fatalf("telemetry received %d UsageEvents, want exactly 2 (one per cycle); got: %+v", len(usage), usage)
	}
	for i, want := range []struct{ hit, miss, out, ctx int }{
		{3072, 60, 92, 3132},
		{3072, 1537, 435, 4609},
	} {
		if u := usage[i]; u.Hit != want.hit || u.Miss != want.miss || u.Output != want.out || u.Ctx != want.ctx {
			t.Errorf("usage %d = hit=%d miss=%d out=%d ctx=%d, want hit=%d miss=%d out=%d ctx=%d",
				i, u.Hit, u.Miss, u.Output, u.Ctx, want.hit, want.miss, want.out, want.ctx)
		}
	}
}
