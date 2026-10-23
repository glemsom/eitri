package tui

import "testing"

// These tests lock the "mutations invalidate" invariant for Transcript.Project:
// it marks the layout cache stale itself, so callers never do it by hand.

func TestStartProjectionMarksLayoutDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("answer", nil))
	tx := newTestTx()
	tx.layout.dirty = false // isolate start projection

	beginTurn(s, &tx, "do the thing", "")
	if !tx.layout.dirty {
		t.Error("start projection must mark the transcript layout dirty: it appends the user message")
	}
}

func TestCompletionProjectionMarksLayoutDirtyStreaming(t *testing.T) {
	s := NewTurnSession(stubTurn("final answer", nil))
	tx := newTestTx()
	cmd := beginTurn(s, &tx, "q", "")
	projectStream(&tx, AnswerStream, "final answer")
	msg := cmd().(turnDoneMsg)

	tx.layout.dirty = false // isolate completion projection
	if _, err := projectDone(&tx, msg); err != nil {
		t.Fatalf("completion projection returned err %v", err)
	}
	if !tx.layout.dirty {
		t.Error("completion projection must mark the transcript layout dirty on the streaming path")
	}
}

func TestCompletionProjectionMarksLayoutDirtyFreshAssistant(t *testing.T) {
	s := NewTurnSession(stubTurn("final answer", nil))
	tx := newTestTx()
	cmd := beginTurn(s, &tx, "q", "")
	// No stream deltas: the turn completes without a streaming message.
	msg := cmd().(turnDoneMsg)

	tx.layout.dirty = false // isolate completion projection
	if _, err := projectDone(&tx, msg); err != nil {
		t.Fatalf("completion projection returned err %v", err)
	}
	if !tx.layout.dirty {
		t.Error("completion projection must mark the transcript layout dirty when appending a fresh assistant message")
	}
}

func TestCompletionProjectionMarksLayoutDirtyStopped(t *testing.T) {
	s := NewTurnSession(stubTurn("partial", nil))
	tx := newTestTx()
	cmd := beginTurn(s, &tx, "q", "")
	projectStream(&tx, AnswerStream, "partial ")
	msg := cmd().(turnDoneMsg)
	msg.stopped = true

	tx.layout.dirty = false // isolate completion projection
	stopped, err := projectDone(&tx, msg)
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want true/nil", stopped, err)
	}
	if !tx.layout.dirty {
		t.Error("completion projection must mark the transcript layout dirty on the stopped path")
	}
}
