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

// Observe grows the streaming assistant message by one answer delta.
func TestTurnRuntimeObserveGrowsStreamingMessage(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, NewFold(s), nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")

	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "Hello"}})

	last := tx.messages[len(tx.messages)-1]
	if got := last.content; got != "Hello" {
		t.Fatalf("streaming message content = %q, want %q", got, "Hello")
	}
}

// Observe updates the live reasoning snapshot on the streaming message.
func TestTurnRuntimeObserveUpdatesLiveReasoning(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, NewFold(s), nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")

	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: "think"}})

	last := tx.messages[len(tx.messages)-1]
	if last.reasoning != "think" || last.content != "" {
		t.Fatalf("reasoning/content = %q/%q, want think/empty", last.reasoning, last.content)
	}
}

// Observe drops stream deltas while no turn is running, matching the
// pre-timeline stream behavior.
func TestTurnRuntimeObserveDropsStreamWhenIdle(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, NewFold(s), nil)
	tx := newTestTx()

	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "late"}})

	if len(tx.messages) != 0 {
		t.Fatalf("expected no messages from a dropped idle stream delta, got %+v", tx.messages)
	}
}

// Observe folds a tool observation into the transcript's tool log.
func TestTurnRuntimeObserveFoldsToolIntoLog(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, NewFold(s), nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")

	rt.Observe(&tx, Event{Tool: &ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}}})

	if tx.log.Len() != 1 {
		t.Fatalf("tool log entries = %d, want 1", tx.log.Len())
	}
}

// Observe arms the busy pulse on a tool start when thinking is off, so a
// thinking-off turn still shows visible progress.
func TestTurnRuntimeObserveArmsPulseOnToolStartWhenThinkingOff(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	s.SetThinkingEnabled(false)
	rt := NewTurnRuntime(s, NewFold(s), nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")
	tx.busyPulse = 0

	rt.Observe(&tx, Event{Tool: &ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}}})

	if tx.busyPulse == 0 {
		t.Fatal("expected busy pulse to be armed on tool start when thinking is off")
	}
}

// Observe does not arm the busy pulse on a tool start when thinking is on.
func TestTurnRuntimeObserveSkipsPulseOnToolStartWhenThinkingOn(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	s.SetThinkingEnabled(true)
	rt := NewTurnRuntime(s, NewFold(s), nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")
	tx.busyPulse = 0

	rt.Observe(&tx, Event{Tool: &ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}}})

	if tx.busyPulse != 0 {
		t.Fatalf("busy pulse = %d, want 0 when thinking is on", tx.busyPulse)
	}
}
