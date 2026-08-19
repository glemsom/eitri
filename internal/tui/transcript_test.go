package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// TestTranscript_thinkingGateScopesReasoningBlock asserts a reasoning block
// renders only for a turn that requested thinking: a message whose
// thinkingRequested flag is false shows no reasoning block even when the
// backend streamed chain-of-thought content, while a true flag renders the
// collapsible hint as before.
func TestTranscript_thinkingGateScopesReasoningBlock(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(thinkingRequested bool) string {
		var hist strings.Builder
		tx := Transcript{
			theme:           th,
			configTheme:     config.DefaultTheme,
			reasoningEffort: "medium",
			messages: []message{{
				role:              "eitri",
				content:           "final answer",
				reasoning:         "sneaked chain-of-thought",
				thinkingRequested: thinkingRequested,
				thinkingExpanded:  true,
			}},
			histFollow:   true,
			histViewport: newHistoryViewport(),
		}
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	// Thinking OFF but the backend still emitted reasoning: the block must be
	// hidden regardless of what the backend returned.
	if off := render(false); strings.Contains(off, "🤔") || strings.Contains(off, "sneaked chain-of-thought") {
		t.Errorf("thinking-off turn rendered a reasoning block, got: %q", off)
	}
	// Thinking ON restores the reasoning block exactly as today (the hint and
	// its per-turn reasoning gate fire).
	on := render(true)
	if !strings.Contains(on, "tok") {
		t.Errorf("thinking-on turn must render the reasoning hint, got: %q", on)
	}
	if !strings.Contains(on, "sneaked chain-of-thought") {
		t.Errorf("expanded thinking-on turn must carry its reasoning, got: %q", on)
	}
}

// TestTranscript_expandAllOverridesThinkingExpansion asserts the Ctrl+E
// expanded-view mode drives the reasoning block's effective
// expansion at the render seam: with the mode ON the reasoning body
// renders even when the per-turn thinkingExpanded flag is false (the default
// auto-collapse), a per-turn collapse override (tab while mode ON) still
// collapses it, and with the mode OFF only the per-turn flag decides.
func TestTranscript_expandAllOverridesThinkingExpansion(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(expandAll bool, thinkingExpanded bool, thinkingCollapsed bool) string {
		var hist strings.Builder
		tx := Transcript{
			theme:           th,
			configTheme:     config.DefaultTheme,
			reasoningEffort: "medium",
			expandAll:       expandAll,
			messages: []message{{
				role:              "eitri",
				content:           "final answer",
				reasoning:         "the reasoning body",
				thinkingRequested: true,
				thinkingExpanded:  thinkingExpanded,
				thinkingCollapsed: thinkingCollapsed,
			}},
			histFollow:   true,
			histViewport: newHistoryViewport(),
		}
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	// Mode OFF, per-turn collapsed: matches today (hint only, no body).
	if off := render(false, false, false); strings.Contains(off, "the reasoning body") {
		t.Errorf("mode OFF collapsed: must not render the body, got: %q", off)
	}
	// Mode ON overrides the default auto-collapse: body renders expanded.
	if on := render(true, false, false); !strings.Contains(on, "the reasoning body") {
		t.Errorf("mode ON must render the body even when per-turn flag is false, got: %q", on)
	}
	// Mode ON with a per-turn collapse override (tab): that block collapses.
	if over := render(true, false, true); strings.Contains(over, "the reasoning body") {
		t.Errorf("mode ON with a collapse override must render collapsed, got: %q", over)
	}
	// Mode OFF still honors tab-expanded per-turn flag (independent of mode).
	if exp := render(false, true, false); !strings.Contains(exp, "the reasoning body") {
		t.Errorf("mode OFF per-turn expanded must render the body, got: %q", exp)
	}
}

// TestTranscript_ownsRailSurface proves the right-context rail surface — its
// visibility, band/transcript width accounting, clamp height, and render — now
// lives on the Transcript value: a Transcript constructed directly
// with a wired rail exposes railVisible / bandWidth / transcriptWidth /
// railClampHeight and surfaces the rail into a pane through its own render
// seam, without reaching through Model.
func TestTranscript_ownsRailSurface(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	tx := Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true,
			"eitri-1", "/tmp/eitri-1"),
	}

	// railVisible derives from the wired rail, not a Model bool.
	if !tx.railVisible() {
		t.Fatalf("Transcript with a wired rail must report it visible")
	}

	// bandWidth spans the full terminal width (pre-resize default) minus gutter.
	if bw := tx.bandWidth(); bw != presizeTerminalWidth-2 {
		t.Errorf("bandWidth = %d, want %d", bw, presizeTerminalWidth-2)
	}

	// transcriptWidth stays rail-shrunk and below bandWidth.
	if bw, tw := tx.bandWidth(), tx.transcriptWidth(); tw >= bw {
		t.Errorf("transcriptWidth %d must be rail-shrunk below bandWidth %d", tw, bw)
	}

	// Once the terminal height resolves, the clamp budgets the rail rows above
	// the band (height minus the passed-in band height).
	tx.width, tx.height = 120, 40
	if c := tx.railClampHeight(4); c != 36 {
		t.Errorf("railClampHeight(band 4) = %d, want height-band=36", c)
	}

	// The rail render surfaces into the full-width pane through the Transcript.
	pane := tx.renderPane("band\n")
	out := tx.viewWithRail(pane, 4)
	if !strings.Contains(out, "STATS") || !strings.Contains(out, "│") {
		t.Errorf("Transcript rail surface must render STATS + rail border, got: %q", out)
	}
}

