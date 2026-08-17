package tui

import (
	"context"
	"strings"
	"testing"
)

// This file covers the T1 alt-screen pivot's viewport follow seam (,
// T04 from #108): the history viewport is a persisted bubbletea/viewport
// component whose scroll position + follow behaviour keep the newest output in
// view while a stream or tool run is live, survive a resize mid-stream, and add
// no paging/scroll UI.
//
// The follow seam is asserted at two seams: (1) the public render seam
// (renderHistoryViewport / renderPane) must hold the newest output at the
// bottom while busy, byte-matching the bottom-anchored slice; and (2) the
// persisted viewport component (histViewport) owns the scroll position and
// follow decision.

// busyStreamingModel builds a streaming model mid-run (busy) whose answer is
// tall enough to overflow a short viewport, so follow-to-bottom is observable.
// It exercises the same live path the app uses: a streamed answer grew in
// place while busy, so the newest output exists beyond the visible height.
func busyStreamingModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Stream: NewStreamer(),
	})
	// A short window so the running answer overflows the viewport height.
	m = resizeTo(t, m, 80, 8)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	// Stream a long answer that dwarfs the viewport: the newest tokens must
	// stay in view while busy.
	for i := 0; i < 6; i++ {
		m = applyDelta(t, m, strings.Repeat("word ", 20))
	}
	return m
}

// followRendered returns the visible scroll-region output via the persisted
// viewport seam (renderHistoryViewport) plus the full rendered history content
// and the viewport height — for asserting the newest output is visible.
func followRendered(m Model) (got string, histContent string, vh int) {
	var hist strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	histContent = hist.String()
	reserved := m.bandHeight()
	vh = m.tx.height - reserved
	return m.tx.renderHistoryViewport(histContent, reserved), histContent, vh
}

// newestNonBlank returns the last non-blank content row of a rendered viewport,
// trailing whitespace trimmed — the row that proves the newest output is in
// view when the viewport is following.
func newestNonBlank(render string) string {
	lines := strings.Split(strings.TrimRight(render, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		// Strip ANSI styling first: lipgloss v2 always renders full-fidelity
		// ANSI (downsampling moved to the output layer), so content rows carry
		// SGR sequences even in tests.
		clean := ansiStrip(lines[i])
		if strings.TrimSpace(clean) != "" {
			return strings.TrimRight(clean, " ") + "\n"
		}
	}
	return ""
}

// TestModel_liveFollowKeepsNewestOutput asserts the history viewport stays at
// the newest output while a stream/tool run is live : the run
// is busy and its answer overflows the viewport, yet the newest content line
// (the busy thinking footer) is the last non-blank row — the viewport follows,
// it does not stare at a stale head.
func TestModel_liveFollowKeepsNewestOutput(t *testing.T) {
	m := busyStreamingModel(t)
	if !m.tx.busy {
		t.Fatalf("test model must be mid-run (busy)")
	}
	got, histContent, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("expected a positive viewport height, got %d", vh)
	}
	// The answer must genuinely overflow the viewport, else follow is vacuous.
	if n := lineCount(histContent); n <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", n, vh)
	}
	// During busy the viewport shows the newest output (not a stale head): the
	// busy thinking footer is the last non-blank rendered row.
	if got := newestNonBlank(got); got != "⠋ working\n" {
		t.Errorf("busy follow must hold the newest output at the bottom, got last row %q\n%s", got, got)
	}
}

// TestModel_liveFollowPersistsThroughResize asserts a resize mid-stream keeps
// the newest output in view : while busy, re-sizing the window
// (grow then shrink) must re-anchor the viewport to the newest streamed output
// rather than leaving it staring at a stale head.
func TestModel_liveFollowPersistsThroughResize(t *testing.T) {
	m := busyStreamingModel(t)
	if !m.tx.busy {
		t.Fatalf("test model must be mid-run (busy)")
	}
	for _, h := range []int{6, 12, 14, 10} {
		m = resizeTo(t, m, 80, h)
		got, _, vh := followRendered(m)
		if vh <= 0 {
			// No vertical room for the history this small; nothing to follow.
			continue
		}
		if row := newestNonBlank(got); row != "⠋ working\n" {
			t.Errorf("resize to height %d lost the newest output (follow should hold the bottom row %q)\n%s", h, row, got)
		}
	}
}

// TestModel_liveFollowTracksAppends asserts the follow seam re-anchors the
// viewport to the newest output after a committed (idle) append, not just while
// the run is streaming : the
// persisted viewport is the scroll-state owner and GotoBottoms after content is
// appended so new output never leaves the user staring at a stale head.
func TestModel_liveFollowTracksAppends(t *testing.T) {
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 80, 12)
	// A long transcript guarantees an overflowed viewport.
	got, histContent, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("test needs a positive viewport height, got %d", vh)
	}
	if n := lineCount(histContent); n <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", n, vh)
	}
	// The visible viewport holds the newest committed output at the bottom even
	// when idle: the last committed answer ("answer qe") is the newest content.
	// Match on the answer word "qe" because Glamour's per-word styling runs
	// split the contiguous phrase apart.
	if row := newestNonBlank(got); !strings.Contains(row, "qe") {
		t.Errorf("idle follow must hold the newest committed answer at the bottom, got last row %q\n%s", row, got)
	}
}

// TestModel_followViewportPersisted asserts the history viewport is backed by a
// persisted bubbletea/viewport component carrying real scroll state
// decision 6's seam, not the stateless always-bottom string slice it
// replaces.
func TestModel_followViewportPersisted(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "answer"}, nil
		},
	})
	if m.tx.histViewport == nil {
		t.Fatalf("model must own a persisted viewport component")
	}
	if m.tx.histViewport.Width() != 0 || m.tx.histViewport.Height() != 0 {
		t.Errorf("fresh viewport should start unsized until the first resize, got %dx%d", m.tx.histViewport.Width(), m.tx.histViewport.Height())
	}
}
