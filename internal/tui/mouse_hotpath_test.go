package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func heavyMouseHistoryModel(t *testing.T) Model {
	t.Helper()
	answer := strings.Repeat("This is a rendered answer row with `inline code`, **bold text**, and enough words to wrap cleanly.\n", 40)
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: answer + prompt}, nil
		},
		WorkspacePath: "/tmp/acme",
		Clipboard:     func(string) error { return nil },
	})
	m = resizeTo(t, m, 120, 12)
	for i := 0; i < 4; i++ {
		m = typeText(t, m, "q"+string(rune('a'+i)))
		m = submitAndWait(t, m)
	}
	view(m)
	if m.tx.histViewport.YOffset() <= 0 {
		t.Fatalf("test needs overflowing history, got offset %d", m.tx.histViewport.YOffset())
	}
	return m
}

func TestMouseWheelViewUsesCachedHistory(t *testing.T) {
	m := heavyMouseHistoryModel(t)
	initialBuilds := m.tx.layout.builds
	if initialBuilds == 0 {
		t.Fatalf("initial view must hydrate the transcript layout cache")
	}

	for i := 0; i < 20; i++ {
		m = mustUpdate(t, m, wheelMsg(true))
		view(m)
	}

	if got := m.tx.layout.builds; got != initialBuilds {
		t.Fatalf("wheel scroll re-rendered full history: builds %d -> %d", initialBuilds, got)
	}
}

func TestDragMotionViewUsesCachedHistory(t *testing.T) {
	m := heavyMouseHistoryModel(t)
	rows, top := historyContentRows(m)
	screenRow := 0
	for top+screenRow < len(rows) && strings.TrimSpace(rows[top+screenRow]) == "" {
		screenRow++
	}
	if top+screenRow >= len(rows) {
		t.Fatalf("no visible non-blank row")
	}
	initialBuilds := m.tx.layout.builds
	if initialBuilds == 0 {
		t.Fatalf("initial view must hydrate the transcript layout cache")
	}

	m = mustUpdate(t, m, dragMsg("press", 0, screenRow))
	for i := 0; i < 40; i++ {
		m = mustUpdate(t, m, tea.MouseMotionMsg{Button: tea.MouseLeft, X: i % 20, Y: screenRow})
		view(m)
	}

	if got := m.tx.layout.builds; got != initialBuilds {
		t.Fatalf("drag motion re-rendered full history: builds %d -> %d", initialBuilds, got)
	}
}
