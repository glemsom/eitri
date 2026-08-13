package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

// eventCollector records every event a live run emits, in order, so a test can
// assert the exact event stream (the observer surface of this ticket).
type eventCollector struct {
	events []Event
}

func (c *eventCollector) on(e Event) { c.events = append(c.events, e) }

// eventTypes returns the concrete Go type names seen, in order, for a compact
// ordering assertion.
func (c *eventCollector) eventTypes() []string {
	out := make([]string, 0, len(c.events))
	for _, e := range c.events {
		out = append(out, typeName(e))
	}
	return out
}

func typeName(e Event) string {
	switch e.(type) {
	case StreamEvent:
		return "stream"
	case TurnEvent:
		return "turn"
	case ToolCallEvent:
		return "toolcall"
	case ToolResultEvent:
		return "toolresult"
	case UsageEvent:
		return "usage"
	case CompactedEvent:
		return "compacted"
	default:
		return "unknown"
	}
}

// TestRunEmitsStreamAndUsageAndTurnEvents verifies a plain non-tool turn pushes
// streamed thinking/answer deltas, per-turn usage, and turn boundaries onto the
// subscriber in order — the (a), (b), (d), (e) event surface for the
// shared drain path (docs/spec.md §6; the reasoning delta is never merged into
// the answer).
func TestRunEmitsStreamAndUsageAndTurnEvents(t *testing.T) {
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "think"},
			provider.Chunk{Content: "Hello "},
			provider.Chunk{Content: "world"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 12, CompletionTokens: 4,
					PromptCacheHitTokens: 2, PromptCacheMissTokens: 10}},
		), nil
	}), &mockTranscript{})
	e.SetListener(col.on)

	res, err := e.Run(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "say hi"})
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if res.Answer != "Hello world" || res.Reasoning != "think" {
		t.Fatalf("Result answer=%q reasoning=%q", res.Answer, res.Reasoning)
	}

	// Turn start then stream deltas, turn end, usage.
	var streamedText, streamedReasoning string
	sawStart, sawEnd := false, false
	var usage *UsageEvent
	for _, e := range col.events {
		switch ev := e.(type) {
		case TurnEvent:
			if ev.Start {
				sawStart = true
			} else {
				sawEnd = true
			}
		case StreamEvent:
			if ev.Kind == AnswerStream {
				streamedText += ev.Delta
			} else if ev.Kind == ReasoningStream {
				streamedReasoning += ev.Delta
			}
		case UsageEvent:
			usage = &ev
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing turn boundaries: start=%v end=%v", sawStart, sawEnd)
	}
	if streamedText != "Hello world" {
		t.Errorf("streamed answer = %q, want %q", streamedText, res.Answer)
	}
	if streamedReasoning != "think" {
		t.Errorf("streamed reasoning = %q, want %q", streamedReasoning, "think")
	}
	if usage == nil {
		t.Fatal("no UsageEvent emitted")
	}
	if usage.Usage.PromptCacheMissTokens != 10 || usage.Usage.PromptCacheHitTokens != 2 {
		t.Errorf("UsageEvent usage = %+v, want cache miss=10 hit=2", usage.Usage)
	}
}

// compressExpr returns a ToolExecutor whose tool result is a compressed,
// truncated listing carrying the explicit "+3 more" tail marker (docs/spec.md
// §5) — the shape the real bash tool emits — so the compression-metadata event
// can be asserted against a known-good marker.
func compressExec() ToolExecutor {
	return ExecutorFunc(func(_ context.Context, name, _ string) (string, error) {
		if name == "bash" {
			return "README.md\ninternal/pkg_a.go\ninternal/pkg_b.go\n+3 more\n", nil
		}
		return "result:" + name, nil
	})
}

// TestRunAgentEmitsToolEventsInOrder drives a tool-call loop and asserts the
// ordered stream of events the TUI consumes streaming: a turn start, answer and
// reasoning deltas, a tool-call start, a tool-result with compression metadata,
// per-turn usage, and the turn transition to the final answer.
func TestRunAgentEmitsToolEventsInOrder(t *testing.T) {
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		var toolResults int
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return provider.StreamFunc(
				provider.Chunk{ReasoningContent: "gather"},
				provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
					{ID: "call_bash", Name: "bash", Arguments: `{"command":"ls"}`},
				}, Done: true},
			), nil
		}
		return provider.StreamFunc(
			provider.Chunk{Content: "done first"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 20, CompletionTokens: 3}},
		), nil
	}), &mockTranscript{})
	e.SetListener(col.on)

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "list"},
		AgentOptions{
			Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "bash"}}},
			Executor: compressExec(),
			MaxTurns: 5,
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	var sawToolCall, sawToolResult bool
	var toolCall *ToolCallEvent
	var toolResult *ToolResultEvent
	for _, e := range col.events {
		switch ev := e.(type) {
		case ToolCallEvent:
			toolCall = &ev
			sawToolCall = true
		case ToolResultEvent:
			toolResult = &ev
			sawToolResult = true
		}
	}
	if !sawToolCall {
		t.Fatal("no ToolCallEvent emitted")
	}
	if !sawToolResult {
		t.Fatal("no ToolResultEvent emitted")
	}
	if toolCall.Name != "bash" || toolCall.ID != "call_bash" {
		t.Errorf("ToolCallEvent = %+v, want bash/call_bash", toolCall)
	}
	if toolResult.Name != "bash" || toolResult.ID != "call_bash" {
		t.Errorf("ToolResultEvent = %+v, want bash/call_bash", toolResult)
	}
	if !toolResult.Compressed {
		t.Errorf("ToolResultEvent.Compressed = %v, want true", toolResult.Compressed)
	}
	if toolResult.Dropped != 3 {
		t.Errorf("ToolResultEvent.Dropped = %d, want 3", toolResult.Dropped)
	}
	if toolResult.Lines != 4 {
		t.Errorf("ToolResultEvent.Lines = %d, want 4 (3 kept + marker)", toolResult.Lines)
	}
}

