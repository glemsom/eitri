package runner

import (
	"context"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

func TestNewRunTracker(t *testing.T) {
	rt := newRunTracker()
	if rt == nil {
		t.Fatal("newRunTracker() returned nil")
	}
	if rt.count() != 0 {
		t.Fatalf("expected empty tracker, got count = %d", rt.count())
	}
}

func TestRunTracker_StoreAndGet(t *testing.T) {
	rt := newRunTracker()

	state := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	rt.store("sess-1", state)

	got := rt.get("sess-1")
	if got == nil {
		t.Fatal("get returned nil after store")
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("session ID = %q, want %q", got.SessionID, "sess-1")
	}

	// get returns nil for unknown session
	if rt.get("nonexistent") != nil {
		t.Fatal("get should return nil for unknown session")
	}
}

func TestRunTracker_GetActive(t *testing.T) {
	rt := newRunTracker()

	// nil when session not found
	if rt.getActive("nonexistent") != nil {
		t.Fatal("getActive should return nil for unknown session")
	}

	done := make(chan struct{})
	state := &RunState{
		SessionID: "sess-1",
		Done:      done,
		SSE:       runstate.New(),
	}
	rt.store("sess-1", state)

	// active when done not closed
	active := rt.getActive("sess-1")
	if active == nil {
		t.Fatal("getActive returned nil for active run")
	}

	// nil when done channel is closed
	close(done)
	if rt.getActive("sess-1") != nil {
		t.Fatal("getActive should return nil when done channel is closed")
	}
}

func TestRunTracker_Remove(t *testing.T) {
	rt := newRunTracker()

	state1 := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	state2 := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	rt.store("sess-1", state1)

	// remove with wrong pointer — no-op
	rt.remove("sess-1", state2)
	if rt.get("sess-1") == nil {
		t.Fatal("remove with wrong pointer should not delete")
	}

	// remove with matching pointer
	rt.remove("sess-1", state1)
	if rt.get("sess-1") != nil {
		t.Fatal("remove with matching pointer should delete")
	}
}

func TestRunTracker_RemoveRun(t *testing.T) {
	rt := newRunTracker()

	// returns nil for unknown session
	if rt.removeRun("nonexistent") != nil {
		t.Fatal("removeRun should return nil for unknown session")
	}

	state := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	rt.store("sess-1", state)

	got := rt.removeRun("sess-1")
	if got == nil {
		t.Fatal("removeRun returned nil")
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("session ID = %q, want %q", got.SessionID, "sess-1")
	}
	if rt.get("sess-1") != nil {
		t.Fatal("session should be deleted after removeRun")
	}
}

func TestRunTracker_ExchangeIfDone(t *testing.T) {
	rt := newRunTracker()

	// false for unknown session
	if rt.exchangeIfDone("nonexistent") {
		t.Fatal("exchangeIfDone should return false for unknown session")
	}

	done := make(chan struct{})
	state := &RunState{SessionID: "sess-1", Done: done}
	rt.store("sess-1", state)

	// false when not done
	if rt.exchangeIfDone("sess-1") {
		t.Fatal("exchangeIfDone should return false when not done")
	}
	if rt.get("sess-1") == nil {
		t.Fatal("session should still exist after exchangeIfDone(false)")
	}

	// true and removes when done channel is closed
	close(done)
	if !rt.exchangeIfDone("sess-1") {
		t.Fatal("exchangeIfDone should return true when done")
	}
	if rt.get("sess-1") != nil {
		t.Fatal("session should be deleted after exchangeIfDone(true)")
	}
}

func TestRunTracker_Count(t *testing.T) {
	rt := newRunTracker()

	if rt.count() != 0 {
		t.Fatalf("expected 0, got %d", rt.count())
	}

	rt.store("sess-1", &RunState{SessionID: "sess-1", Done: make(chan struct{})})
	if rt.count() != 1 {
		t.Fatalf("expected 1, got %d", rt.count())
	}

	rt.store("sess-2", &RunState{SessionID: "sess-2", Done: make(chan struct{})})
	if rt.count() != 2 {
		t.Fatalf("expected 2, got %d", rt.count())
	}
}

func TestRunTracker_AllActiveStates(t *testing.T) {
	rt := newRunTracker()

	// empty when no runs
	active := rt.allActiveStates()
	if len(active) != 0 {
		t.Fatalf("expected 0 active, got %d", len(active))
	}

	done1 := make(chan struct{})
	close(done1)
	rt.store("sess-done", &RunState{SessionID: "sess-done", Done: done1})

	active2 := make(chan struct{})
	rt.store("sess-active", &RunState{SessionID: "sess-active", Done: active2})

	active = rt.allActiveStates()
	if len(active) != 1 {
		t.Fatalf("expected 1 active state, got %d", len(active))
	}
	if active[0].SessionID != "sess-active" {
		t.Fatalf("active session = %q, want %q", active[0].SessionID, "sess-active")
	}
}

func TestRunTracker_SSECounters(t *testing.T) {
	rt := newRunTracker()

	// empty when no runs
	counters := rt.sseCounters()
	if len(counters) != 0 {
		t.Fatalf("expected 0 counters, got %d", len(counters))
	}

	sse := runstate.New()
	done := make(chan struct{})
	rt.store("sess-1", &RunState{SessionID: "sess-1", Done: done, SSE: sse})

	counters = rt.sseCounters()
	if len(counters) != 1 {
		t.Fatalf("expected 1 counter, got %d", len(counters))
	}
	if _, ok := counters["sess-1"]; !ok {
		t.Fatal("expected counter for sess-1")
	}
}

func TestRunTracker_SSESnapshot(t *testing.T) {
	rt := newRunTracker()

	// nil for unknown session
	if rt.sseSnapshot("nonexistent") != nil {
		t.Fatal("sseSnapshot should return nil for unknown session")
	}

	// nil for done session
	done := make(chan struct{})
	close(done)
	rt.store("sess-done", &RunState{SessionID: "sess-done", Done: done, SSE: runstate.New()})
	if rt.sseSnapshot("sess-done") != nil {
		t.Fatal("sseSnapshot should return nil for done session")
	}

	// returns snapshot for active session
	activeDone := make(chan struct{})
	activeSSE := runstate.New()
	rt.store("sess-active", &RunState{SessionID: "sess-active", Done: activeDone, SSE: activeSSE, Turns: 3})

	snap := rt.sseSnapshot("sess-active")
	if snap == nil {
		t.Fatal("sseSnapshot should return snapshot for active session")
	}
	if snap.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", snap.Turns)
	}
}

