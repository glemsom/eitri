package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel_heightAwareClampsHistory(t *testing.T) {
	t.Parallel()
	m := newTallHistoryModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = asModel(t, nm)

	content := view(m)
	comp := m.composer.View()
	if !strings.Contains(content, comp) {
		t.Fatalf("composer (band) missing from content, got:\n%q", content)
	}
	if !strings.HasSuffix(strings.TrimRight(content, "\n"), strings.TrimRight(comp, "\n")) {
		t.Errorf("composer band must be the bottom (last) region of the content, got:\n%q", content)
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > 10 {
		t.Errorf("content exceeds terminal height (10), got %d lines:\n%q", len(lines), content)
	}
}

func TestModel_bandPinnedOnResize(t *testing.T) {
	t.Parallel()
	m := newTallHistoryModel(t)

	for _, h := range []int{24, 10, 14, 18} {
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		m = asModel(t, nm)
		content := view(m)
		comp := m.composer.View()
		if !strings.Contains(content, comp) {
			t.Fatalf("composer band lost at height %d, got:\n%q", h, content)
		}
		trimmed := strings.TrimRight(content, "\n")
		if !strings.HasSuffix(trimmed, strings.TrimRight(comp, "\n")) {
			t.Errorf("band not pinned to bottom at height %d, got:\n%q", h, content)
		}
		if n := len(strings.Split(trimmed, "\n")); n > h {
			t.Errorf("view (%d lines) exceeds terminal height %d at resize, got:\n%q", n, h, trimmed)
		}
	}
}

func TestModel_historyClipHoldsNewestFollowSeam(t *testing.T) {
	t.Parallel()
	m := newTallHistoryModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = asModel(t, nm)

	var hist strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	histLines := len(strings.Split(strings.TrimRight(hist.String(), "\n"), "\n"))
	viewLines := len(strings.Split(strings.TrimRight(view(m), "\n"), "\n"))
	if viewLines >= histLines {
		t.Errorf("history not clipped: view (%d lines) shows the whole %d-line history instead of height-aware viewport", viewLines, histLines)
	}
}

func newTallHistoryModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "answer " + prompt}, nil
		},
		Telemetry: NewTelemetry("deepseek-v4-flash", "low", true, 250),
	})
	for i := 1; i <= 5; i++ {
		m = typeText(t, m, "q"+string(rune('a'+i-1)))
		m = submitAndWait(t, m)
	}
	nm, _ := m.Update(telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 1, Miss: 1, Output: 1}})
	m = asModel(t, nm)
	return m
}
