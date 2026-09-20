package tui

import (
	"context"
	"errors"
	"testing"
)

// TurnRuntime.OnTurnStart records the engine-reported run ID, and Accept only
// admits events matching it.
func TestTurnRuntimeRecordsRunIDAndAcceptsMatching(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil)
	rt.OnTurnStart(7)
	if !rt.Accept(Event{RunID: 7}) {
		t.Fatal("expected event matching the started run ID to be accepted")
	}
}

// TurnRuntime.Accept rejects events whose run ID does not match the current run.
func TestTurnRuntimeIgnoresZeroTurnStartRunID(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil)
	rt.OnTurnStart(7)
	rt.OnTurnStart(0)
	if !rt.Accept(Event{RunID: 7}) {
		t.Fatal("expected the active nonzero run ID to remain accepted")
	}
	if rt.Accept(Event{RunID: 8}) {
		t.Fatal("expected a different nonzero run ID to remain rejected")
	}
}

func TestTurnRuntimeRejectsMismatchedRunID(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil)
	rt.OnTurnStart(7)
	if rt.Accept(Event{RunID: 8}) {
		t.Fatal("expected event with mismatched run ID to be rejected")
	}
}

// Direct events with RunID == 0 are always accepted, so package-local tests and
// callers can deliver events without a live engine run.
func TestTurnRuntimeAcceptsDirectZeroRunID(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil)
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
	rt := NewTurnRuntime(s, feed)

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
	rt := NewTurnRuntime(s, nil)
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
	rt := NewTurnRuntime(s, nil)
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
	rt := NewTurnRuntime(s, nil)
	tx := newTestTx()

	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "late"}})

	if len(tx.messages) != 0 {
		t.Fatalf("expected no messages from a dropped idle stream delta, got %+v", tx.messages)
	}
}

// Observe preserves stream/tool arrival order and exposes the resulting timeline
// through TurnRuntime rather than requiring callers to coordinate Fold.
func TestTurnRuntimeObservePreservesMixedTimelineOrder(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")

	rt.Observe(&tx, Event{Tool: &ToolUpdate{Start: &ToolStart{Name: "read"}}})
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "done"}})
	rt.Observe(&tx, Event{Tool: &ToolUpdate{Result: &ToolResult{Name: "read", Result: "ok", Lines: 1}}})

	events := rt.LiveTimeline()
	if len(events) != 3 {
		t.Fatalf("timeline events = %d, want 3", len(events))
	}
	if events[0].Kind != EventToolStart || events[1].Kind != EventAnswer || events[2].Kind != EventToolResult {
		t.Fatalf("timeline kinds = %v, want tool start, answer, tool result", []EventKind{events[0].Kind, events[1].Kind, events[2].Kind})
	}
	if got := tx.log.Len(); got != 1 {
		t.Fatalf("tool log entries = %d, want 1", got)
	}
}

// Observe folds a tool observation into the transcript's tool log.
func TestTurnRuntimeObserveFoldsToolIntoLog(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	rt := NewTurnRuntime(s, nil)
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
	rt := NewTurnRuntime(s, nil)
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
	rt := NewTurnRuntime(s, nil)
	tx := newTestTx()
	rt.Begin(&tx, "hi", "")
	tx.busyPulse = 0

	rt.Observe(&tx, Event{Tool: &ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}}})

	if tx.busyPulse != 0 {
		t.Fatalf("busy pulse = %d, want 0 when thinking is on", tx.busyPulse)
	}
}

// newTestRuntime builds a TurnRuntime with its fold bound to its own session,
// the shape callers always use outside of tests.
func newTestRuntime(answer string, err error) *TurnRuntime {
	s := NewTurnSession(stubTurn(answer, err))
	return NewTurnRuntime(s, nil)
}

// Commit reconciles a streamed success into the streaming assistant message
// and clears busy/live-timeline state, driven entirely through TurnRuntime.
func TestTurnRuntimeCommitSuccessStreaming(t *testing.T) {
	rt := newTestRuntime("", nil)
	tx := newTestTx()
	rt.Begin(&tx, "q", "")
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "partial"}})

	stopped, err := rt.Commit(&tx, turnDoneMsg{answer: "final answer", reasoning: "reasoned"})
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}
	msg := tx.messages[len(tx.messages)-1]
	if msg.content != "final answer" || msg.reasoning != "reasoned" || msg.streaming {
		t.Fatalf("message = %+v", msg)
	}
	if tx.busy || rt.LiveTimeline() != nil {
		t.Error("Commit did not clear busy/live-timeline state")
	}
}

// Commit synthesizes the event log for a non-streamed success, so the
// committed message still carries an arrival-ordered record.
func TestTurnRuntimeCommitSuccessNoStreaming(t *testing.T) {
	rt := newTestRuntime("", nil)
	tx := newTestTx()
	rt.Begin(&tx, "q", "")

	stopped, err := rt.Commit(&tx, turnDoneMsg{answer: "the answer"})
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}
	msg := tx.messages[len(tx.messages)-1]
	if msg.content != "the answer" || len(msg.events) == 0 {
		t.Fatalf("message = %+v, want a synthesized event log", msg)
	}
}

