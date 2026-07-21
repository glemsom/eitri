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