func TestRunTracker_NotifyAllClosed(t *testing.T) {
	rt := newRunTracker()

	done := make(chan struct{})
	sse := runstate.New()
	rt.store("sess-1", &RunState{SessionID: "sess-1", Done: done, SSE: sse})

	// Subscribe to receive the closed event
	id, ch, _ := sse.Subscribe()
	defer sse.Unsubscribe(id)

	rt.notifyAllClosed("all done")

	select {
	case evt := <-ch:
		if evt.Type != "closed" {
			t.Fatalf("event type = %q, want %q", evt.Type, "closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed event")
	}
}

func TestRunTracker_BroadcastStatusUpdate(t *testing.T) {
	rt := newRunTracker()
	bb := newBrowserBroadcaster()

	uiMgr := uisession.NewManager(10, t.TempDir())
	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Subscribe to browser-level events
	id, ch := bb.Subscribe("browser-1")
	defer bb.Unsubscribe("browser-1", id)

	rt.broadcastStatusUpdate(sess.ID, uisession.StatusRunning, uiMgr, bb)

	select {
	case evt := <-ch:
		if evt.Type != "session_status" {
			t.Fatalf("event type = %q, want %q", evt.Type, "session_status")
		}
		data, ok := evt.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data type = %T, want map[string]any", evt.Data)
		}
		if data["session_id"] != sess.ID {
			t.Errorf("session_id = %v, want %s", data["session_id"], sess.ID)
		}
		if data["status"] != "running" {
			t.Errorf("status = %v, want running", data["status"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for status event")
	}

	// nil uiSessionMgr = noop
	rt.broadcastStatusUpdate("sess-2", uisession.StatusRunning, nil, bb)
}

func TestRunTracker_BatchCtx(t *testing.T) {
	rt := newRunTracker()

	// getBatchCtx returns nil initially
	if rt.getBatchCtx() != nil {
		t.Fatal("getBatchCtx should return nil initially")
	}

	ctx := &debugConversationContext{
		LastUserMessage:      "hello",
		LastAssistantMessage: "hi there",
		TurnNumber:           5,
	}
	rt.setBatchCtx(ctx)

	got := rt.getBatchCtx()
	if got == nil {
		t.Fatal("getBatchCtx returned nil after set")
	}
	if got.LastUserMessage != "hello" {
		t.Errorf("LastUserMessage = %q, want %q", got.LastUserMessage, "hello")
	}
	if got.LastAssistantMessage != "hi there" {
		t.Errorf("LastAssistantMessage = %q, want %q", got.LastAssistantMessage, "hi there")
	}
	if got.TurnNumber != 5 {
		t.Errorf("TurnNumber = %d, want 5", got.TurnNumber)
	}
}

func TestRunTracker_RemoveAll(t *testing.T) {
	rt := newRunTracker()

	rt.store("sess-1", &RunState{SessionID: "sess-1", Done: make(chan struct{})})
	rt.store("sess-2", &RunState{SessionID: "sess-2", Done: make(chan struct{})})

	states := rt.removeAll()
	if len(states) != 2 {
		t.Fatalf("removeAll returned %d states, want 2", len(states))
	}
	if rt.count() != 0 {
		t.Fatalf("expected 0 after removeAll, got %d", rt.count())
	}
}

func TestRunTracker_GetPanicFree(t *testing.T) {
	// Ensure no race conditions under concurrent access
	rt := newRunTracker()
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				rt.store("sess-1", &RunState{SessionID: "sess-1", Done: done})
				rt.get("sess-1")
				rt.getActive("sess-1")
				rt.removeRun("sess-1")
				rt.count()
			}
		}
	}()

	time.Sleep(10 * time.Millisecond)
}
