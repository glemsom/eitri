package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestResize_KeepsNewestPinnedAcrossResize(t *testing.T) {
	t.Parallel()
	m := buildMultiTurnModel(t)
	m = applyResize(t, m, 120, 24)

	assertNewestOnce(t, view(m), "answer q3")

	for _, w := range []int{100, 80, 60, 40} {
		m = applyResize(t, m, w, 18)
		assertNewestOnce(t, view(m), "answer q3")
	}

	m = applyResize(t, m, 120, 24)
	assertNewestOnce(t, view(m), "answer q3")
}

func TestResize_ReFlowsHeadToNewWidth(t *testing.T) {
	t.Parallel()
	m := buildMultiTurnModel(t)
	m = applyResize(t, m, 80, 12) // short viewport clips the oldest head

	clean := plain(view(m))
	if strings.Count(clean, "q1") != 0 {
		t.Errorf("narrow viewport should pin to newest and clip the q1 head, got %d occurrences", strings.Count(clean, "q1"))
	}
	if !strings.Contains(clean, "answer q3") {
		t.Errorf("narrow viewport must hold the newest answer, got:\n%s", clean)
	}
}

func assertNewestOnce(t *testing.T, render, msg string) {
	t.Helper()
	clean := plain(render)
	if n := strings.Count(clean, msg); n != 1 {
		t.Errorf("newest %q appears %d times (want once) after repaint\n%s", msg, n, clean)
	}
}

func applyResize(t *testing.T, m Model, w, h int) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return asModel(t, nm)
}

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
