package tui

import (
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// These tests lock the "mutations invalidate" invariant for the remaining
// transcript mutation paths (issue #536): resize, the Settings save flip, and
// the user-message appends for slash/skill/login activations each leave the
// layout cache stale from the Transcript method alone, so no caller — the
// Model included — ever writes the dirty flag by hand around them.

// TestSetSizeMarksLayoutDirty drives a resize through Transcript.SetSize and
// asserts the layout cache comes out stale from the method alone.
func TestSetSizeMarksLayoutDirty(t *testing.T) {
	tx := newTestTx()
	tx.layout.dirty = false // isolate SetSize: only the method may re-mark it

	tx.SetSize(100, 40)
	if !tx.layout.dirty {
		t.Error("SetSize must mark the transcript layout dirty: a width change re-wraps the rows")
	}
	if tx.width != 100 || tx.height != 40 {
		t.Errorf("SetSize stored width=%d height=%d, want 100/40", tx.width, tx.height)
	}
}

// TestApplySettingsMarksLayoutDirty drives a settings save through
// Transcript.applySettings and asserts both the applied settings and the
// self-invalidation.
func TestApplySettingsMarksLayoutDirty(t *testing.T) {
	tx := newTestTx()
	tx.layout.dirty = false // isolate applySettings: only the method may re-mark it

	cfg := config.Config{Theme: "gruvbox", CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true}
	tx.applySettings(cfg)
	if !tx.layout.dirty {
		t.Error("applySettings must mark the transcript layout dirty: the flip can re-wrap the transcript")
	}
	if tx.configTheme != "gruvbox" {
		t.Errorf("applySettings stored configTheme %q, want gruvbox", tx.configTheme)
	}
	if tx.cotExpanded || tx.toolResultsExpanded {
		t.Error("applySettings must apply the collapse-by-default flags")
	}
}

// TestAppendUserMsgMarksLayoutDirty drives a slash-activation-style user
// append through Transcript.appendUserMsg and asserts the same invariant.
func TestAppendUserMsgMarksLayoutDirty(t *testing.T) {
	tx := newTestTx()
	n := len(tx.messages)
	tx.layout.dirty = false // isolate appendUserMsg: only the method may re-mark it

	tx.appendUserMsg("/skill")
	if !tx.layout.dirty {
		t.Error("appendUserMsg must mark the transcript layout dirty: it appends a rendered message")
	}
	if len(tx.messages) != n+1 || tx.messages[n].role != "you" || tx.messages[n].content != "/skill" {
		t.Errorf("appendUserMsg appended %+v, want a 'you' message with the given content", tx.messages[n])
	}
}