// TestTranscript_dynamicRailWidth proves the rail width is mutable state on the
// Transcript: setting the width re-derives the rail-shrunk
// transcript width, re-renders the rail at the new width, and marks the layout
// cache dirty so the next render pass re-wraps the history, so a future
// drag-resize lands as one state write plus the normal render pass. The default
// field value (0 -> defaultRailWidth) keeps hand-built Transcripts rendering
// at the historical 30 columns.
func TestTranscript_dynamicRailWidth(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true,
			"eitri-1", "/tmp/eitri-1"),
		width:  120,
		height: 30,
	}

	// Unset field: consumers fall back to the default 30-column width.
	if d := tx.railWidthOrDefault(); d != 30 {
		t.Errorf("railWidthOrDefault = %d, want default 30", d)
	}
	defaultTW := tx.transcriptWidth()
	if defaultTW != 120-2-31 {
		t.Errorf("transcriptWidth = %d at default width, want %d", defaultTW, 120-2-31)
	}

	// A narrower rail yields the transcript more columns and re-renders the
	// rail strip narrower (left border still present). The setter marks the
	// shared layout cache dirty so the next render re-wraps at the new width.
	tx.setRailWidth(22)
	if tx.railWidthOrDefault() != 22 {
		t.Errorf("railWidthOrDefault = %d after set, want 22", tx.railWidthOrDefault())
	}
	if tw := tx.transcriptWidth(); tw != 120-2-23 {
		t.Errorf("transcriptWidth = %d at railWidth 22, want %d", tw, 120-2-23)
	}
	if !tx.layout.dirty {
		t.Errorf("setRailWidth must mark the layout cache dirty (re-wrap trigger), got clean")
	}
	rails := strings.Split(tx.viewWithRail(tx.renderPane("band\n"), 4), "\n")
	bordered := false
	for _, ln := range rails {
		if w := lipgloss.Width(ln); w > 120 {
			t.Errorf("surface row width %d exceeds terminal 120 at railWidth 22: %q", w, ln)
		}
		if strings.Contains(ln, g("│", "|")) {
			bordered = true
		}
	}
	if !bordered {
		t.Errorf("rail must still render its left border at width 22, got:\n%q", strings.Join(rails, "\n"))
	}
}

