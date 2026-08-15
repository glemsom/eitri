package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// TestTranscript_rendersStandalone proves the Transcript value seam (issue
// #243) is real: a Transcript constructed directly — not reached through Model
// — renders the scroll region through its own height/width/follow/render
// state. It is the fixture the expand half ships to prove a standalone
// Transcript can own the transcript render path.
func TestTranscript_rendersStandalone(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := Transcript{
		theme:           th,
		messages:        []message{{role: "you", content: "hello"}},
		configTheme:     config.DefaultTheme,
		workspacePath:   "/tmp/acme",
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}

	// Render the history into a scratch builder through the Transcript directly,
	// then clip it through the same Transcript's viewport.
	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	content := hist.String()
	if !strings.Contains(content, "hello") {
		t.Errorf("standalone Transcript must render its messages, got: %q", content)
	}
	if !strings.Contains(content, "workspace: /tmp/acme") {
		t.Errorf("standalone Transcript must render the workspace header, got: %q", content)
	}

	region := tx.renderHistoryViewport(content, 2) // reserve 2 rows (a synthetic band)
	if region == "" {
		t.Fatalf("standalone Transcript viewport render returned empty for content %q", content)
	}
	// The reserved rows leave height-2=10 rows for the scroll region.
	if n := strings.Count(region, "\n"); n > 11 {
		t.Errorf("viewport render exceeded reserved height (%d newline-terminated rows)", n)
	}
}

// TestTranscript_matchesModelRender asserts the Transcript value renders the
// scroll region byte-for-byte identically to the Model render it replaces
// (issue #243 AC3): Model delegates to newTranscript, so the two must agree on
// the history content for the same state.
func TestTranscript_matchesModelRender(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	msgs := []message{{role: "you", content: "hello"}, {role: "eitri", content: "**plain** answer"}}
	m := Model{
		theme:           th,
		messages:        msgs,
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		deps:            Dependencies{WorkspacePath: "/tmp/acme", Config: config.Config{Theme: config.DefaultTheme}},
		reasoningEffort: "medium",
	}

	var viaModel strings.Builder
	m.renderHistory(&viaModel, nil, nil)

	var viaTranscript strings.Builder
	newTranscript(m).renderHistory(&viaTranscript, nil, nil)

	if viaModel.String() != viaTranscript.String() {
		t.Errorf("Transcript render diverged from Model render:\nmodel:      %q\ntranscript: %q",
			viaModel.String(), viaTranscript.String())
	}
}

// transcriptScrollModel builds a Transcript whose viewport is hydrated with
// overflowing content so keyboard/mouse navigation (issue #244) can move it:
// the same arrangement the Model-path scroll tests use, but expressed purely at
// the Transcript seam so the navigation migration proves it lives on Transcript.
func transcriptScrollModel(t *testing.T) Transcript {
	t.Helper()
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 120, 12)
	tx := newTranscript(m)
	// Hydrate the persisted (shared) viewport with the current content so
	// navigation has a real scroll range.
	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	tx.renderHistoryViewport(hist.String(), m.bandHeight())
	vp := tx.histViewport
	if vp == nil || vp.TotalLineCount() <= vp.Height() {
		t.Fatalf("test must overflow: viewport lines (%d) should exceed height (%d)", vp.TotalLineCount(), vp.Height())
	}
	return tx
}

// TestTranscript_navigateScrollsSharedViewport asserts the navigation seam now
// lives on the Transcript value (issue #244): PgUp/Home/PgDn/End drive the
// shared persisted viewport through the Transcript, moving the reading offset
// and mutating the transcript's follow flag.
func TestTranscript_navigateScrollsSharedViewport(t *testing.T) {
	tx := transcriptScrollModel(t)
	vp := tx.histViewport
	start := vp.YOffset()
	if start <= 0 {
		t.Fatalf("overflowed follow should start at the bottom, got offset %d", start)
	}

	// PgUp moves up; the returned flag reports follow broke.
	if follow := tx.navigateHistory("pgup"); follow {
		t.Errorf("PgUp must break follow, got follow=true")
	}
	if up := vp.YOffset(); up >= start {
		t.Errorf("PgUp must move the viewport up: offset %d -> %d", start, up)
	}

	// Home jumps to the top.
	tx.navigateHistory("home")
	if top := vp.YOffset(); top != 0 {
		t.Errorf("Home should jump to the transcript top, got offset %d", top)
	}

	// PgDn moves down; End reaches the bottom and re-engages follow.
	tx.navigateHistory("pgdown")
	if down := vp.YOffset(); down <= 0 {
		t.Errorf("PgDn must move the viewport down from the top, got offset %d", down)
	}
	tx.navigateHistory("end")
	if !vp.AtBottom() {
		t.Errorf("End should jump to the transcript bottom, got offset %d", vp.YOffset())
	}
	if follow := tx.navigateHistory("end"); !follow {
		t.Errorf("End reaching the bottom should re-engage follow, got follow=false")
	}

	// Wheel up scrolls toward older output and breaks follow.
	wheelStart := vp.YOffset()
	if follow := tx.navigateMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp}); follow {
		t.Errorf("wheel up must break follow, got follow=true")
	}
	if up := vp.YOffset(); up >= wheelStart {
		t.Errorf("wheel up must scroll up, offset %d -> %d", wheelStart, up)
	}
}

