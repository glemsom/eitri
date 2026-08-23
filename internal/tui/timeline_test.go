package tui

import (
	"errors"
	"testing"
)

// timelineKinds extracts just the kinds of an event log, in log order, so
// assertions read as the arrival sequence the turn produced.
func timelineKinds(events []TimelineEvent) []EventKind {
	ks := make([]EventKind, len(events))
	for i, ev := range events {
		ks[i] = ev.Kind
	}
	return ks
}

func TestTurnDispatch_timelinePreservesArrivalOrder(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "go", "")

	// The interleaved stream from the acceptance criteria:
	// reasoning -> tool start -> tool result -> reasoning -> answer.
	d.appendStreamDelta(&tx, ReasoningStream, "r1")
	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	applyTool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\n", Lines: 1}})
	d.appendStreamDelta(&tx, ReasoningStream, "r2")
	d.appendStreamDelta(&tx, AnswerStream, "a1")

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{answer: "a1", reasoning: "r1r2"})
	if stopped || err != nil {
		t.Fatalf("handleTurnDone = stopped %v, err %v", stopped, err)
	}

	msg := tx.messages[1]
	want := []EventKind{EventReasoning, EventToolStart, EventToolResult, EventReasoning, EventAnswer}
	if got := timelineKinds(msg.events); len(got) != len(want) {
		t.Fatalf("event log kinds = %v, want %v", got, want)
	}
	for i, k := range want {
		if msg.events[i].Kind != k {
			t.Errorf("event %d kind = %v, want %v", i, msg.events[i].Kind, k)
		}
		if msg.events[i].Seq != i {
			t.Errorf("event %d seq = %d, want %d", i, msg.events[i].Seq, i)
		}
	}
}

func TestTurnDispatch_timelineSnapshotsDerivedFromLog(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "go", "")

	d.appendStreamDelta(&tx, ReasoningStream, "r1")
	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{}`}})
	applyTool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\n", Lines: 1}})
	d.appendStreamDelta(&tx, ReasoningStream, "r2")
	d.appendStreamDelta(&tx, AnswerStream, "a1")
	d.appendStreamDelta(&tx, AnswerStream, "a2")

	if _, err := d.handleTurnDone(&tx, turnDoneMsg{answer: "a1a2", reasoning: "r1r2"}); err != nil {
		t.Fatalf("handleTurnDone err = %v", err)
	}

	msg := tx.messages[1]
	content, reasoning := deriveSnapshots(msg.events)
	if content != msg.content {
		t.Errorf("content %q != derived from log %q", msg.content, content)
	}
	if reasoning != msg.reasoning {
		t.Errorf("reasoning %q != derived from log %q", msg.reasoning, reasoning)
	}
	if msg.content != "a1a2" || msg.reasoning != "r1r2" {
		t.Errorf("snapshots = content %q reasoning %q, want %q/%q", msg.content, msg.reasoning, "a1a2", "r1r2")
	}
}

func TestTurnDispatch_timelineToolBeforeFirstDelta(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "go", "")

	// Tool activity can arrive before any stream delta creates the message;
	// the event log must still record it first.
	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	applyTool(&tx, ToolUpdate{Result: &ToolResult{Name: "read", Result: "x", Lines: 1}})
	d.appendStreamDelta(&tx, AnswerStream, "hi")

	if _, err := d.handleTurnDone(&tx, turnDoneMsg{answer: "hi"}); err != nil {
		t.Fatalf("handleTurnDone err = %v", err)
	}

	msg := tx.messages[1]
	want := []EventKind{EventToolStart, EventToolResult, EventAnswer}
	if got := timelineKinds(msg.events); len(got) != len(want) {
		t.Fatalf("event log kinds = %v, want %v", got, want)
	}
	for i, k := range want {
		if msg.events[i].Kind != k {
			t.Errorf("event %d kind = %v, want %v", i, msg.events[i].Kind, k)
		}
	}
}

func TestTranscript_applyPostTurnToolAppendsToLastMessage(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "go", "")
	d.appendStreamDelta(&tx, AnswerStream, "done")
	if _, err := d.handleTurnDone(&tx, turnDoneMsg{answer: "done"}); err != nil {
		t.Fatalf("handleTurnDone err = %v", err)
	}
	if tx.busy {
		t.Fatal("turn should be over")
	}

	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"b.txt"}`}})

	msg := tx.messages[len(tx.messages)-1]
	if len(msg.events) != 2 {
		t.Fatalf("event log = %v, want the answer event plus the post-turn tool start", timelineKinds(msg.events))
	}
	if got := timelineKinds(msg.events); got[0] != EventAnswer || got[1] != EventToolStart {
		t.Errorf("event log kinds = %v, want [answer toolStart]", got)
	}
	if msg.events[1].Seq != 1 {
		t.Errorf("post-turn tool start seq = %d, want 1 (continuing the committed log)", msg.events[1].Seq)
	}
}

func TestTurnDispatch_timelineCommitsOnStoppedTurn(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "go", "")

	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{}`}})
	d.appendStreamDelta(&tx, AnswerStream, "partial")
	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{stopped: true, answer: "partial"})
	if !stopped || err != nil {
		t.Fatalf("handleTurnDone = stopped %v, err %v", stopped, err)
	}

	msg := tx.messages[1]
	if got := timelineKinds(msg.events); len(got) != 2 || got[0] != EventToolStart || got[1] != EventAnswer {
		t.Errorf("stopped turn event log = %v, want [toolStart answer]", got)
	}
}

func TestTurnDispatch_timelineCommitsOnErrorTurn(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "go", "")

	d.appendStreamDelta(&tx, ReasoningStream, "r")
	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{}`}})
	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped || err == nil {
		t.Fatalf("handleTurnDone = stopped %v, err %v", stopped, err)
	}

	msg := tx.messages[1]
	if got := timelineKinds(msg.events); len(got) != 2 || got[0] != EventReasoning || got[1] != EventToolStart {
		t.Errorf("errored turn event log = %v, want [reasoning toolStart]", got)
	}
}