// TestTranscript_matchesModelRender asserts the Model renders the scroll region
// through its OWNED Transcript: the Model's tx field produces the
// same history content an independently-constructed Transcript with the same
// transcript state does, so the contract step left one transcript renderer with
// no per-frame rebuild from duplicated Model fields.
func TestTranscript_matchesModelRender(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	msgs := []message{{role: "you", content: "hello"}, {role: "eitri", content: "**plain** answer"}}
	tx := Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		workspacePath:   "/tmp/acme",
		messages:        msgs,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}
	// A Model whose owned tx carries exactly that state.
	m := NewModelCfg(Dependencies{WorkspacePath: "/tmp/acme", Config: config.Config{Theme: config.DefaultTheme, ReasoningEffort: "medium"}})
	m.tx.messages = msgs
	m.tx.width = 80
	m.tx.height = 12

	var viaModel strings.Builder
	m.tx.renderHistory(&viaModel, nil, nil)

	var viaTranscript strings.Builder
	tx.renderHistory(&viaTranscript, nil, nil)

	if viaModel.String() != viaTranscript.String() {
		t.Errorf("Model render diverged from an equal standalone Transcript:\nmodel:      %q\ntranscript: %q",
			viaModel.String(), viaTranscript.String())
	}
}

// transcriptScrollModel builds a Transcript whose viewport is hydrated with
// overflowing content so keyboard/mouse navigation can move it:
// the same arrangement the Model-path scroll tests use, but expressed purely at
// the Transcript seam so the navigation migration proves it lives on Transcript.
func transcriptScrollModel(t *testing.T) Transcript {
	t.Helper()
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 120, 12)
	tx := *m.tx // shallow copy of the shared root for Transcript-seam navigation
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
// lives on the Transcript value: PgUp/Home/PgDn/End drive the
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
// tool entry . The entry anchors to message 0 and renders as a
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
		theme:       th,
		configTheme: config.DefaultTheme,
		messages:    []message{{role: "you", content: "run it"}},
		log:         log,
		width:       80,
		height:      12,
		layout:      transcriptLayout{dirty: true},
	}
}