// Commit marks a streamed turn stopped and keeps its live partial content.
func TestTurnRuntimeCommitStoppedStreaming(t *testing.T) {
	rt := newTestRuntime("", nil)
	tx := newTestTx()
	rt.Begin(&tx, "q", "")
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "partial"}})

	stopped, err := rt.Commit(&tx, turnDoneMsg{stopped: true})
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want true/nil", stopped, err)
	}
	msg := tx.messages[len(tx.messages)-1]
	if msg.content != "partial" || !msg.stopped || msg.streaming {
		t.Fatalf("message = %+v, want stopped live partial", msg)
	}
}

// Commit appends a failure message and reports the error for a failed turn.
func TestTurnRuntimeCommitError(t *testing.T) {
	rt := newTestRuntime("", nil)
	tx := newTestTx()
	rt.Begin(&tx, "q", "")

	stopped, err := rt.Commit(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped || err == nil {
		t.Fatalf("stopped=%v err=%v, want false/non-nil", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].role != "eitri" {
		t.Fatalf("messages = %+v", tx.messages)
	}
}

func TestTurnRuntimePostCompletionToolObservation(t *testing.T) {
	rt := newTestRuntime("answer", nil)
	tx := newTestTx()
	cmd := rt.Begin(&tx, "q", "")
	if _, err := rt.Commit(&tx, cmd().(turnDoneMsg)); err != nil {
		t.Fatal(err)
	}
	rt.Observe(&tx, Event{Tool: &ToolUpdate{Result: &ToolResult{Name: "read", Result: "late"}}})
	events := tx.messages[len(tx.messages)-1].events
	if len(events) != 2 || events[1].Kind != EventToolResult {
		t.Fatalf("events = %+v", events)
	}
}

func TestTurnRuntimeLaterRunIsolation(t *testing.T) {
	rt := newTestRuntime("answer", nil)
	tx := newTestTx()
	first := rt.Begin(&tx, "one", "")
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "old"}})
	if _, err := rt.Commit(&tx, first().(turnDoneMsg)); err != nil {
		t.Fatal(err)
	}
	second := rt.Begin(&tx, "two", "")
	if rt.LiveTimeline() != nil {
		t.Fatal("later run inherited timeline")
	}
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "new"}})
	if _, err := rt.Commit(&tx, second().(turnDoneMsg)); err != nil {
		t.Fatal(err)
	}
	if got := tx.messages[len(tx.messages)-1].content; got != "answer" {
		t.Fatalf("content = %q", got)
	}
	if len(tx.messages[len(tx.messages)-1].events) != 1 {
		t.Fatal("later run inherited events")
	}
}

func TestTurnRuntimeCommitErrorPreservesPartialOutput(t *testing.T) {
	rt := newTestRuntime("", nil)
	tx := newTestTx()
	rt.Begin(&tx, "q", "")
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "partial"}})
	stopped, err := rt.Commit(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped || err == nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if tx.messages[1].content != "partial" || tx.messages[1].streaming {
		t.Fatalf("partial = %+v", tx.messages[1])
	}
	if len(tx.messages) != 3 || tx.messages[2].content == "" {
		t.Fatalf("messages = %+v", tx.messages)
	}
}

// Stop cancels the in-flight turn started through Begin, so the turn's
// cancelable context observes cancellation.
func TestTurnRuntimeStopCancelsBegunTurn(t *testing.T) {
	cancelSeen := func(ctx context.Context, _ string, _ string) (TurnResult, error) {
		<-ctx.Done()
		return TurnResult{}, context.Canceled
	}
	rt := NewTurnRuntime(NewTurnSession(cancelSeen), nil)
	tx := newTestTx()
	cmd := rt.Begin(&tx, "hi", "")

	rt.Stop()

	msg := cmd().(turnDoneMsg)
	if !msg.stopped {
		t.Fatalf("turnDoneMsg = %+v, want stopped", msg)
	}
}

// SetThinkingEnabled/ThinkingEnabled round-trip through the runtime, which is
// the shape turn-created messages read.
func TestTurnRuntimeThinkingEnabledRoundTrips(t *testing.T) {
	rt := NewTurnRuntime(NewTurnSession(stubTurn("ok", nil)), nil)
	rt.SetThinkingEnabled(true)
	if !rt.ThinkingEnabled() {
		t.Fatal("expected thinking enabled to round-trip true")
	}
	rt.SetThinkingEnabled(false)
	if rt.ThinkingEnabled() {
		t.Fatal("expected thinking enabled to round-trip false")
	}
}

// Begin, Observe, and Commit together mark the transcript layout dirty on
// every mutation, so no caller invalidates the cache by hand around a turn.
func TestTurnRuntimeMarksLayoutDirtyThroughFullTurn(t *testing.T) {
	rt := newTestRuntime("final answer", nil)
	tx := newTestTx()
	tx.layout.dirty = false
	cmd := rt.Begin(&tx, "do the thing", "")
	if !tx.layout.dirty {
		t.Fatal("Begin must mark the transcript layout dirty")
	}

	tx.layout.dirty = false
	rt.Observe(&tx, Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "final answer"}})
	if !tx.layout.dirty {
		t.Fatal("Observe must mark the transcript layout dirty")
	}

	tx.layout.dirty = false
	msg := cmd().(turnDoneMsg)
	if _, err := rt.Commit(&tx, msg); err != nil {
		t.Fatalf("Commit returned err %v", err)
	}
	if !tx.layout.dirty {
		t.Fatal("Commit must mark the transcript layout dirty")
	}
}
