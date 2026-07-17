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
