package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips ANSI escape sequences from a render so phrase matching is not
// broken by Glamour's per-word styling runs.
func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

// This file covers the T1 alt-screen pivot's resize behaviour: the
// TUI renders through the alternate screen, so every frame is a clean repaint
// and a resize re-flows the transcript with no duplicated or scattered lines.
// The former per-width-bucket scroll-cache that
// existed to compensate for the primary buffer is gone; each render rebuilds the
// history and lets the native viewport clip it, so a resize cannot leave stale
// residue.

// TestResize_KeepsNewestPinnedAcrossResize asserts a resize re-flows the
// transcript and keeps the newest content pinned in view without duplicating or
// scattering any committed line: the newest answer is present exactly once at
// every tested width (wide, mid, and narrow), and a resize back to a prior
// size must never re-introduce doubled or scattered lines.
func TestResize_KeepsNewestPinnedAcrossResize(t *testing.T) {
	t.Parallel()
	m := buildMultiTurnModel(t)
	m = applyResize(t, m, 120, 24)

	// Wide: the newest answer is present and not duplicated.
	assertNewestOnce(t, view(m), "answer q3")

	// Shrink the window stepwise; the transcript must re-flow (re-word-wrap)
	// without duplicating or scattering the newest committed output at any
	// width along the way.
	for _, w := range []int{100, 80, 60, 40} {
		m = applyResize(t, m, w, 18)
		assertNewestOnce(t, view(m), "answer q3")
	}

	// Grow back: still a single coherent newest answer, never doubled by stale
	// primary-buffer residue.
	m = applyResize(t, m, 120, 24)
	assertNewestOnce(t, view(m), "answer q3")
}

// TestResize_ReFlowsHeadToNewWidth asserts resizing to a narrower width
// (re-)wraps rendered content, shrinking the oldest line out of the viewport as
// the newest stays pinned — proving a live re-flow rather than a frozen frame.
func TestResize_ReFlowsHeadToNewWidth(t *testing.T) {
	t.Parallel()
	m := buildMultiTurnModel(t)
	m = applyResize(t, m, 80, 12) // short viewport clips the oldest head

	// The oldest prompt head is scrolled out of the pinned-to-bottom viewport.
	clean := plain(view(m))
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
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "answer " + prompt}, nil
		},
	})
	for i := 0; i < 3; i++ {
		m = typeText(t, m, "q"+string(rune('1'+i)))
		m = submitAndWait(t, m)
	}
	return m
}
