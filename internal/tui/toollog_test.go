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
// Result fold into one complete entry with the result field filled.
func TestToolLog_ApplyPairsStartWithResult(t *testing.T) {
	t.Parallel()
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
// out-of-order result cannot corrupt an already-complete entry.
func TestToolLog_ApplyPairsMostRecentIncompleteSameName(t *testing.T) {
	t.Parallel()
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
// and no-ops outside them.
func TestToolLog_ToggleBoundsChecks(t *testing.T) {
	t.Parallel()
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
// path.
func TestToolLog_ReviewProjectsChangedFiles(t *testing.T) {
	t.Parallel()
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
	if rev[0].path != "a.go" {
		t.Errorf("review entry = %+v, want a.go", rev[0])
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
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})

	got, rows := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "🔧 bash") {
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
// indents the complete result, mirroring the clipboard transcript.
func TestToolLog_PlainTextRendersEntry(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "one\ntwo\n", Lines: 2}})

	out := l.PlainText(0)
	if !strings.Contains(out, "📖 read  a.txt") {
		t.Errorf("PlainText must include the head, got %q", out)
	}
	if !strings.Contains(out, "  one") || !strings.Contains(out, "  two") {
		t.Errorf("PlainText must indent the result lines, got %q", out)
	}
}

// TestToolLog_PlainTextCollapsedAndExpanded asserts PlainText renders the head
// alone for an entry whose result has not landed yet (collapsed) and the head
// plus the indented full result once it is complete (expanded) — the two
// shapes the clipboard transcript never truncates.
func TestToolLog_PlainTextCollapsedAndExpanded(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	// A Start with no Result: incomplete, so the head is all there is.
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	// A completed entry whose head carries the delta tag and whose result is indented.
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"a.go"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "edit", Result: "change\n", Lines: 1,
		Before: "a", After: "b", Path: "a.go", Added: 1, Removed: 1}})

	out := l.PlainText(0)
	if out != "🔧 bash  ls\n✂️ edit  a.go  [+1, −1]\n  change\n" {
		t.Errorf("PlainText shape mismatch, got %q", out)
	}
}

// TestToolLog_ReviewKeepsMostRecentState asserts Review consolidates repeated
// writes to one path by keeping the most recent before/after content span,
// replacing the older entry's content wholesale.
func TestToolLog_ReviewKeepsMostRecentState(t *testing.T) {
	t.Parallel()
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
	}{
		{name: "added-then-rewritten", before: "package a\n", after: "package a\n\nfunc x() {}\n"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var l toolLog
			applyFile(&l, "edit", "f.go", c.before, c.after)
			rev := l.Review()
			if len(rev) != 1 {
				t.Fatalf("expected one reviewed file, got %d", len(rev))
			}
			if rev[0].before != c.before || rev[0].after != c.after {
				t.Errorf("most recent state = (%q, %q), want (%q, %q)", rev[0].before, rev[0].after, c.before, c.after)
			}
		})
	}
}

// TestToolLog_HeadForms asserts the shared head builders render the compact
// per-tool-glyph `tool  args` head in its three forms — plain args, the read `:start-end`
// range, and the file-edit `[+N, −M]` delta tag.
func TestToolLog_HeadForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		entry toolEntry
		want  string
	}{
		{
			name:  "plain args",
			entry: toolEntry{name: "bash", args: `{"command":"ls"}`},
			want:  "🔧 bash  ls",
		},
		{
			name:  "read range",
			entry: toolEntry{name: "read", args: `{"path":"a.txt","start_line":3,"end_line":7}`},
			want:  "📖 read  a.txt:3-7",
		},
		{
			name:  "edit delta",
			entry: toolEntry{name: "edit", args: `{"path":"a.go"}`, added: 2, removed: 1},
			want:  "✂️ edit  a.go  [+2, −1]",
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

// TestToolLog_RenderRowAccountCollapsed asserts Render counts a collapsed entry's
// content rows (head + collapsed summary) and that those ranges feed AtLine
// (render and hit-test share one layout).
func TestToolLog_RenderRowAccountCollapsed(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"go test"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "ok\n", Lines: 5}})

	_, rows := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if len(rows) != 1 {
		t.Fatalf("expected one row range, got %d", len(rows))
	}
	// Collapsed: line-summary head + the "5 lines" readout = rows 0..1.
	if rows[0].start != 0 || rows[0].end != 1 {
		t.Errorf("collapsed row range = %d..%d, want 0..1", rows[0].start, rows[0].end)
	}
	if idx, _, ok := l.AtLine(0, rows); !ok || idx != 0 {
		t.Errorf("AtLine(0) = %d/%v, want entry 0", idx, ok)
	}
	if idx, _, ok := l.AtLine(1, rows); !ok || idx != 0 {
		t.Errorf("AtLine(1) = %d/%v, want entry 0 (summary row)", idx, ok)
	}
}

