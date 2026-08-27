package tui

import (
	"strings"
	"testing"
	"time"
)

// renderViaFlow renders the log's anchored entries through the FlowRenderer —
// the only tool-entry renderer since the legacy tool-log render path was
// deleted (issue #493). It mirrors the Transcript's flowInput assembly: one
// start event per anchored entry, expansion read through the ExpansionState
// seam.
func renderViaFlow(l toolLog, mode viewMode, defaultCollapsed bool, now time.Time, width, anchor int, pulse bool) (string, []toolRowRange) {
	tools := make([]flowTool, 0)
	var events []TimelineEvent
	for i := range l.entries {
		if l.entries[i].anchor != anchor {
			continue
		}
		tools = append(tools, flowTool{entry: l.entries[i], logIdx: i, expanded: l.expandedFor(i, expansionConfig{mode: mode, toolExpanded: !defaultCollapsed})})
		events = append(events, TimelineEvent{Kind: EventToolStart})
	}
	return RenderFlow(flowInput{Events: events, Theme: defaultTheme, Width: width, Pulse: pulse, Now: now, Tools: tools})
}

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

func TestToolLog_ApplyPairsMostRecentIncompleteSameName(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"a"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a out", Lines: 1}})
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"b"}`}})
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

// TestToolLog_ExpandForceCollapseBoundsChecks locks that the live per-entry
// operations (the two toggleToolEntry routes) bounds-check their index: an
// in-range expansion/force-collapse pins the seam force, and out-of-range
// indices are silent no-ops that never disturb an in-range entry.
func TestToolLog_ExpandForceCollapseBoundsChecks(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})

	l.Expand(0)
	if !l.expandedFor(0, expansionConfig{mode: viewDefault, toolExpanded: !true}) {
		t.Errorf("entry should be expanded after Expand(0)")
	}
	l.ForceCollapse(0)
	if l.expandedFor(0, expansionConfig{mode: viewExpandAll, toolExpanded: !true}) {
		t.Errorf("entry should be collapsed after ForceCollapse(0)")
	}

	l.Expand(-1)
	l.Expand(5)
	l.ForceCollapse(-1)
	l.ForceCollapse(5)
	if l.expandedFor(0, expansionConfig{mode: viewExpandAll, toolExpanded: !true}) {
		t.Errorf("out-of-range operations must not disturb the in-range force-collapse")
	}
}

// TestToolLog_ExpansionSeamOwnsForces locks issue #470's migration: the per-entry
// expand / force-collapse operations route through the ExpansionState seam, so
// the per-block force lives on l.expansion (keyed by the flat log index) and
// every open/collapsed decision reads through it.
func TestToolLog_ExpansionSeamOwnsForces(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})

	// No force yet: the default (collapsed) decision and no pinned force on the seam.
	if l.expandedFor(0, expansionConfig{mode: viewDefault, toolExpanded: !true}) {
		t.Errorf("fresh entry must follow the collapsed default, got expanded")
	}
	if _, ok := l.expansion.forceFor(blockTool, 0); ok {
		t.Errorf("fresh entry must carry no seam force")
	}

	// Expand pins a force-expand on the seam that beats the collapsed default.
	l.Expand(0)
	if !l.expandedFor(0, expansionConfig{mode: viewDefault, toolExpanded: !true}) {
		t.Errorf("Expand must leave the entry expanded under the collapsed default")
	}
	if f, ok := l.expansion.forceFor(blockTool, 0); !ok || !f {
		t.Errorf("Expand must pin an expand force on the seam, got ok=%v force=%v", ok, f)
	}

	// ForceCollapse pins the opposite force, beating even the expand-all mode.
	l.ForceCollapse(0)
	if l.expandedFor(0, expansionConfig{mode: viewExpandAll, toolExpanded: !true}) {
		t.Errorf("ForceCollapse must win over expand-all mode, got expanded")
	}
	if f, ok := l.expansion.forceFor(blockTool, 0); !ok || f {
		t.Errorf("ForceCollapse must pin a collapse force on the seam, got ok=%v force=%v", ok, f)
	}
}

func TestToolLog_PlainTextRendersEntry(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "one\ntwo\n", Lines: 2}})

	out := clipboardToolText(l, 0)
	if !strings.Contains(out, "🔧 bash  ls") {
		t.Errorf("PlainText must include the head, got %q", out)
	}
	if !strings.Contains(out, "  one") || !strings.Contains(out, "  two") {
		t.Errorf("PlainText must indent the result lines, got %q", out)
	}
}

func TestToolLog_PlainTextCollapsedAndExpanded(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"cat a.go"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "change\n", Lines: 1}})

	out := clipboardToolText(l, 0)
	if out != "🔧 bash  ls\n🔧 bash  cat a.go\n  change\n" {
		t.Errorf("PlainText shape mismatch, got %q", out)
	}
}

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
			name:  "web fetch",
			entry: toolEntry{name: "web_fetch", args: `{"url":"https://example.com"}`},
			want:  "🌐 web_fetch  https://example.com",
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

func TestToolLog_RenderRowAccountCollapsed(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"go test"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "ok\n", Lines: 5}})

	_, rows := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if len(rows) != 1 {
		t.Fatalf("expected one row range, got %d", len(rows))
	}
	if rows[0].start != 0 || rows[0].end != 1 {
		t.Errorf("collapsed row range = %d..%d, want 0..1", rows[0].start, rows[0].end)
	}
	if idx, _, ok := l.AtLine(0, rows, expansionConfig{mode: viewDefault, toolExpanded: false}); !ok || idx != 0 {
		t.Errorf("AtLine(0) = %d/%v, want entry 0", idx, ok)
	}
	if idx, _, ok := l.AtLine(1, rows, expansionConfig{mode: viewDefault, toolExpanded: false}); !ok || idx != 0 {
		t.Errorf("AtLine(1) = %d/%v, want entry 0 (summary row)", idx, ok)
	}
}

func TestToolLog_RenderRowAccountExpanded(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"go test"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "ok\n", Lines: 1}})
	l.Expand(0) // flip the entry open

	_, rows := renderViaFlow(l, viewExpandAll, true, time.Time{}, 80, 0, false)
	if len(rows) != 1 {
		t.Fatalf("expected one row range, got %d", len(rows))
	}
	if rows[0].start != 0 || rows[0].end != 3 {
		t.Errorf("expanded row range = %d..%d, want 0..3", rows[0].start, rows[0].end)
	}
}

func TestToolLog_RenderRowAccountSkipsOtherAnchors(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(2)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.SetAnchor(7)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})

	_, rows := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 2, false)
	if len(rows) != 1 {
		t.Fatalf("expected the anchor-2 entry only, got %d ranges", len(rows))
	}
	if rows[0].idx != 0 {
		t.Errorf("row range idx = %d, want 0 (the anchor-2 entry)", rows[0].idx)
	}
}

func TestToolLog_RenderOutcomeElapsedAndTruncation(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"make build"}`}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "done\n", Lines: 1}})
	start := time.Now().Add(-105 * time.Second)
	l.SetStart(0, start)

	got, _ := renderViaFlow(l, viewDefault, true, time.Now(), 80, 0, false)
	if !strings.Contains(got, "🔧 bash") {
		t.Errorf("head missing, got %q", got)
	}
	if !strings.Contains(got, "1m 44s") {
		t.Errorf("elapsed readout missing, got %q", got)
	}
	if !strings.Contains(got, "✓") {
		t.Errorf("outcome marker missing, got %q", got)
	}

	narrow, _ := renderViaFlow(l, viewDefault, true, time.Time{}, 18, 0, false)
	if strings.Contains(narrow, "make build") || !strings.Contains(narrow, "…") {
		t.Errorf("args should truncate with an ellipsis at width 18, got %q", narrow)
	}
}

