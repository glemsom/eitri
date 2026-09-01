package engine

import (
	"context"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

type eventCollector struct {
	events []Event
}

func (c *eventCollector) on(e Event) { c.events = append(c.events, e) }

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

func TestRunEmitsStreamAndUsageAndTurnEvents(t *testing.T) {
	t.Parallel()
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

	res, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "say hi"}, AgentOptions{})
	if err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if res.Answer != "Hello world" || res.Reasoning != "think" {
		t.Fatalf("Result answer=%q reasoning=%q", res.Answer, res.Reasoning)
	}

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

func compressExec() ToolExecutor {
	return ExecutorFunc(func(_ context.Context, name, _ string) (ToolExecResult, error) {
		if name == "bash" {
			return ToolExecResult{Text: "README.md\ninternal/pkg_a.go\ninternal/pkg_b.go\n+3 more\n", Compressed: true}, nil
		}
		return ToolExecResult{Text: "result:" + name}, nil
	})
}

func TestRunAgentEmitsToolEventsInOrder(t *testing.T) {
	t.Parallel()
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

func TestRunWithoutSubscriberEmitsNoEvents(t *testing.T) {
	t.Parallel()
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(
			provider.Chunk{ReasoningContent: "think"},
			provider.Chunk{Content: "Hello"},
			provider.Chunk{FinishReason: "stop", Done: true,
				Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 1}},
		), nil
	}), &mockTranscript{})

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{}); err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if len(col.events) != 0 {
		t.Fatalf("events emitted without a subscriber: %v", col.eventTypes())
	}
}

func TestSetListenerNilStopsDelivery(t *testing.T) {
	t.Parallel()
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "hi"}, provider.Chunk{Done: true}), nil
	}), &mockTranscript{})
	e.SetListener(col.on)
	e.SetListener(nil) // unsubscribe

	if _, err := e.RunAgent(context.Background(), RunRequest{Model: "deepseek-v4-flash", Prompt: "go"}, AgentOptions{}); err != nil {
		t.Fatalf("run error = %v, want nil", err)
	}
	if len(col.events) != 0 {
		t.Fatalf("events delivered after unsubscribe: %v", col.eventTypes())
	}
}

func TestMaybeCompactEmitsCompactionEvent(t *testing.T) {
	t.Parallel()
	col := &eventCollector{}
	e := New(provider.NewScripted(func(_ context.Context, _ provider.Request) (provider.Stream, error) {
		return provider.StreamFunc(provider.Chunk{Content: "## Objective\nRecovered.\n## Next Move\nRetry.", FinishReason: "stop", Done: true}), nil
	}), &mockTranscript{})
	e.SetListener(col.on)
	messages := []provider.Message{
		{Role: provider.RoleSystem, Content: SystemPromptContent()},
		{Role: provider.RoleUser, Content: "old prompt"},
		{Role: provider.RoleAssistant, Content: "old answer"},
		{Role: provider.RoleTool, ToolCallID: "t1", Content: "tool result"},
		{Role: provider.RoleUser, Content: "mid prompt"},
		{Role: provider.RoleAssistant, Content: "mid answer"},
		{Role: provider.RoleUser, Content: "latest prompt"},
	}

	_, ok := e.maybeCompact(context.Background(), RunRequest{Model: "deepseek-v4-flash"}, AgentOptions{
		Compaction:  compactCfg(),
		OnCompacted: func() {},
	}, messages, true, 1)
	if !ok {
		t.Fatal("forced compaction did not run")
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

func TestRunAgentOverflowKeepsTurnEventsBalanced(t *testing.T) {
	t.Parallel()
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