// TestToolLog_RenderRowAccountExpanded asserts Render counts an expanded entry's
// rows (head + the full framed result lines) as its own row range, so a click on
// any of them toggles the same entry.
func TestToolLog_RenderRowAccountExpanded(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"go test"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "ok\n", Lines: 1}})
	l.Toggle(0) // flip the entry open

	_, rows := l.Render(defaultTheme, true, time.Time{}, 80, 0, false)
	if len(rows) != 1 {
		t.Fatalf("expected one row range, got %d", len(rows))
	}
	// Expanded result "ok" renders as a framed card: the head row plus the
	// card's top padding, content row, and bottom padding = rows 0..3.
	if rows[0].start != 0 || rows[0].end != 3 {
		t.Errorf("expanded row range = %d..%d, want 0..3", rows[0].start, rows[0].end)
	}
}

// TestToolLog_RenderRowAccountSkipsOtherAnchors asserts Render only accounts
// entries for the requested anchor, so content-row ranges stay relative to each
// message's own block and multiple turns share the same Render pass.
func TestToolLog_RenderRowAccountSkipsOtherAnchors(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(2)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.SetAnchor(7)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})

	_, rows := l.Render(defaultTheme, false, time.Time{}, 80, 2, false)
	if len(rows) != 1 {
		t.Fatalf("expected the anchor-2 entry only, got %d ranges", len(rows))
	}
	if rows[0].idx != 0 {
		t.Errorf("row range idx = %d, want 0 (the anchor-2 entry)", rows[0].idx)
	}
}

// TestToolLog_RenderOutcomeElapsedAndTruncation asserts the entry head carries
// the ✓/✗ outcome marker, the elapsed readout for a completed timed tool, and
// arg truncation at narrow widths — the presentation forms Render must preserve
// byte-for-byte.
func TestToolLog_RenderOutcomeElapsedAndTruncation(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"make build"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "done\n", Lines: 1}})
	start := time.Now().Add(-105 * time.Second)
	l.SetStart(0, start)

	// A completed entry freezes its elapsed span from SetStart to now. The few
	// sub-second ms between Apply (which stamps doneAt) and SetStart only shave
	// the fractional part, so the 105s window reads deterministically as 1m 44s.
	got, _ := l.Render(defaultTheme, false, time.Now(), 80, 0, false)
	if !strings.Contains(got, "🔧 bash") {
		t.Errorf("head missing, got %q", got)
	}
	if !strings.Contains(got, "1m 44s") {
		t.Errorf("elapsed readout missing, got %q", got)
	}
	// A success outcome renders the ✓ marker (default glyph mode).
	if !strings.Contains(got, "✓") {
		t.Errorf("outcome marker missing, got %q", got)
	}

	// A narrow-but-viable width truncates the args with an ellipsis rather than
	// cutting abruptly: budget = width − label(6) − 8, so width 18 leaves a
	// budget of 4, and the 10-wide "make build" args truncate.
	narrow, _ := l.Render(defaultTheme, false, time.Time{}, 18, 0, false)
	if strings.Contains(narrow, "make build") || !strings.Contains(narrow, "…") {
		t.Errorf("args should truncate with an ellipsis at width 18, got %q", narrow)
	}
}

