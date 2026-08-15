package tui

import (
	"strings"
	"testing"
	"time"
)

// toolEntryFor returns a minimal real toolEntry with the identifying fields
// set, so a test can assert on what the log stores without constructing
// zero-value noise.
func toolEntryFor(name, args string) toolEntry {
	return toolEntry{name: name, args: args}
}

// TestToolLog_ApplyPairsStartWithResult asserts a Start then a matching name
// Result fold into one complete entry with the result field filled (issue #84).
func TestToolLog_ApplyPairsStartWithResult(t *testing.T) {
	var l toolLog
	l.SetAnchor(3)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "contents", Lines: 1}})

	if l.Len() != 1 {
		t.Fatalf("expected one entry, got %d", l.Len())
	}
	e := l.Entry(0)
	if !e.complete {
		t.Errorf("entry should be complete after result")
	}
	if e.name != "read" || e.result != "contents" || e.anchor != 3 {
		t.Errorf("entry = %+v, want read/contents/anchor 3", e)
	}
}

// TestToolLog_ApplyPairsMostRecentIncompleteSameName asserts a Result pairs back
// to the most recent not-yet-complete entry for that tool name, so a stray or
// out-of-order result cannot corrupt an already-complete entry (issue #208 US3).
func TestToolLog_ApplyPairsMostRecentIncompleteSameName(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"a"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a out", Lines: 1}})
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"b"}`}})
	// The second result must pair to the still-incomplete bash entry.
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "b out", Lines: 1}})

	if l.Len() != 2 {
		t.Fatalf("expected two entries, got %d", l.Len())
	}
	if l.Entry(0).complete != true || l.Entry(0).result != "a out" {
		t.Errorf("first entry = %+v, want complete with 'a out'", l.Entry(0))
	}
	if l.Entry(1).result != "b out" {
		t.Errorf("second entry = %+v, want 'b out'", l.Entry(1))
	}
}

// TestToolLog_ToggleBoundsChecks asserts Toggle flips expansion within bounds
// and no-ops outside them (issue #208 US7).
func TestToolLog_ToggleBoundsChecks(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})

	l.Toggle(0)
	if !l.Entry(0).expanded {
		t.Errorf("entry should be expanded after Toggle(0)")
	}
	l.Toggle(0)
	if l.Entry(0).expanded {
		t.Errorf("entry should collapse after second Toggle(0)")
	}
	// Out-of-bounds toggles must not panic or corrupt.
	l.Toggle(-1)
	l.Toggle(5)
}

// TestToolLog_ReviewProjectsChangedFiles asserts Review consolidates the
// file-mutating (edit/write) entries by path, keeping the most recent state per
// path and classifying added/modified/deleted (issue #90, #208 US5).
func TestToolLog_ReviewProjectsChangedFiles(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"a.go"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "edit", Result: "added", Lines: 1,
		Before: "", After: "package a\n", Path: "a.go", Added: 1, Removed: 0}})
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "write", Args: `{"path":"a.go"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "write", Result: "rewritten", Lines: 1,
		Before: "package a\n", After: "package a\n\nfunc x() {}\n", Path: "a.go", Added: 2, Removed: 0}})

	rev := l.Review()
	if len(rev) != 1 {
		t.Fatalf("expected one reviewed file, got %d", len(rev))
	}
	if rev[0].path != "a.go" || rev[0].status != "modified" {
		t.Errorf("review entry = %+v, want a.go/modified", rev[0])
	}
	// The most recent (write) state wins.
	if !strings.Contains(rev[0].after, "func x()") {
		t.Errorf("review should keep the most recent after-content, got: %q", rev[0].after)
	}
}

// TestToolLog_RenderWritesEntryWithRowRanges asserts Render emits the tool
// entry text and records each entry's content-row range for the anchor (issue
// #208 US6: one layout pass shared by transcript and hit-test).
func TestToolLog_RenderWritesEntryWithRowRanges(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})

	got, rows := l.Render(defaultTheme, false, time.Time{}, 80, 0)
	if !strings.Contains(got, "⊕ bash") {
		t.Errorf("Render must emit the tool head, got %q", got)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row range, got %d", len(rows))
	}
	if rows[0].idx != 0 {
		t.Errorf("row range idx = %d, want 0", rows[0].idx)
	}
}

// TestToolLog_PlainTextRendersEntry asserts PlainText emits the ⊕ tool head and
// indents the complete result, mirroring the clipboard transcript (issue #123).
func TestToolLog_PlainTextRendersEntry(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "one\ntwo\n", Lines: 2}})

	out := l.PlainText(0)
	if !strings.Contains(out, "⊕ read  a.txt") {
		t.Errorf("PlainText must include the head, got %q", out)
	}
	if !strings.Contains(out, "  one") || !strings.Contains(out, "  two") {
		t.Errorf("PlainText must indent the result lines, got %q", out)
	}
}
