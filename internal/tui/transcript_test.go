package tui

import (
	"strings"
	"testing"

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
