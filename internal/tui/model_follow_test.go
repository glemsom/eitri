package tui

import (
	"context"
	"strings"
	"testing"
)

func busyStreamingModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
	})
	m = resizeTo(t, m, 80, 8)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	for i := 0; i < 6; i++ {
		m = applyDelta(t, m, strings.Repeat("word ", 20))
	}
	return m
}

func followRendered(m Model) (got string, histContent string, vh int) {
	var hist strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	histContent = hist.String()
	reserved := m.bandHeight()
	vh = m.tx.height - reserved
	return m.tx.renderHistoryViewport(histContent, reserved), histContent, vh
}

func newestNonBlank(render string) string {
	lines := strings.Split(strings.TrimRight(render, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		clean := ansiStrip(lines[i])
		if strings.TrimSpace(clean) != "" {
			return strings.TrimRight(clean, " ") + "\n"
		}
	}
	return ""
}

func TestModel_liveFollowKeepsNewestOutput(t *testing.T) {
	t.Parallel()
	m := busyStreamingModel(t)
	if !m.tx.busy {
		t.Fatalf("test model must be mid-run (busy)")
	}
	got, histContent, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("expected a positive viewport height, got %d", vh)
	}
	if n := lineCount(histContent); n <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", n, vh)
	}
	if got := newestNonBlank(got); got != "⠋ Answering\n" {
		t.Errorf("busy follow must hold the newest output at the bottom, got last row %q\n%s", got, got)
	}
}

func TestModel_liveFollowPersistsThroughResize(t *testing.T) {
	t.Parallel()
	m := busyStreamingModel(t)
	if !m.tx.busy {
		t.Fatalf("test model must be mid-run (busy)")
	}
	for _, h := range []int{6, 12, 14, 10} {
		m = resizeTo(t, m, 80, h)
		got, _, vh := followRendered(m)
		if vh <= 1 {
			continue
		}
		if row := newestNonBlank(got); row != "⠋ Answering\n" {
			t.Errorf("resize to height %d lost the newest output (follow should hold the bottom row %q)\n%s", h, row, got)
		}
	}
}

func TestModel_liveFollowTracksAppends(t *testing.T) {
	t.Parallel()
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 80, 12)
	got, histContent, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("test needs a positive viewport height, got %d", vh)
	}
	if n := lineCount(histContent); n <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", n, vh)
	}
	if row := newestNonBlank(got); !strings.Contains(row, "qe") {
		t.Errorf("idle follow must hold the newest committed answer at the bottom, got last row %q\n%s", row, got)
	}
}

func TestModel_followViewportPersisted(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "answer"}, nil
		},
	})
	if m.tx.histViewport.Width() != 0 || m.tx.histViewport.Height() != 0 {
		t.Errorf("fresh viewport should start unsized until the first resize, got %dx%d", m.tx.histViewport.Width(), m.tx.histViewport.Height())
	}
}
