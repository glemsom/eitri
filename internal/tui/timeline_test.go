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

func TestTimeline_PreservesArrivalOrder(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))

	tx := newTestTx()
	beginTurn(s, &tx, "go", "")

	// The interleaved stream from the acceptance criteria:
	// reasoning -> tool start -> tool result -> reasoning -> answer.
	projectStream(&tx, ReasoningStream, "r1")
	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	projectTool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\n", Lines: 1}})
	projectStream(&tx, ReasoningStream, "r2")
	projectStream(&tx, AnswerStream, "a1")

	stopped, err := projectDone(&tx, turnDoneMsg{answer: "a1", reasoning: "r1r2"})
	if stopped || err != nil {
		t.Fatalf("completion = stopped %v, err %v", stopped, err)
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

func TestTimeline_SnapshotsDerivedFromLog(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))

	tx := newTestTx()
	beginTurn(s, &tx, "go", "")

	projectStream(&tx, ReasoningStream, "r1")
	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{}`}})
	projectTool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\n", Lines: 1}})
	projectStream(&tx, ReasoningStream, "r2")
	projectStream(&tx, AnswerStream, "a1")
	projectStream(&tx, AnswerStream, "a2")

	if _, err := projectDone(&tx, turnDoneMsg{answer: "a1a2", reasoning: "r1r2"}); err != nil {
		t.Fatalf("completion err = %v", err)
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

func TestTimeline_ToolBeforeFirstDelta(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))

	tx := newTestTx()
	beginTurn(s, &tx, "go", "")

	// Tool activity can arrive before any stream delta creates the message;
	// the event log must still record it first.
	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	projectTool(&tx, ToolUpdate{Result: &ToolResult{Name: "read", Result: "x", Lines: 1}})
	projectStream(&tx, AnswerStream, "hi")

	if _, err := projectDone(&tx, turnDoneMsg{answer: "hi"}); err != nil {
		t.Fatalf("completion err = %v", err)
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

func TestTimeline_CommitsOnStoppedTurn(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))

	tx := newTestTx()
	beginTurn(s, &tx, "go", "")

	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{}`}})
	projectStream(&tx, AnswerStream, "partial")
	stopped, err := projectDone(&tx, turnDoneMsg{stopped: true, answer: "partial"})
	if !stopped || err != nil {
		t.Fatalf("completion = stopped %v, err %v", stopped, err)
	}

	msg := tx.messages[1]
	if got := timelineKinds(msg.events); len(got) != 2 || got[0] != EventToolStart || got[1] != EventAnswer {
		t.Errorf("stopped turn event log = %v, want [toolStart answer]", got)
	}
}

func TestTimeline_CommitsOnErrorTurn(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))

	tx := newTestTx()
	beginTurn(s, &tx, "go", "")

	projectStream(&tx, ReasoningStream, "r")
	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{}`}})
	stopped, err := projectDone(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped || err == nil {
		t.Fatalf("completion = stopped %v, err %v", stopped, err)
	}

	msg := tx.messages[1]
	if got := timelineKinds(msg.events); len(got) != 2 || got[0] != EventReasoning || got[1] != EventToolStart {
		t.Errorf("errored turn event log = %v, want [reasoning toolStart]", got)
	}
}