// TestTranscript_toolEntryAtLineReadsPersistentIndex asserts the click-to-expand
// hit-test now lives on the Transcript: toolEntryAtLine
// maps a content row back to the owning entry through the Transcript's
// persistent row->entry index — the collapsed head and its summary row both
// resolve to entry 0, and the first call builds the layout exactly once while
// repeats reuse the recorded index (no re-render).
func TestTranscript_toolEntryAtLineReadsPersistentIndex(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.ensureLayout() // build the persistent row->entry index once
	if len(tx.layout.rows) == 0 {
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
// toggle now lives on the Transcript: toggling entry 0 expands
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
// Transcript: apply folds a Start/Result pair into the
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

// TestTranscript_toggleExpandAllFlipsGlobalMode asserts the Ctrl+E
// global all-entries expanded view mode now lives on the Transcript (issue
// #273 AC1/U1): a toggling Transcript flips its persistent expandAll flag and
// reports the new value back, the same state the transcript render reads for
// global expansion. It covers the on→off→on sequence (AC: toggling).
func TestTranscript_toggleExpandAllFlipsGlobalMode(t *testing.T) {
	tx := transcriptWithTool(t)
	if tx.expandAll {
		t.Fatalf("transcript should start collapsed (expanded view off)")
	}
	// on
	if v := tx.toggleExpandAll(); !v || !tx.expandAll {
		t.Errorf("toggleExpandAll must expand all, got value=%v field=%v", v, tx.expandAll)
	}
	// off
	if v := tx.toggleExpandAll(); v || tx.expandAll {
		t.Errorf("second toggleExpandAll must collapse all, got value=%v field=%v", v, tx.expandAll)
	}
	// on again
	if v := tx.toggleExpandAll(); !v || !tx.expandAll {
		t.Errorf("third toggleExpandAll must expand all again, got value=%v field=%v", v, tx.expandAll)
	}
}

// TestTranscript_toggleExpandAllEmptyLogIsNoOp asserts toggling the Ctrl+E
// expanded-view mode on a transcript with no tool entries must not panic or
// break: the flag flips but the
// empty log renders nothing and stays coherent.
func TestTranscript_toggleExpandAllEmptyLogIsNoOp(t *testing.T) {
	tx := transcriptWithTool(t)
	// Drain the seeded entry so the log is empty, then ensure the empty-log
	// toggle path is safe.
	tx.log.entries = tx.log.entries[:0]
	if tx.toggleExpandAll() != true {
		t.Fatalf("toggleExpandAll on empty log must turn the mode on")
	}
	// Rendering an empty log with the mode on must not panic or fabricate an
	// entry.
	var b strings.Builder
	tx.renderHistory(&b, nil, nil)
	if strings.Contains(b.String(), "🔧 bash") {
		t.Errorf("empty log must render no tool entries, got: %q", b.String())
	}
	if tx.expandAll != true {
		t.Errorf("expandAll must stay flipped after rendering an empty log")
	}
}

// TestTranscript_expandAllPerEntryCollapseOverride asserts the Ctrl+E
// expanded-view mode keeps per-entry click-to-expand orthogonal (
// AC); a per-entry collapse still works even when the global mode is ON — it
// overrides the expanded view for just that entry while others stay expanded.
func TestTranscript_expandAllPerEntryCollapseOverride(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.expandAll = true

	var on strings.Builder
	tx.renderHistory(&on, nil, nil)
	if !strings.Contains(on.String(), "a.go") {
		t.Fatalf("with expandAll on, entry must render its full result, got: %q", on.String())
	}

	// A click in expanded mode collapses just this entry.
	tx.toggleCollapse(0)
	var off strings.Builder
	tx.renderHistory(&off, nil, nil)
	if strings.Contains(off.String(), "a.go") {
		t.Errorf("per-entry collapse must hide the result even in expanded-view mode, got: %q", off.String())
	}

	// Clicking again re-expands just this entry through the global mode.
	tx.toggleCollapse(0)
	var again strings.Builder
	tx.renderHistory(&again, nil, nil)
	if !strings.Contains(again.String(), "a.go") {
		t.Errorf("second per-entry toggle must re-expand in expanded-view mode, got: %q", again.String())
	}
}

// TestTranscript_toolEntryAtLineEffectiveCollapse asserts the click-to-collapse
// hit-test reports the EFFECTIVE rendered state under the Ctrl+E expanded-view
// mode, not the raw per-entry flag: with the mode ON an entry the
// user collapses via the per-entry override reports collapsed=true (a click
// would re-expand it), and when the override is released the mode re-shows it
// as expanded. toolEntryAtLine reads the same expandedFor computation as Render,
// so the hit-test and the rendered rows never disagree.
func TestTranscript_toolEntryAtLineEffectiveCollapse(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.ensureLayout()
	head := tx.layout.rows[0].start

	// Global mode ON: everything expands by default.
	tx.expandAll = true
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || collapsed {
		t.Errorf("expanded mode: entry 0 must report expanded, got %d/%v/%v", idx, collapsed, ok)
	}

	// Per-entry collapse beats the mode for just this entry.
	tx.toggleCollapse(0)
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || !collapsed {
		t.Errorf("override collapse: entry 0 must report collapsed, got %d/%v/%v", idx, collapsed, ok)
	}

	// Releasing the override re-expands it through the global mode.
	tx.toggleCollapse(0)
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || collapsed {
		t.Errorf("override released: entry 0 must report expanded, got %d/%v/%v", idx, collapsed, ok)
	}
}

// TestTranscript_expandAllOffDoesNotWipePerEntry asserts turning the global
// expanded-view mode OFF does not wipe per-entry expansion: an
// entry the user opened individually stays open, and toggling back on shows
// everything again.
func TestTranscript_expandAllOffDoesNotWipePerEntry(t *testing.T) {
	tx := transcriptWithTool(t)
	// Open entry 0 per-entry while the global mode is off.
	tx.toggleToolEntry(0)
	var one strings.Builder
	tx.renderHistory(&one, nil, nil)
	if !strings.Contains(one.String(), "a.go") {
		t.Fatalf("per-entry open must show the result in default mode, got: %q", one.String())
	}

	// Toggle the global mode on then off; the per-entry open survives.
	tx.toggleExpandAll()
	tx.toggleExpandAll()
	if !tx.log.Entry(0).expanded {
		t.Fatalf("toggling expandAll must not wipe per-entry expansion state")
	}
	var back strings.Builder
	tx.renderHistory(&back, nil, nil)
	if !strings.Contains(back.String(), "a.go") {
		t.Errorf("per-entry open must persist after an expandAll on/off cycle, got: %q", back.String())
	}
}

// layoutBuildsOf reports how many layout builds a Transcript's cache has
// performed ( test hook, so a repeated hit-test can assert the
// persistent index is reused).
func layoutBuildsOf(tx Transcript) int {
	return tx.layout.builds
}

// newStreamPaneTestTranscript builds a Transcript with the standard test
// fixture fields (theme, workspace, dimensions) and the given messages.
func newStreamPaneTestTranscript(th Theme, msgs []message) Transcript {
	return Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		workspacePath:   "/tmp/acme",
		messages:        msgs,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}
}

// TestRenderHistory_streamingAssistantUsesDimmedPane asserts that a
// streaming assistant message renders with the dimmed streaming pane style
// instead of the full agent pane style: the left-bordered pane uses
// streamingPaneStyle while the turn is still in-flight.
func TestRenderHistory_streamingAssistantUsesDimmedPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(streaming bool) string {
		var hist strings.Builder
		tx := newStreamPaneTestTranscript(th, []message{{role: "eitri", content: "partial", streaming: streaming}})
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	streaming := render(true)
	completed := render(false)

	// Both renders must contain the message content.
	if !strings.Contains(ansiStrip(streaming), "partial") {
		t.Fatalf("streaming render must contain message content, got: %q", streaming)
	}
	if !strings.Contains(ansiStrip(completed), "partial") {
		t.Fatalf("completed render must contain message content, got: %q", completed)
	}

	// The streaming pane must use a different border color than the completed
	// pane: identical ANSI output means the streaming branch was not taken.
	if streaming == completed {
		t.Errorf("streaming pane must differ from completed pane (streaming branch not taken)")
	}

	// Verify the border foreground color matches streamingPaneStyle, not
	// agentPaneStyle. Extract the first 24-bit color from the rendered border
	// line and compare it against the expected theme style.
	streamingColor := borderColorCode(streaming)
	agentColor := borderColorCode(completed)
	if streamingColor == agentColor {
		t.Errorf("streaming border color %q must differ from completed border color %q", streamingColor, agentColor)
	}

	expectedStreamColor := borderColorStr(th.streamingPaneStyle)
	if streamingColor != expectedStreamColor {
		t.Errorf("streaming border must use streamingPaneStyle color %q, got %q", expectedStreamColor, streamingColor)
	}
}

// TestRenderHistory_completedAssistantUsesAgentPane asserts that a completed
// (non-streaming) assistant message renders with the full agent pane style:
// no regression from the streaming pane change.
func TestRenderHistory_completedAssistantUsesAgentPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	var hist strings.Builder
	tx := newStreamPaneTestTranscript(th, []message{{role: "eitri", content: "done", streaming: false}})
	tx.renderHistory(&hist, nil, nil)
	rendered := hist.String()

	if !strings.Contains(ansiStrip(rendered), "done") {
		t.Fatalf("completed render must contain message content, got: %q", rendered)
	}

	// Verify the border foreground color matches agentPaneStyle.
	gotColor := borderColorCode(rendered)
	expectedColor := borderColorStr(th.agentPaneStyle)
	if gotColor != expectedColor {
		t.Errorf("completed assistant border must use agentPaneStyle color %q, got %q", expectedColor, gotColor)
	}
}

