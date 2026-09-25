package tui

import "testing"

// These tests lock the "mutations invalidate" invariant for Transcript.Project,
// so callers never write the dirty flag around a projection call.

// TestTranscriptProjectStreamMarksLayoutDirty projects one streamed delta and
// asserts the layout cache comes out stale from the projection call alone.
func TestTranscriptProjectStreamMarksLayoutDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("answer", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	tx.layout.dirty = false // isolate stream projection
	projectStream(&tx, AnswerStream, "hello")
	if !tx.layout.dirty {
		t.Error("stream projection must mark the transcript layout dirty: the in-progress message grew")
	}
}

// TestTranscriptProjectToolMarksLayoutDirty projects one tool observation and
// asserts the same self-invalidation invariant on the tool-log path.
func TestTranscriptProjectToolMarksLayoutDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("answer", nil))
	tx := newTestTx()
	tx.busy = true
	beginTurn(s, &tx, "q", "")

	tx.layout.dirty = false // isolate tool projection
	projectTool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash"}})
	if !tx.layout.dirty {
		t.Error("tool projection must mark the transcript layout dirty: it changed the tool log's rendered rows")
	}
}
