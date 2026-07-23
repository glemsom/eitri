package runstate

import (
	"testing"
)

func TestWriter_ThinkingDelta(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Subscribe before broadcasting so we catch live events.
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	content := "Reasoning about the problem step by step..."
	w.ThinkingDelta(content)

	select {
	case evt := <-ch:
		if evt.Type != "thinking_delta" {
			t.Errorf("event type = %q, want %q", evt.Type, "thinking_delta")
		}
		if evt.Content != content {
			t.Errorf("event content = %q, want %q", evt.Content, content)
		}
	default:
		t.Fatal("no event received after ThinkingDelta")
	}
}

func TestWriter_ThinkingDelta_MultipleDeltas(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	deltas := []string{
		"First reasoning step...",
		"Second reasoning step...",
		"Third reasoning step...",
	}

	for _, d := range deltas {
		w.ThinkingDelta(d)
	}

	for i, want := range deltas {
		select {
		case evt := <-ch:
			if evt.Type != "thinking_delta" {
				t.Errorf("event %d: type = %q, want %q", i, evt.Type, "thinking_delta")
			}
			if evt.Content != want {
				t.Errorf("event %d: content = %q, want %q", i, evt.Content, want)
			}
		default:
			t.Fatalf("event %d: no event received", i)
		}
	}
}

func TestWriter_ThinkingDelta_SubscriberAfterBroadcastGetsHistory(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	content := "Late joiner reasoning..."
	w.ThinkingDelta(content)

	// Late subscriber should get the event replayed from history.
	_, ch, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	select {
	case evt := <-ch:
		if evt.Type != "thinking_delta" {
			t.Errorf("event type = %q, want %q", evt.Type, "thinking_delta")
		}
		if evt.Content != content {
			t.Errorf("event content = %q, want %q", evt.Content, content)
		}
	default:
		t.Fatal("no event received after Subscribe (history replay)")
	}
}

func TestWriter_ThinkingDelta_AccumulatesInBuffer(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Initially empty
	if got := state.ReasoningBufferString(); got != "" {
		t.Errorf("initial reasoning buffer = %q, want empty", got)
	}

	deltas := []string{
		"Thinking step one...",
		"Thinking step two...",
		"Thinking step three...",
	}

	var expected string
	for _, d := range deltas {
		w.ThinkingDelta(d)
		expected += d
	}

	if got := state.ReasoningBufferString(); got != expected {
		t.Errorf("reasoning buffer after 3 deltas = %q, want %q", got, expected)
	}
}

func TestReasoningBuffer_AppendAndRetrieve(t *testing.T) {
	t.Parallel()

	state := New()

	// Test AppendReasoningBuffer directly
	state.AppendReasoningBuffer("part1")
	state.AppendReasoningBuffer("part2")
	state.AppendReasoningBuffer("part3")

	want := "part1part2part3"
	if got := state.ReasoningBufferString(); got != want {
		t.Errorf("ReasoningBufferString = %q, want %q", got, want)
	}
}

func TestReasoningBuffer_DoesNotAffectTextBuffer(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	w.Token("Hello ")
	w.ThinkingDelta("(thinking)")
	w.Token("world")

	// Text buffer should only have tokens
	if got := state.BufferString(); got != "Hello world" {
		t.Errorf("BufferString = %q, want %q", got, "Hello world")
	}

	// Reasoning buffer should only have thinking content
	if got := state.ReasoningBufferString(); got != "(thinking)" {
		t.Errorf("ReasoningBufferString = %q, want %q", got, "(thinking)")
	}
}

func TestState_SubscriberCount_StartsZero(t *testing.T) {
	t.Parallel()

	state := New()
	if got := state.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount() = %d, want 0", got)
	}
}

func TestState_ReplayCount_StartsZero(t *testing.T) {
	t.Parallel()

	state := New()
	if got := state.ReplayCount(); got != 0 {
		t.Errorf("ReplayCount() = %d, want 0", got)
	}
}

func TestState_Subscribe_IncrementsSubscriberCount(t *testing.T) {
	t.Parallel()

	state := New()

	id1, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("first Subscribe returned ok=false")
	}
	if id1 != 0 {
		t.Errorf("first subscriber id = %d, want 0", id1)
	}
	if got := state.SubscriberCount(); got != 1 {
		t.Errorf("SubscriberCount after first Subscribe = %d, want 1", got)
	}

	id2, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("second Subscribe returned ok=false")
	}
	if id2 != 1 {
		t.Errorf("second subscriber id = %d, want 1", id2)
	}
	if got := state.SubscriberCount(); got != 2 {
		t.Errorf("SubscriberCount after second Subscribe = %d, want 2", got)
	}
}

func TestState_Subscribe_DoesNotIncrementSubscriberCountWhenStreamsClosed(t *testing.T) {
	t.Parallel()

	state := New()
	state.BroadcastDone("mid-1", nil)

	// BroadcastDone appends a done event to history, so Subscribe returns ok=true
	// (history available) but does NOT create a new subscriber.
	_, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe after BroadcastDone should return ok=true (history available)")
	}
	if got := state.SubscriberCount(); got != 0 {
		t.Errorf("SubscriberCount after Subscribe to closed stream = %d, want 0", got)
	}
}

func TestState_ReplayCount_IncrementsOnHistoryReplay(t *testing.T) {
	t.Parallel()

	state := New()
	w := NewWriter(state)

	// Broadcast some events to build history
	w.Token("hello")

	// First subscribe — should replay history
	_, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("first Subscribe returned ok=false")
	}
	if got := state.ReplayCount(); got != 1 {
		t.Errorf("ReplayCount after first Subscribe with history = %d, want 1", got)
	}

	// Second subscribe — should replay history again
	_, _, ok = state.Subscribe()
	if !ok {
		t.Fatal("second Subscribe returned ok=false")
	}
	if got := state.ReplayCount(); got != 2 {
		t.Errorf("ReplayCount after second Subscribe with history = %d, want 2", got)
	}
}

func TestState_ReplayCount_DoesNotIncrementWhenHistoryEmpty(t *testing.T) {
	t.Parallel()

	state := New()

	_, _, ok := state.Subscribe()
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}
	if got := state.ReplayCount(); got != 0 {
		t.Errorf("ReplayCount with empty history = %d, want 0", got)
	}
}

func TestState_Counters_ResetOnNewRun(t *testing.T) {
	t.Parallel()

	// Simulate: run completes, new run starts with fresh State
	state1 := New()
	state1.Subscribe()
	state1.Subscribe()
	w := NewWriter(state1)
	w.Token("test")
	state1.Subscribe()

	// Three Subscribe calls: two before history, one after (all create subscribers)
	if got := state1.SubscriberCount(); got != 3 {
		t.Errorf("state1 SubscriberCount = %d, want 3", got)
	}
	if got := state1.ReplayCount(); got != 1 {
		t.Errorf("state1 ReplayCount = %d, want 1", got)
	}

	// New state = fresh run
	state2 := New()
	if got := state2.SubscriberCount(); got != 0 {
		t.Errorf("state2 SubscriberCount = %d, want 0", got)
	}
	if got := state2.ReplayCount(); got != 0 {
		t.Errorf("state2 ReplayCount = %d, want 0", got)
	}
}
