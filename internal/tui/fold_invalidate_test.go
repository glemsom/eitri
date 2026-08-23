package tui

import "testing"

// These tests lock the "mutations invalidate" invariant for the Fold
// (issue #535): Stream and Tool leave the transcript layout cache stale by
// themselves, so no caller — the Model's stream-delta or tool-update handler
// included — ever writes the dirty flag around a Fold call.

// TestFoldStreamMarksLayoutDirty drives one streamed delta through Fold.Stream
// and asserts the layout cache comes out stale from the Fold call alone.
func TestFoldStreamMarksLayoutDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("answer", nil))
	tx := newTestTx()
	s.Begin(&tx, "q", "")
	f := NewFold(s)

	tx.layout.dirty = false // isolate Stream: only the Fold may re-mark it
	f.Stream(&tx, AnswerStream, "hello")
	if !tx.layout.dirty {
		t.Error("Fold.Stream must mark the transcript layout dirty: the in-progress message grew")
	}
}

// TestFoldToolMarksLayoutDirty drives one tool observation through Fold.Tool
// and asserts the same self-invalidation invariant on the tool-log path.
func TestFoldToolMarksLayoutDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("answer", nil))
	tx := newTestTx()
	tx.busy = true
	s.Begin(&tx, "q", "")
	f := NewFold(s)

	tx.layout.dirty = false // isolate Tool: only the Fold may re-mark it
	f.Tool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash"}})
	if !tx.layout.dirty {
		t.Error("Fold.Tool must mark the transcript layout dirty: it changed the tool log's rendered rows")
	}
}