// reasoningLineInfo returns the ANSI border color of the rendered line that
// carries the given reasoning body text, and whether that line is framed by
// the pane's left border glyph (i.e. the reasoning body rendered inside a
// pane rather than raw).
func reasoningLineInfo(s, body string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(line, body) {
			continue
		}
		framed := strings.Contains(line, g("\u2502", "|"))
		start := strings.Index(line, "\x1b[38;2;")
		if start == -1 {
			return "", framed
		}
		end := strings.IndexByte(line[start+len("\x1b[38;2;"):], 'm')
		if end == -1 {
			return "", framed
		}
		return line[start+len("\x1b[38;2;") : start+len("\x1b[38;2;")+end], framed
	}
	return "", false
}

// TestRenderHistory_liveReasoningBlockUsesStreamingPane asserts that the
// current turn's expanded reasoning block renders inside the dimmed streaming
// pane border while reasoning deltas stream (issue #364 AC1): the live
// thinking panel the user watches grow. A completed (non-streaming) expanded
// reasoning block keeps the plain agent pane border instead.
func TestRenderHistory_liveReasoningBlockUsesStreamingPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(streaming bool) string {
		var hist strings.Builder
		tx := newStreamPaneTestTranscript(th, []message{{
			role:              "eitri",
			reasoning:         "the live reasoning body",
			streaming:         streaming,
			thinkingRequested: true,
			thinkingExpanded:  true,
		}})
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	const body = "the live reasoning body"

	live := render(true)
	liveColor, liveFramed := reasoningLineInfo(live, body)
	if !liveFramed {
		t.Errorf("live reasoning body must render inside a pane, got line info for: %q", live)
	}
	if liveColor != borderColorStr(th.streamingPaneStyle) {
		t.Errorf("live reasoning block must use streamingPaneStyle color, got %q", liveColor)
	}

	done := render(false)
	doneColor, doneFramed := reasoningLineInfo(done, body)
	if !doneFramed {
		t.Errorf("completed reasoning body must render inside a pane, got line info for: %q", done)
	}
	if doneColor != borderColorStr(th.agentPaneStyle) {
		t.Errorf("completed reasoning block must use agentPaneStyle color, got %q", doneColor)
	}
}