// transcriptWithTool builds a standalone Transcript carrying one completed
// tool entry (issue #245). The entry anchors to message 0 and renders as a
// collapsed block whose rows the tool-surface routes map back to it.
func transcriptWithTool(t *testing.T) Transcript {
	t.Helper()
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go\n", Lines: 2}})
	return Transcript{
		theme:      th,
		configTheme: config.DefaultTheme,
		messages:    []message{{role: "you", content: "run it"}},
		log:         log,
		width:       80,
		height:      12,
	}
}

// TestTranscript_toolEntryAtLineReadsPersistentIndex asserts the click-to-expand
// hit-test now lives on the Transcript (issue #245 AC1/AC3): toolEntryAtLine
// maps a content row back to the owning entry through the Transcript's
// persistent row->entry index — the collapsed head and its summary row both
// resolve to entry 0, and the first call builds the layout exactly once while
// repeats reuse the recorded index (no re-render).
func TestTranscript_toolEntryAtLineReadsPersistentIndex(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.ensureLayout() // build the persistent row->entry index once
	if tx.layout == nil || len(tx.layout.rows) == 0 {
		t.Fatalf("hit-test must record a tool-entry row index")
	}
	head := tx.layout.rows[0].start

	idx, collapsed, ok := tx.toolEntryAtLine(head)
	if !ok || idx != 0 || !collapsed {
		t.Errorf("toolEntryAtLine(%d) = %d/%v/%v, want entry 0 collapsed=ok", head, idx, collapsed, ok)
	}

	for i := 0; i < 20; i++ {
		if idx, _, ok := tx.toolEntryAtLine(head); !ok || idx != 0 {
			t.Errorf("toolEntryAtLine(%d) = %d/%v, want entry 0 (reused index)", head, idx, ok)
		}
	}
	if tx.layout.builds != 1 {
		t.Errorf("hit-test must build the layout exactly once, got builds=%d", layoutBuildsOf(tx))
	}

	if _, _, ok := tx.toolEntryAtLine(99); ok {
		t.Errorf("toolEntryAtLine(99) must be out of range")
	}
}

// TestTranscript_toggleToolEntryFlipsExpansion asserts the per-entry expansion
// toggle now lives on the Transcript (issue #245 AC1): toggling entry 0 expands
// it open and a second toggle collapses it, exactly like the tool-log Toggle the
// click path maps to.
func TestTranscript_toggleToolEntryFlipsExpansion(t *testing.T) {
	tx := transcriptWithTool(t)

	tx.toggleToolEntry(0)
	if !tx.log.Entry(0).expanded {
		t.Errorf("toggleToolEntry(0) must expand entry 0")
	}
	tx.toggleToolEntry(0)
	if tx.log.Entry(0).expanded {
		t.Errorf("second toggleToolEntry(0) must collapse entry 0")
	}
}

// TestTranscript_applyFoldsToolUpdate asserts tool updates now route through the
// Transcript (issue #245 AC1/AC2): apply folds a Start/Result pair into the
// transcript's own log — the same entries renderPane reads — so the live
// transcript never drifts from what the tools accomplished.
func TestTranscript_applyFoldsToolUpdate(t *testing.T) {
	tx := transcriptWithTool(t)
	before := tx.log.Len()

	tx.apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	tx.apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "contents", Lines: 1}})

	if tx.log.Len() != before+1 {
		t.Fatalf("apply must fold a new entry, got len before=%d after=%d", before, tx.log.Len())
	}
	e := tx.log.Entry(tx.log.Len() - 1)
	if e.name != "read" || e.result != "contents" || !e.complete {
		t.Errorf("apply result entry = %+v, want read/contents/complete", e)
	}
}

// TestTranscript_toggleShowToolResultFlipsGlobalExpansion asserts the alt+y
// global all-entries toggle now lives on the Transcript (issue #245 AC2): a
// toggling Transcript flips its showToolResult flag and reports the new value
// back, the same state the transcript render reads for global expansion.
func TestTranscript_toggleShowToolResultFlipsGlobalExpansion(t *testing.T) {
	tx := transcriptWithTool(t)
	if tx.showToolResult {
		t.Fatalf("transcript should start globally collapsed")
	}
	if v := tx.toggleShowToolResult(); !v || !tx.showToolResult {
		t.Errorf("toggleShowToolResult must expand all, got value=%v field=%v", v, tx.showToolResult)
	}
	if v := tx.toggleShowToolResult(); v || tx.showToolResult {
		t.Errorf("second toggleShowToolResult must collapse all, got value=%v field=%v", v, tx.showToolResult)
	}
}

// layoutBuildsOf reports how many layout builds a Transcript's shared cache has
// performed (issue #242 AC4 test hook, read through the pointer so a repeated
// hit-test can assert the persistent index is reused).
func layoutBuildsOf(tx Transcript) int {
	if tx.layout == nil {
		return 0
	}
	return tx.layout.builds
}
