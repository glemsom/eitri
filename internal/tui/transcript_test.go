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