// TestRenderHistory_liveReasoningRespectsTabCollapse asserts the tab thinking
// toggle still collapses a live streaming reasoning block (issue #364 AC3): a
// streaming-reasoning message carrying the per-turn collapse override renders
// collapsed even while it is streaming.
func TestRenderHistory_liveReasoningRespectsTabCollapse(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	var hist strings.Builder
	tx := newStreamPaneTestTranscript(th, []message{{
		role:              "eitri",
		content:           "final answer",
		reasoning:         "hidden reasoning",
		streaming:         true,
		thinkingRequested: true,
		thinkingExpanded:  true,
		thinkingCollapsed: true,
	}})
	tx.renderHistory(&hist, nil, nil)
	if strings.Contains(ansiStrip(hist.String()), "hidden reasoning") {
		t.Errorf("tab-collapsed live reasoning block must render collapsed, got: %q", hist.String())
	}
}

// TestRenderHistory_liveReasoningBlockRespectsThinkingGate asserts the
// thinking-off gate also holds for the live panel (issue #364 AC4): a
// streaming message on a thinking-off turn must not render a reasoning block
// even though it auto-expanded while reasoning streamed.
func TestRenderHistory_liveReasoningBlockRespectsThinkingGate(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	var hist strings.Builder
	tx := newStreamPaneTestTranscript(th, []message{{
		role:              "eitri",
		content:           "final answer",
		reasoning:         "sneaked reasoning",
		streaming:         true,
		thinkingRequested: false,
		thinkingExpanded:  true,
	}})
	tx.renderHistory(&hist, nil, nil)
	rendered := hist.String()
	if strings.Contains(ansiStrip(rendered), "sneaked reasoning") {
		t.Errorf("thinking-off live turn must not render the reasoning block, got: %q", rendered)
	}
}

