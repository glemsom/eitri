package tui

import (
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// These tests lock the "mutations invalidate" invariant: the layout-mutating
// Transcript methods — resize, settings, and the user-message appends for
// slash/skill/login activations — each leave the layout cache stale from the
// method alone, so no caller, the Model included, writes the dirty flag by hand.

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

func TestApplySettingsMarksLayoutDirty(t *testing.T) {
	tx := newTestTx()
	tx.layout.dirty = false // isolate ApplySettings: only the method may re-mark it

	cfg := config.Config{Theme: "gruvbox", CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true}
	tx.ApplySettings(cfg)
	if !tx.layout.dirty {
		t.Error("ApplySettings must mark the transcript layout dirty: the flip can re-wrap the transcript")
	}
	if tx.ConfigTheme() != "gruvbox" {
		t.Errorf("ApplySettings stored configTheme %q, want gruvbox", tx.ConfigTheme())
	}
	if tx.cotExpanded || tx.toolResultsExpanded {
		t.Error("ApplySettings must apply the collapse-by-default flags")
	}
}

func TestAppendUserMsgMarksLayoutDirty(t *testing.T) {
	tx := newTestTx()
	n := len(tx.messages)
	tx.layout.dirty = false // isolate AppendUserMsg: only the method may re-mark it

	tx.AppendUserMsg("/skill")
	if !tx.layout.dirty {
		t.Error("AppendUserMsg must mark the transcript layout dirty: it appends a rendered message")
	}
	if len(tx.messages) != n+1 || tx.messages[n].role != "you" || tx.messages[n].content != "/skill" {
		t.Errorf("AppendUserMsg appended %+v, want a 'you' message with the given content", tx.messages[n])
	}
}
