package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestModel_heightAwareClampsHistory asserts the history region is a
// Height-aware viewport (issue T02): the terminal Height is captured from
// WindowSizeMsg and the history clamps to it, so a long
// session never overflows the terminal — the composer + status band stay on
// screen and only the history scrolls.
func TestModel_heightAwareClampsHistory(t *testing.T) {
	m := newTallHistoryModel(t)
	// Install a small-but-realistic terminal (band = status strip + composer,
	// ~7 rows) so the whole transcript cannot fit.
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = asModel(t, nm)

	content := view(m)
	// The band (status strip + composer) must be pinned at the bottom of the
	// content, i.e. the final composer line is the last non-blank content.
	comp := m.composer.View()
	if !strings.Contains(content, comp) {
		t.Fatalf("composer (band) missing from content, got:\n%q", content)
	}
	if !strings.HasSuffix(strings.TrimRight(content, "\n"), strings.TrimRight(comp, "\n")) {
		t.Errorf("composer band must be the bottom (last) region of the content, got:\n%q", content)
	}
	// Total rendered content must not exceed the terminal height (band + clamped
	// history viewport). Spurious padding lines are permitted by Bubble Tea, but
	// the visible content must be the band sitting at the terminal bottom.
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > 10 {
		t.Errorf("content exceeds terminal height (10), got %d lines:\n%q", len(lines), content)
	}
}

// TestModel_bandPinnedOnResize asserts the composer + status band stay pinned
// at the bottom across a window shrink and grow (issue T02): resizing never
// lets the band trail off-screen and the band remains the final region of the
// content at any height.
func TestModel_bandPinnedOnResize(t *testing.T) {
	m := newTallHistoryModel(t)

	for _, h := range []int{24, 10, 14, 18} {
		nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: h})
		m = asModel(t, nm)
		content := view(m)
		comp := m.composer.View()
		if !strings.Contains(content, comp) {
			t.Fatalf("composer band lost at height %d, got:\n%q", h, content)
		}
		// Band is the last region: after the composer there is only whitespace.
		trimmed := strings.TrimRight(content, "\n")
		if !strings.HasSuffix(trimmed, strings.TrimRight(comp, "\n")) {
			t.Errorf("band not pinned to bottom at height %d, got:\n%q", h, content)
		}
		if n := len(strings.Split(trimmed, "\n")); n > h {
			t.Errorf("view (%d lines) exceeds terminal height %d at resize, got:\n%q", n, h, trimmed)
		}
	}
}

// TestModel_historyClipHoldsNewestFollowSeam asserts the history is genuinely
// clipped by the Height-aware viewport: for a long transcript in a short
// window it is impossible to show every committed message, so the visible
// region drops lines that exist in the full unclamped history — proving the
// history clamps rather than the band yielding. The follow-to-bottom behaviour
// that keeps the newest output visible is the separate T04 seam (#108).
func TestModel_historyClipHoldsNewestFollowSeam(t *testing.T) {
	m := newTallHistoryModel(t)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 10})
	m = asModel(t, nm)

	// The full unclamped history far exceeds the viewport height (band ~7 rows
	// leaves ~3 for history), so the newest turn cannot all be visible.
	var hist strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	histLines := len(strings.Split(strings.TrimRight(hist.String(), "\n"), "\n"))
	viewLines := len(strings.Split(strings.TrimRight(view(m), "\n"), "\n"))
	if viewLines >= histLines {
		t.Errorf("history not clipped: view (%d lines) shows the whole %d-line history instead of height-aware viewport", viewLines, histLines)
	}
}

// newTallHistoryModel builds a model whose transcript is long enough to
// overflow a small terminal: five user+assistant turns, plus a wired telemetry
// strip so the band has a status line.
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
	// Ensure a stat strip renders (telemetry is wired) -> band has 2+ lines.
	nm, _ := m.Update(telemetryUpdateMsg{update: TelemetryUpdate{Kind: TelemetryUsage, Hit: 1, Miss: 1, Output: 1}})
	m = asModel(t, nm)
	return m
}
