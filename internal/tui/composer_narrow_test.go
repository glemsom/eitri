package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestRenderTitledPanelTruncatesBodyToNarrowWidth(t *testing.T) {
	got := renderTitledPanel("Long title", 8, lipgloss.NewStyle(), "abcdefghi")
	for _, line := range strings.Split(ansiStrip(got), "\n") {
		if strings.HasPrefix(line, "│") && lipgloss.Width(line) > 8 {
			t.Fatalf("panel body row exceeds panel width 8: %q\n%s", line, got)
		}
	}
	if !strings.Contains(ansiStrip(got), "…") {
		t.Fatalf("narrow overflowing body should truncate with ellipsis, got:\n%s", got)
	}
}

func TestModelCommandCenterNarrowMatrixDoesNotOverflow(t *testing.T) {
	cases := map[string]func(Model) Model{
		"empty":   func(m Model) Model { return m },
		"draft":   func(m Model) Model { return typeText(t, m, strings.Repeat("a", 40)) },
		"slash":   func(m Model) Model { return typeText(t, m, "/") },
		"mention": func(m Model) Model { return typeText(t, m, "@") },
		"feedback": func(m Model) Model {
			m.feedback = successFeedback("copied a very long transcript message")
			return m
		},
		"busy": func(m Model) Model {
			m.tx.busy = true
			return m
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			m := NewModelCfg(Dependencies{})
			nm, _ := m.Update(tea.WindowSizeMsg{Width: 12, Height: 10})
			m = setup(nm.(Model))
			content := ansiStrip(view(m))
			band := content
			if i := strings.Index(content, strings.Repeat("─", m.tx.bandWidth())); i >= 0 {
				band = content[i:]
			}
			for _, line := range strings.Split(band, "\n") {
				if w := lipgloss.Width(line); w > m.tx.bandWidth()+1 {
					t.Fatalf("%s band row width %d exceeds band width %d: %q\n%s", name, w, m.tx.bandWidth(), line, content)
				}
			}
		})
	}
}
