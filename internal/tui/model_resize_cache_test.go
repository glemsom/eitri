package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips ANSI escape sequences from a render so phrase matching is not
// broken by Glamour's per-word styling runs.
func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// This file covers the T1 alt-screen pivot's resize behaviour (issue #119): the
// TUI renders through the alternate screen, so every frame is a clean repaint
// and a resize re-flows the transcript with no duplicated or scattered lines.
// The former per-width-bucket scroll-cache (ADR-0006 decision 4 / T03) that
// existed to compensate for the primary buffer is gone; each render rebuilds the
// history and lets the native viewport clip it, so a resize cannot leave stale
// residue.

// TestResize_KeepsNewestPinnedAcrossResize asserts a resize re-flows the
// transcript and keeps the newest content pinned in view without duplicating or
// scattering any committed line: the newest answer is present exactly once at
// both the wide and narrow sizes.
func TestResize_KeepsNewestPinnedAcrossResize(t *testing.T) {
	m := buildMultiTurnModel(t)
	m = applyResize(t, m, 120, 24)

	// Wide: the newest answer is present and not duplicated.
	wide := m.View()
	assertNewestOnce(t, wide, "answer q3")

	// Shrink the window; the transcript must re-flow (re-word-wrap) without
	// duplicating or scattering the newest committed output.
	m = applyResize(t, m, 80, 18)
	narrow := m.View()
	assertNewestOnce(t, narrow, "answer q3")

	// Grow back: still a single coherent newest answer, never doubled by stale
	// primary-buffer residue.
	m = applyResize(t, m, 120, 24)
	assertNewestOnce(t, m.View(), "answer q3")
}

// TestResize_ReFlowsHeadToNewWidth asserts resizing to a narrower width
// (re-)wraps rendered content, shrinking the oldest line out of the viewport as
// the newest stays pinned — proving a live re-flow rather than a frozen frame.
func TestResize_ReFlowsHeadToNewWidth(t *testing.T) {
	m := buildMultiTurnModel(t)
	m = applyResize(t, m, 80, 12) // short viewport clips the oldest head

	// The oldest prompt head is scrolled out of the pinned-to-bottom viewport.
	clean := plain(m.View())
	if strings.Count(clean, "q1") != 0 {
		t.Errorf("narrow viewport should pin to newest and clip the q1 head, got %d occurrences", strings.Count(clean, "q1"))
	}
	if !strings.Contains(clean, "answer q3") {
		t.Errorf("narrow viewport must hold the newest answer, got:\n%s", clean)
	}
}

// assertNewestOnce asserts msg appears in render exactly once, after stripping
// ANSI styling so phrase matching is reliable.
func assertNewestOnce(t *testing.T, render, msg string) {
	t.Helper()
	clean := plain(render)
	if n := strings.Count(clean, msg); n != 1 {
		t.Errorf("newest %q appears %d times (want once) after repaint\n%s", msg, n, clean)
	}
}

// applyResize drives one WindowSizeMsg into the model, asserting the returned
// tea.Model is still a tui.Model.
func applyResize(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return asModel(t, nm)
}

// buildMultiTurnModel builds a model with several committed turns whose answers
// are distinctive, so a resize-miss can be detected by missing/duplicated words.
func buildMultiTurnModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "answer " + prompt}, nil
		},
	})
	for i := 0; i < 3; i++ {
		m = typeText(t, m, "q"+string(rune('1'+i)))
		m = submitAndWait(t, m)
	}
	return m
}