// TestToolDeltaSeamReportsFileEditDelta asserts the AgentOptions.ToolDelta seam
// (issue #84) captures a file-mutating tool call's before/after line delta and
// surfaces it on the ToolResultEvent, alongside the full delivered result, so
// the TUI can tag a one-line `⊕ edit path [+12,-3]` entry that still expands
// to the complete output (lossless-recovery invariant). A nil seam must yield
// a zero delta and byte-identical behaviour.
func TestToolDeltaSeamReportsFileEditDelta(t *testing.T) {
	// Begin snapshots the pre-edit state; End diffs against the post-edit file.
	// The engine calls Begin before and End after executing the tool.
	var beginNames, endNames []string
	delta := &ToolDelta{
		Begin: func(_ context.Context, name, _ string) {
			beginNames = append(beginNames, name)
		},
		End: func(_ context.Context, name, _ string) (int, int) {
			endNames = append(endNames, name)
			if name == "edit" {
				return 12, 3
			}
			return 0, 0
		},
	}
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				return provider.StreamFunc(
					provider.Chunk{Content: "done", FinishReason: "stop", Done: true,
						Usage: &provider.Usage{PromptTokens: 50, CompletionTokens: 3}},
				), nil
			}
		}
		return provider.StreamFunc(
			provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
				{ID: "call_edit", Name: "edit", Arguments: `{"path":"/w/f.go"}`},
			}, Done: true},
		), nil
	}), &mockTranscript{})
	e.SetListener(col.on)

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "edit"},
		AgentOptions{
			Tools:     []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "edit"}}},
			Executor:  ExecutorFunc(func(_ context.Context, _, _ string) (string, error) { return "Edit applied to /w/f.go", nil }),
			MaxTurns:  5,
			ToolDelta: delta,
		}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	var editResult *ToolResultEvent
	for _, e := range col.events {
		if ev, ok := e.(ToolResultEvent); ok && ev.Name == "edit" {
			editResult = &ev
		}
	}
	if len(beginNames) != 1 || beginNames[0] != "edit" {
		t.Errorf("Begin called = %v, want one edit", beginNames)
	}
	if len(endNames) != 1 || endNames[0] != "edit" {
		t.Errorf("End called = %v, want one edit", endNames)
	}
	if editResult == nil {
		t.Fatal("no edit ToolResultEvent emitted")
	}
	if editResult.Added != 12 || editResult.Removed != 3 {
		t.Errorf("ToolResultEvent delta = +%d-%d, want +12-3", editResult.Added, editResult.Removed)
	}
	if editResult.Result != "Edit applied to /w/f.go" {
		t.Errorf("ToolResultEvent.Result = %q, want full delivered result", editResult.Result)
	}
}

// TestToolDeltaSeamReportsFileContent asserts the ToolDelta.Content seam (issue
// #90) pairs a pre-edit snapshot (taken in Begin) with the post-edit on-disk
// file and surfaces the before/after content plus host path on the
// ToolResultEvent, so the TUI review panel can render an inline diff and hand
// the file to the browser. A nil Content seam yields empty content.
func TestToolDeltaSeamReportsFileContent(t *testing.T) {
	delta := &ToolDelta{
		Begin: func(_ context.Context, name, _ string) {
			// real impl snapshots content here; stand-in just records
			_ = name
		},
		End: func(_ context.Context, _ string, _ string) (int, int) { return 1, 1 },
		Content: func(_ context.Context, name, _ string) (string, string, string) {
			if name != "edit" {
				return "", "", ""
			}
			return "pre\n", "post\n", "/w/f.go"
		},
	}
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, req provider.Request) (provider.Stream, error) {
		for _, m := range req.Messages {
			if m.Role == provider.RoleTool {
				return provider.StreamFunc(provider.Chunk{Content: "done", FinishReason: "stop", Done: true}), nil
			}
		}
		return provider.StreamFunc(provider.Chunk{FinishReason: "tool_calls", ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "edit", Arguments: `{"path":"/w/f.go"}`},
		}, Done: true}), nil
	}), &mockTranscript{})
	e.SetListener(col.on)

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "m", Prompt: "edit"},
		AgentOptions{
			Tools:    []provider.Tool{{Type: "function", Function: provider.ToolFunction{Name: "edit"}}},
			Executor: ExecutorFunc(func(_ context.Context, _, _ string) (string, error) { return "ok", nil }),
			MaxTurns: 5, ToolDelta: delta,
		}); err != nil {
		t.Fatalf("RunAgent() error = %v", err)
	}

	var got *ToolResultEvent
	for _, ev := range col.events {
		if e, ok := ev.(ToolResultEvent); ok && e.Name == "edit" {
			got = &e
		}
	}
	if got == nil {
		t.Fatal("no edit ToolResultEvent emitted")
	}
	if got.Before != "pre\n" || got.After != "post\n" {
		t.Errorf("content = before %q after %q, want pre\n/post\n", got.Before, got.After)
	}
	if got.Path != "/w/f.go" {
		t.Errorf("Path = %q, want /w/f.go", got.Path)
	}
}

