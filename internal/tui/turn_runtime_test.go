package tui

import "testing"

// TurnRuntime.OnTurnStart records the engine-reported run ID, and Accept only
// admits events matching it.
func TestTurnRuntimeRecordsRunIDAndAcceptsMatching(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil, nil)
	rt.OnTurnStart(7)
	if !rt.Accept(Event{RunID: 7}) {
		t.Fatal("expected event matching the started run ID to be accepted")
	}
}

// TurnRuntime.Accept rejects events whose run ID does not match the current run.
func TestTurnRuntimeRejectsMismatchedRunID(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil, nil)
	rt.OnTurnStart(7)
	if rt.Accept(Event{RunID: 8}) {
		t.Fatal("expected event with mismatched run ID to be rejected")
	}
}

// Direct events with RunID == 0 are always accepted, so package-local tests and
// callers can deliver events without a live engine run.
func TestTurnRuntimeAcceptsDirectZeroRunID(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil, nil)
	rt.OnTurnStart(7)
	if !rt.Accept(Event{RunID: 0}) {
		t.Fatal("expected RunID == 0 event to be accepted regardless of current run")
	}
}

// Begin drains the merged event feed before starting the turn, so events left
// over from a prior (stale) run never reach the new turn.
func TestTurnRuntimeBeginDrainsEventFeed(t *testing.T) {
	feed := NewEventFeed()
	feed.UpdateChan() <- Event{RunID: 99}
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, NewFold(s), feed)

	tx := newTestTx()
	cmd := rt.Begin(&tx, "hello", "")
	if cmd == nil {
		t.Fatal("Begin should return a command")
	}
	select {
	case <-feed.Updates():
		t.Fatal("expected the stale event to be drained before Begin starts the turn")
	default:
	}
}