// TestToolLog_AtLineConsistentWithRender locks the hit-test fix: AtLine's
// collapsed answer must come from the same config the render used, even when
// the seam's stored mode is stale (constructed in default mode, rendered under
// collapse-all).
func TestToolLog_AtLineConsistentWithRender(t *testing.T) {
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\nb\n", Lines: 2}})

	// seam constructed/stored in default mode; render + hit-test in collapse-all.
	cfg := expansionConfig{mode: viewCollapseAll}
	_, rows := renderViaFlow(l, viewCollapseAll, true, time.Time{}, 80, 0, false)
	if idx, collapsed, ok := l.AtLine(1, rows, cfg); !ok || idx != 0 || !collapsed {
		t.Errorf("AtLine under collapse-all = %d/%v/%v, want entry 0 collapsed", idx, collapsed, ok)
	}

	// expand-all config: same row now reports expanded.
	_, rowsAll := renderViaFlow(l, viewExpandAll, true, time.Time{}, 80, 0, false)
	if idx, collapsed, ok := l.AtLine(1, rowsAll, expansionConfig{mode: viewExpandAll}); !ok || idx != 0 || collapsed {
		t.Errorf("AtLine under expand-all = %d/%v/%v, want entry 0 expanded", idx, collapsed, ok)
	}
}