// TestToolLog_AtLineMapping asserts AtLine resolves the owning entry, reports its
// collapsed state, and no-ops outside every recorded range.
func TestToolLog_AtLineMapping(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\nb\n", Lines: 2}}) // rows 0..1
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "c\n", Lines: 1}}) // rows 2..3

	_, rows := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if len(rows) != 2 {
		t.Fatalf("expected two row ranges, got %d", len(rows))
	}

	// Collapsed entries report collapsed=true (a click expands them).
	if idx, collapsed, ok := l.AtLine(1, rows); !ok || idx != 0 || !collapsed {
		t.Errorf("AtLine(1) = %d/%v/%v, want entry 0 collapsed=ok", idx, collapsed, ok)
	}
	if idx, collapsed, ok := l.AtLine(2, rows); !ok || idx != 1 || !collapsed {
		t.Errorf("AtLine(2) = %d/%v/%v, want entry 1 collapsed=ok", idx, collapsed, ok)
	}

	// An expanded entry reports collapsed=false (a click collapses it). Expand
	// only that entry and re-render so the other entry's rows stay put: the
	// "read" (idx 1) head row remains at absolute line 2.
	l.Toggle(1)
	_, rows2 := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if idx, collapsed, ok := l.AtLine(2, rows2); !ok || idx != 1 || collapsed {
		t.Errorf("AtLine(2) = %d/%v/%v, want entry 1 expanded (collapsed=false)", idx, collapsed, ok)
	}

	// Bounds: lines above and below the recorded ranges are inert.
	if _, _, ok := l.AtLine(99, rows); ok {
		t.Errorf("AtLine(99) should be out of range")
	}
	// AtLine never panics on an empty row account.
	if _, _, ok := l.AtLine(0, nil); ok {
		t.Errorf("AtLine over nil rows should be out of range")
	}
}

// TestToolLog_RenderFailureOutcome asserts a tool whose result is error-shaped
// renders the ✗ outcome marker.
func TestToolLog_RenderFailureOutcome(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "error executing tool: boom", Lines: 0}})

	got, _ := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "✗") {
		t.Errorf("failure entry must render ✗, got %q", got)
	}
}

// TestToolLog_RenderBytesTruncatedHint asserts a byte-capped delivery renders
// a "(+N bytes truncated)" hint on the collapsed summary line when bytes were
// dropped, and merges with the existing "(+N more)" hint when both line and
// byte truncation happened (never silent for the user either).
func TestToolLog_RenderBytesTruncatedHint(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\nb\nc\n+3 more\n",
		Lines: 4, Dropped: 3, Compressed: true, BytesDropped: 2048}})

	got, _ := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "4 lines (+3 more, +2048 bytes truncated)") {
		t.Errorf("collapsed summary missing merged truncated hint, got %q", got)
	}

	// Byte truncation alone (no line marker): only the bytes hint renders.
	l2 := toolLog{}
	l2.SetAnchor(0)
	l2.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l2.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: strings.Repeat("x", 70000),
		Lines: 1, BytesDropped: 4444}})
	got2, _ := l2.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if !strings.Contains(got2, "1 line (+4444 bytes truncated)") {
		t.Errorf("collapsed summary missing bytes-only hint, got %q", got2)
	}
	if strings.Contains(got2, "(+0 more)") {
		t.Errorf("bytes-only hint must not render a stale line-marker hint, got %q", got2)
	}
}

// TestToolLog_RenderBothHintsWithoutCompressedFlag asserts the collapsed summary
// shows BOTH hints whenever both truncations happened, even when the separate
// Compressed flag is false: the "+N more" hint derives from
// Dropped alone, so a byte-cap that also dropped lines never loses the line
// count just because the result is byte-only-capped in the seam's model.
func TestToolLog_RenderBothHintsWithoutCompressedFlag(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: strings.Repeat("x", 70000),
		Lines: 4, Dropped: 3, Compressed: false, BytesDropped: 2048}})

	got, _ := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "4 lines (+3 more, +2048 bytes truncated)") {
		t.Errorf("collapsed summary must show both hints regardless of Compressed, got %q", got)
	}
}

// TestToolLog_ExpandedRendersFullRawResult asserts the expanded view renders
// the entry's full pre-cap Result even when the delivered form was byte-capped
// (nothing silently truncated on the expand path).
func TestToolLog_ExpandedRendersFullRawResult(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "RAW FULL RESULT",
		Lines: 1, BytesDropped: 9000}})
	l.Toggle(0)

	got, _ := l.Render(defaultTheme, false, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "RAW FULL RESULT") {
		t.Errorf("expanded view must render the full raw result, got %q", got)
	}
}
