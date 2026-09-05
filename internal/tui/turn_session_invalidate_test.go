package tui

import "testing"

// These tests lock the "mutations invalidate" invariant for the TurnSession
// layout cache marked stale itself, so no caller — the Model included — ever
// writes the dirty flag by hand around a turn-life call.

// TestBeginMarksLayoutDirty drives Begin and asserts the user-message append
// left the layout cache stale with no caller-side invalidation.
func TestBeginMarksLayoutDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("answer", nil))
	tx := newTestTx()
	tx.layout.dirty = false // isolate Begin: only the verb may re-mark it

	s.Begin(&tx, "do the thing", "")
	if !tx.layout.dirty {
		t.Error("Begin must mark the transcript layout dirty: it appends the turn's user message")
	}
}

// TestCommitMarksLayoutDirtyStreaming covers the streaming completion path:
// Commit reconciles the streamed message and attaches the timeline, and the
// cache must come out stale from the Commit call alone.
func TestCommitMarksLayoutDirtyStreaming(t *testing.T) {
	s := NewTurnSession(stubTurn("final answer", nil))
	tx := newTestTx()
	cmd := s.Begin(&tx, "q", "")
	f := NewFold(s)
	f.Stream(&tx, AnswerStream, "final answer")
	msg := cmd().(turnDoneMsg)

	tx.layout.dirty = false // isolate Commit: only the verb may re-mark it
	if _, err := s.Commit(&tx, msg); err != nil {
		t.Fatalf("Commit returned err %v", err)
	}
	if !tx.layout.dirty {
		t.Error("Commit must mark the transcript layout dirty on the streaming path")
	}
}

// TestCommitMarksLayoutDirtyFreshAssistant covers the non-streaming
// completion path, where Commit appends a fresh assistant message via
// commitNewAssistant; the cache must still be invalidated inside the verb.
func TestCommitMarksLayoutDirtyFreshAssistant(t *testing.T) {
	s := NewTurnSession(stubTurn("final answer", nil))
	tx := newTestTx()
	cmd := s.Begin(&tx, "q", "")
	// No stream deltas: the turn completes without a streaming message.
	msg := cmd().(turnDoneMsg)

	tx.layout.dirty = false // isolate Commit: only the verb may re-mark it
	if _, err := s.Commit(&tx, msg); err != nil {
		t.Fatalf("Commit returned err %v", err)
	}
	if !tx.layout.dirty {
		t.Error("Commit must mark the transcript layout dirty when appending a fresh assistant message")
	}
}

// TestCommitMarksLayoutDirtyStopped covers the stopped-turn completion path:
// the partial streamed message is finalized in place and the cache must come
// out stale from the verb.
func TestCommitMarksLayoutDirtyStopped(t *testing.T) {
	s := NewTurnSession(stubTurn("partial", nil))
	tx := newTestTx()
	cmd := s.Begin(&tx, "q", "")
	f := NewFold(s)
	f.Stream(&tx, AnswerStream, "partial ")
	msg := cmd().(turnDoneMsg)
	msg.stopped = true

	tx.layout.dirty = false // isolate Commit: only the verb may re-mark it
	stopped, err := s.Commit(&tx, msg)
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want true/nil", stopped, err)
	}
	if !tx.layout.dirty {
		t.Error("Commit must mark the transcript layout dirty on the stopped path")
	}
}