func TestToolLog_AtLineMapping(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\nb\n", Lines: 2}}) // rows 0..1
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "c\n", Lines: 1}}) // rows 2..3

	_, rows := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if len(rows) != 2 {
		t.Fatalf("expected two row ranges, got %d", len(rows))
	}

	cfg := expansionConfig{mode: viewDefault, toolExpanded: false}
	if idx, collapsed, ok := l.AtLine(1, rows, cfg); !ok || idx != 0 || !collapsed {
		t.Errorf("AtLine(1) = %d/%v/%v, want entry 0 collapsed=ok", idx, collapsed, ok)
	}
	if idx, collapsed, ok := l.AtLine(2, rows, cfg); !ok || idx != 1 || !collapsed {
		t.Errorf("AtLine(2) = %d/%v/%v, want entry 1 collapsed=ok", idx, collapsed, ok)
	}

	l.Expand(1)
	_, rows2 := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if idx, collapsed, ok := l.AtLine(2, rows2, cfg); !ok || idx != 1 || collapsed {
		t.Errorf("AtLine(2) = %d/%v/%v, want entry 1 expanded (collapsed=false)", idx, collapsed, ok)
	}

	if _, _, ok := l.AtLine(99, rows, cfg); ok {
		t.Errorf("AtLine(99) should be out of range")
	}
	if _, _, ok := l.AtLine(0, nil, cfg); ok {
		t.Errorf("AtLine over nil rows should be out of range")
	}
}

func TestToolLog_RenderFailureOutcome(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "error executing tool: boom", Lines: 0}})

	got, _ := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "✗") {
		t.Errorf("failure entry must render ✗, got %q", got)
	}
}

func TestToolLog_RenderBytesTruncatedHint(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a\nb\nc\n+3 more\n",
		Lines: 4, Dropped: 3, Compressed: true, BytesDropped: 2048}})

	got, _ := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "4 lines (+3 more, +2048 bytes truncated)") {
		t.Errorf("collapsed summary missing merged truncated hint, got %q", got)
	}

	l2 := toolLog{}
	l2.SetAnchor(0)
	l2.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l2.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: strings.Repeat("x", 70000),
		Lines: 1, BytesDropped: 4444}})
	got2, _ := renderViaFlow(l2, viewDefault, true, time.Time{}, 80, 0, false)
	if !strings.Contains(got2, "1 line (+4444 bytes truncated)") {
		t.Errorf("collapsed summary missing bytes-only hint, got %q", got2)
	}
	if strings.Contains(got2, "(+0 more)") {
		t.Errorf("bytes-only hint must not render a stale line-marker hint, got %q", got2)
	}
}

func TestToolLog_RenderBothHintsWithoutCompressedFlag(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: strings.Repeat("x", 70000),
		Lines: 4, Dropped: 3, Compressed: false, BytesDropped: 2048}})

	got, _ := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "4 lines (+3 more, +2048 bytes truncated)") {
		t.Errorf("collapsed summary must show both hints regardless of Compressed, got %q", got)
	}
}

func TestToolLog_ExpandedRendersFullRawResult(t *testing.T) {
	t.Parallel()
	var l toolLog
	l.SetAnchor(0)
	l.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: ""}})
	l.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "RAW FULL RESULT",
		Lines: 1, BytesDropped: 9000}})
	l.Expand(0)

	got, _ := renderViaFlow(l, viewDefault, true, time.Time{}, 80, 0, false)
	if !strings.Contains(got, "RAW FULL RESULT") {
		t.Errorf("expanded view must render the full raw result, got %q", got)
	}
}