// TestTranscript_liveReasoningBlockTogglesViaTab asserts that while a turn's
// reasoning streams, tab toggles the auto-expanded live panel through the
// per-turn collapse override and re-expands it back (issue #364 AC3), and that
// a completed turn's block collapses back to the hint automatically (AC2).
func TestTranscript_liveReasoningBlockTogglesViaTab(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	tx := newStreamPaneTestTranscript(th, []message{{
		role:              "eitri",
		reasoning:         "visible reasoning",
		streaming:         true,
		thinkingRequested: true,
	}})

	// Live and auto-expanded by default: the body is visible.
	var live strings.Builder
	tx.renderHistory(&live, nil, nil)
	if !strings.Contains(ansiStrip(live.String()), "visible reasoning") {
		t.Fatalf("streaming reasoning must render its body expanded, got: %q", live.String())
	}

	// Tab collapses the live block.
	tx.toggleThinking(0)
	var collapsed strings.Builder
	tx.renderHistory(&collapsed, nil, nil)
	if strings.Contains(ansiStrip(collapsed.String()), "visible reasoning") {
		t.Errorf("tab must collapse the live reasoning block, got: %q", collapsed.String())
	}

	// Tab again re-expands it.
	tx.toggleThinking(0)
	var reexpanded strings.Builder
	tx.renderHistory(&reexpanded, nil, nil)
	if !strings.Contains(ansiStrip(reexpanded.String()), "visible reasoning") {
		t.Errorf("tab must re-expand the live reasoning block, got: %q", reexpanded.String())
	}

	// Once the turn completes (not streaming) the block collapses to the hint.
	tx.messages[0].streaming = false
	var done strings.Builder
	tx.renderHistory(&done, nil, nil)
	if strings.Contains(ansiStrip(done.String()), "visible reasoning") {
		t.Errorf("a completed turn's reasoning block must collapse to the hint, got: %q", done.String())
	}
}

// TestRenderHistory_streamingErrorPrefixUsesDimmedErrorPane asserts that a
// streaming assistant message with the error prefix renders with the dimmed
// streaming error pane style: error-prefix messages that are still streaming
// use streamingErrorPaneStyle, not the full errorPaneStyle.
func TestRenderHistory_streamingErrorPrefixUsesDimmedErrorPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(streaming bool) string {
		var hist strings.Builder
		tx := newStreamPaneTestTranscript(th, []message{{role: "eitri", content: failurePrefix() + "broke", streaming: streaming}})
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	streaming := render(true)
	completed := render(false)

	// Both renders must contain the error prefix.
	if !strings.Contains(ansiStrip(streaming), failurePrefix()) {
		t.Fatalf("streaming error render must contain the error prefix, got: %q", streaming)
	}
	if !strings.Contains(ansiStrip(completed), failurePrefix()) {
		t.Fatalf("completed error render must contain the error prefix, got: %q", completed)
	}

	// Streaming error pane must differ from completed error pane.
	streamingColor := borderColorCode(streaming)
	completedColor := borderColorCode(completed)
	if streamingColor == completedColor {
		t.Errorf("streaming error border color %q must differ from completed error border color %q", streamingColor, completedColor)
	}

	// Verify streaming error uses streamingErrorPaneStyle.
	expectedColor := borderColorStr(th.streamingErrorPaneStyle)
	if streamingColor != expectedColor {
		t.Errorf("streaming error must use streamingErrorPaneStyle color %q, got %q", expectedColor, streamingColor)
	}
}

// borderColorCode extracts the first 24-bit ANSI foreground color from a
// rendered string. It looks for the pattern \x1b[38;2;R;G;Bm and returns
// "R;G;B" for comparison.
func borderColorCode(s string) string {
	// Find the first occurrence of a border line: a line starting with the
	// pane style's ANSI color code for the left border.
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, g("\u2502", "|")) {
			// Extract the first 24-bit color code from this line.
			start := strings.Index(line, "\x1b[38;2;")
			if start == -1 {
				continue
			}
			end := strings.IndexByte(line[start+len("\x1b[38;2;"):], 'm')
			if end == -1 {
				continue
			}
			return line[start+len("\x1b[38;2;") : start+len("\x1b[38;2;")+end]
		}
	}
	return ""
}

// borderColorStr extracts the border foreground color from a lipgloss style
// as "R;G;B" for comparison with borderColorCode.
func borderColorStr(style lipgloss.Style) string {
	c := style.GetBorderLeftForeground()
	if c == nil {
		return ""
	}
	r, g2, b, _ := c.RGBA()
	return fmt.Sprintf("%d;%d;%d", r>>8, g2>>8, b>>8)
}
