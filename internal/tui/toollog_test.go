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

// TestToolLog_PlainTextCollapsedAndExpanded asserts PlainText renders the head
// alone for an entry whose result has not landed yet (collapsed) and the head
// plus the indented full result once it is complete (expanded) — the two
// shapes the clipboard transcript never truncates (issue #123, #208 US2).
func TestToolLog_PlainTextCollapsedAndExpanded(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	// A Start with no Result: incomplete, so the head is all there is.
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	// A completed entry whose head carries the delta tag and whose result is indented.
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"a.go"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "edit", Result: "change\n", Lines: 1,
		Before: "a", After: "b", Path: "a.go", Added: 1, Removed: 1}})

	out := l.PlainText(0)
	if out != "⊕ bash  ls\n⊕ edit  a.go  [+1, −1]\n  change\n" {
		t.Errorf("PlainText shape mismatch, got %q", out)
	}
}

// TestToolLog_ReviewClassifiesAddDeleteModify asserts Review classifies each
// file-mutating entry by its before/after content span into added/deleted/
// modified, keeping the same behaviour as the historical review builder (issue
// #90, #208 US5).
func TestToolLog_ReviewClassifiesAddDeleteModify(t *testing.T) {
	applyFile := func(l *toolLog, name, path, before, after string) {
		l.SetAnchor(0)
		l.Apply(ToolUpdate{Start: &ToolStart{Name: name, Args: `{"path":"` + path + `"}`}})
		l.Apply(ToolUpdate{Result: &ToolResult{Name: name, Result: "done", Lines: 1,
			Before: before, After: after, Path: path}})
	}

	cases := []struct {
		name   string
		before string
		after  string
		want   string
	}{
		{name: "added", before: "", after: "package a\n", want: "added"},
		{name: "deleted", before: "package a\n", after: "", want: "deleted"},
		{name: "modified", before: "a", after: "b", want: "modified"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var l toolLog
			applyFile(&l, "edit", "f.go", c.before, c.after)
			rev := l.Review()
			if len(rev) != 1 {
				t.Fatalf("expected one reviewed file, got %d", len(rev))
			}
			if rev[0].status != c.want {
				t.Errorf("status = %q, want %q", rev[0].status, c.want)
			}
		})
	}
}

// TestToolLog_HeadForms asserts the shared head builders render the compact
// `⊕ tool  args` head in its three forms — plain args, the read `:start-end`
// range, and the file-edit `[+N, −M]` delta tag (issue #204, #84, #208 US4).
func TestToolLog_HeadForms(t *testing.T) {
	cases := []struct {
		name  string
		entry toolEntry
		want  string
	}{
		{
			name:  "plain args",
			entry: toolEntry{name: "bash", args: `{"command":"ls"}`},
			want:  "⊕ bash  ls",
		},
		{
			name:  "read range",
			entry: toolEntry{name: "read", args: `{"path":"a.txt","start_line":3,"end_line":7}`},
			want:  "⊕ read  a.txt:3-7",
		},
		{
			name:  "edit delta",
			entry: toolEntry{name: "edit", args: `{"path":"a.go"}`, added: 2, removed: 1},
			want:  "⊕ edit  a.go  [+2, −1]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := toolEntryHead(c.entry); got != c.want {
				t.Errorf("toolEntryHead = %q, want %q", got, c.want)
			}
		})
	}
}
