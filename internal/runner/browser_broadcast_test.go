package runner

import (
	"sync"
	"testing"
	"time"
)

func TestNewBrowserBroadcaster(t *testing.T) {
	bb := newBrowserBroadcaster()
	if bb == nil {
		t.Fatal("newBrowserBroadcaster() returned nil")
	}
	if bb.Count("browser-1") != 0 {
		t.Fatalf("expected 0 subscribers, got %d", bb.Count("browser-1"))
	}
}

func TestBrowserBroadcaster_SubscribeAndUnsubscribe(t *testing.T) {
	bb := newBrowserBroadcaster()

	// Subscribe returns a unique ID and a channel
	id1, ch1 := bb.Subscribe("browser-1")
	id2, _ := bb.Subscribe("browser-1")

	if id1 == id2 {
		t.Fatal("expected unique subscriber IDs")
	}
	if ch1 == nil {
		t.Fatal("Subscribe returned nil channel")
	}

	if bb.Count("browser-1") != 2 {
		t.Fatalf("expected 2 subscribers, got %d", bb.Count("browser-1"))
	}

	// Unsubscribe removes the subscriber
	bb.Unsubscribe("browser-1", id1)
	if bb.Count("browser-1") != 1 {
		t.Fatalf("expected 1 subscriber after unsubscribe, got %d", bb.Count("browser-1"))
	}

	// Unsubscribe closes the channel
	select {
	case _, ok := <-ch1:
		if ok {
			t.Fatal("channel should be closed after unsubscribe")
		}
	default:
		t.Fatal("channel should be closed, but read would block")
	}
}

func TestBrowserBroadcaster_Unsubscribe_UnknownBrowserID(t *testing.T) {
	bb := newBrowserBroadcaster()
	// Should not panic
	bb.Unsubscribe("nonexistent", 42)
}

func TestBrowserBroadcaster_Unsubscribe_UnknownSubscriberID(t *testing.T) {
	bb := newBrowserBroadcaster()
	id, _ := bb.Subscribe("browser-1")
	// Unsubscribe with wrong ID should not affect the real subscriber
	bb.Unsubscribe("browser-1", 999)

	if bb.Count("browser-1") != 1 {
		t.Fatalf("expected 1 subscriber, got %d", bb.Count("browser-1"))
	}
	// Clean up
	bb.Unsubscribe("browser-1", id)
}

func TestBrowserBroadcaster_Broadcast_NoSubscribers(t *testing.T) {
	bb := newBrowserBroadcaster()
	// Should not panic or block
	bb.Broadcast("browser-1", BrowserEvent{Type: "test", Data: "data"})
}

func TestBrowserBroadcaster_Broadcast_SingleSubscriber(t *testing.T) {
	bb := newBrowserBroadcaster()
	id, ch := bb.Subscribe("browser-1")
	defer bb.Unsubscribe("browser-1", id)

	bb.Broadcast("browser-1", BrowserEvent{Type: "greeting", Data: "hello"})

	select {
	case evt := <-ch:
		if evt.Type != "greeting" {
			t.Fatalf("event type = %q, want %q", evt.Type, "greeting")
		}
		if evt.Data != "hello" {
			t.Fatalf("event data = %v, want %q", evt.Data, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for broadcast event")
	}
}

func TestBrowserBroadcaster_Broadcast_MultipleSubscribers(t *testing.T) {
	bb := newBrowserBroadcaster()

	id1, ch1 := bb.Subscribe("browser-1")
	defer bb.Unsubscribe("browser-1", id1)

	id2, ch2 := bb.Subscribe("browser-1")
	defer bb.Unsubscribe("browser-1", id2)

	bb.Broadcast("browser-1", BrowserEvent{Type: "notify", Data: 42})

	// Both subscribers should receive the event
	for i, ch := range []<-chan BrowserEvent{ch1, ch2} {
		select {
		case evt := <-ch:
			if evt.Type != "notify" {
				t.Errorf("subscriber %d: event type = %q, want %q", i, evt.Type, "notify")
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timed out", i)
		}
	}
}

func TestBrowserBroadcaster_Broadcast_DifferentBrowserIDs(t *testing.T) {
	bb := newBrowserBroadcaster()

	id1, ch1 := bb.Subscribe("browser-a")
	defer bb.Unsubscribe("browser-a", id1)

	id2, ch2 := bb.Subscribe("browser-b")
	defer bb.Unsubscribe("browser-b", id2)

	// Broadcast to browser-a only
	bb.Broadcast("browser-a", BrowserEvent{Type: "a-only"})

	select {
	case evt := <-ch1:
		if evt.Type != "a-only" {
			t.Fatalf("browser-a event type = %q, want %q", evt.Type, "a-only")
		}
	case <-time.After(time.Second):
		t.Fatal("browser-a subscriber timed out")
	}

	// browser-b should not receive the event
	select {
	case <-ch2:
		t.Fatal("browser-b should not receive browser-a event")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestBrowserBroadcaster_Count(t *testing.T) {
	bb := newBrowserBroadcaster()

	if bb.Count("browser-1") != 0 {
		t.Fatalf("expected 0, got %d", bb.Count("browser-1"))
	}

	id1, _ := bb.Subscribe("browser-1")
	if bb.Count("browser-1") != 1 {
		t.Fatalf("expected 1, got %d", bb.Count("browser-1"))
	}

	id2, _ := bb.Subscribe("browser-1")
	if bb.Count("browser-1") != 2 {
		t.Fatalf("expected 2, got %d", bb.Count("browser-1"))
	}

	bb.Unsubscribe("browser-1", id1)
	if bb.Count("browser-1") != 1 {
		t.Fatalf("expected 1 after unsubscribe, got %d", bb.Count("browser-1"))
	}

	bb.Unsubscribe("browser-1", id2)
	if bb.Count("browser-1") != 0 {
		t.Fatalf("expected 0 after all unsubscribes, got %d", bb.Count("browser-1"))
	}
}

func TestBrowserBroadcaster_Broadcast_SlowSubscriberDrops(t *testing.T) {
	bb := newBrowserBroadcaster()

	id, ch := bb.Subscribe("browser-1")
	defer bb.Unsubscribe("browser-1", id)

	// Fill the channel buffer (capacity 64)
	// Broadcast should not block even though the subscriber isn't reading
	for i := 0; i < 128; i++ {
		bb.Broadcast("browser-1", BrowserEvent{Type: "spam"})
	}

	// Drain what we can
	received := 0
	done := false
	for !done {
		select {
		case <-ch:
			received++
		case <-time.After(50 * time.Millisecond):
			done = true
		}
	}

	if received < 64 {
		t.Fatalf("expected at least 64 events in buffer, got %d", received)
	}
}

func TestBrowserBroadcaster_ConcurrentAccess(t *testing.T) {
	bb := newBrowserBroadcaster()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, ch := bb.Subscribe("browser-1")
			bb.Broadcast("browser-1", BrowserEvent{Type: "ping"})
			<-ch
			bb.Unsubscribe("browser-1", id)
		}()
	}
	wg.Wait()

	if bb.Count("browser-1") != 0 {
		t.Fatalf("expected 0 after all goroutines, got %d", bb.Count("browser-1"))
	}
}