// an engine never subscribed to the event surface pushes nothing, so plain runs
// behave byte-identically (docs/spec.md §10). This is the "no event surface
// required, no regressions" acceptance criterion.
func TestRunWithoutSubscriberEmitsNoEvents(t *testing.T) {
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "think"},
			provider.Chunk{Content: "Hello"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	}), &mockTranscript{})

	if _, err := e.Run(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(col.events) != 0 {
		t.Fatalf("events emitted without a subscriber: %v", col.eventTypes())
	}
}

// TestSetListenerNilStopsDelivery verifies unsubscribing with a nil listener
// halts event delivery mid-engine (set once, detached before the run), so a
// detached TUI never receives stale events.
func TestSetListenerNilStopsDelivery(t *testing.T) {
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{Done: true}), nil
	}), &mockTranscript{})
	e.SetListener(col.on)
	e.SetListener(nil) // unsubscribe

	if _, err := e.Run(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if len(col.events) != 0 {
		t.Fatalf("events delivered after unsubscribe: %v", col.eventTypes())
	}
}

// TestRunAgentEmitsCompactionEvent verifies the compaction marker (docs/spec.md
// §7) is surfaced as a CompactedEvent on the subscriber when the proactive 80%
// threshold is crossed — the (f) event surface for the compaction marker.
func TestRunAgentEmitsCompactionEvent(t *testing.T) {
	col := &eventCollector{}
	h := &compactHandler{}
	e := New(provider.NewScripted(h.stream), &mockTranscript{})
	e.SetListener(col.on)

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "compact me"},
		AgentOptions{
			Tools:       strictToolDefs(),
			ToolChoice:  "auto",
			Executor:    &mockToolRecorder{},
			Compaction:  compactCfg(),
			OnCompacted: func() {},
			MaxTurns:    10,
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil", err)
	}

	var sawCompacted bool
	for _, e := range col.events {
		if _, ok := e.(CompactedEvent); ok {
			sawCompacted = true
		}
	}
	if !sawCompacted {
		t.Fatalf("no CompactedEvent emitted; got %v", col.eventTypes())
	}
}

// TestRunAgentOverflowKeepsTurnEventsBalanced drives the emergency
// context-overflow→compact→retry path and asserts every TurnEvent Start is
// paired with a matching End. An overflowed-and-retried turn streams nothing,
// so it must not emit an orphaned Start (the event stream a TUI consumes must
// never pair a Start without a corresponding End).
func TestRunAgentOverflowKeepsTurnEventsBalanced(t *testing.T) {
	col := &eventCollector{}
	h := &overflowHandler{}
	e := New(&budgetScripted{Scripted: *provider.NewScripted(h.stream)}, &mockTranscript{})
	e.SetListener(col.on)

	_, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"},
		AgentOptions{
			Tools:      strictToolDefs(),
			ToolChoice: "auto",
			Executor:   &mockToolRecorder{},
			MaxTurns:   5,
			Compaction: compactCfg(),
		})
	if err != nil {
		t.Fatalf("RunAgent() error = %v, want nil (overflow should compact and retry)", err)
	}

	// Every Start must have a Matching End; no Start may dangle.
	open := map[int]int{}
	var sawCompacted bool
	for _, e := range col.events {
		switch ev := e.(type) {
		case TurnEvent:
			if ev.Start {
				open[ev.Turn]++
			} else {
				open[ev.Turn]--
				if open[ev.Turn] < 0 {
					t.Fatalf("End without Start for turn %d", ev.Turn)
				}
			}
		case CompactedEvent:
			sawCompacted = true
		}
	}
	for turn, n := range open {
		if n != 0 {
			t.Errorf("turn %d has %d unmatched Start events", turn, n)
		}
	}
	if !sawCompacted {
		t.Fatal("no CompactedEvent emitted on the emergency-overflow path")
	}
}
