package tui

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// This file covers ADR-0006 decision 6's follow seam (issue #108, T04): the
// history viewport is a persisted bubbletea/viewport component whose scroll
// position + follow behaviour keep the newest output in view while a stream or
// tool run is live, survive a resize mid-stream, and add no paging/scroll UI
// (native terminal scroll is the navigation path).
//
// The follow seam is asserted at two seams: (1) the public render seam
// (renderHistoryViewport / renderPane) must hold the newest output at the
// bottom while busy, byte-matching the classically-correct bottom-anchored
// slice; and (2) the persisted viewport component (histViewport) owns the
// scroll position and follow decision.

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
	// stay in view while busy. Distinct tokens per delta let a test track which
	// (newest) line is at the bottom despite markdown soft-wrapping.
	for i := 0; i < 6; i++ {
		m = applyDelta(t, m, " tok"+string(rune('a'+i)))
	}
	return m
}

// followRendered returns the visible scroll-region output via the persisted
// viewport seam (renderHistoryViewport) and the h/vh it used, for asserting
// follow-to-bottom byte-equality with the classically-correct slice.
func followRendered(m Model) (got string, histContent string, reserved, vh int) {
	histContent = m.historyContent()
	reserved = m.bandHeight()
	vh = m.height - reserved
	return m.renderHistoryViewport(histContent, reserved), histContent, reserved, vh
}

// TestModel_liveFollowKeepsNewestOutput asserts the history viewport stays at
// the newest output while a stream/tool run is live (issue #108 AC1): the run
// is busy and its answer overflows the viewport, yet the visible history is the
// bottom-anchored newest slice — the viewport follows, it does not stare at a
// stale head — and the newest content line is at the very bottom of the view.
func TestModel_liveFollowKeepsNewestOutput(t *testing.T) {
	m := busyStreamingModel(t)
	if !m.busy {
		t.Fatalf("test model must be mid-run (busy)")
	}
	got, histContent, _, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("expected a positive viewport height, got %d", vh)
	}
	// The answer must genuinely overflow the viewport, else follow is vacuous.
	if n := len(histLines(histContent)); n <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", n, vh)
	}
	// During busy the viewport byte-matches the bottom-anchored newest slice,
	// proving follow-to-bottom: the newest output (not a stale head) is shown.
	if want := visibleHistory(histContent, vh); got != want {
		t.Errorf("busy follow != bottom-anchored newest slice\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModel_liveFollowPersistsThroughResize asserts a resize mid-stream keeps
// the newest output in view (issue #108 AC2): while busy, re-sizing the window
// (grow then shrink) must re-anchor the viewport to the newest streamed output
// rather than leaving it staring at a stale head.
func TestModel_liveFollowPersistsThroughResize(t *testing.T) {
	m := busyStreamingModel(t)
	if !m.busy {
		t.Fatalf("test model must be mid-run (busy)")
	}
	for _, h := range []int{6, 12, 8} {
		m = resizeTo(t, m, 80, h)
		got, histContent, _, vh := followRendered(m)
		if want := visibleHistory(histContent, vh); got != want {
			t.Errorf("resize to height %d lost the newest output (follow should hold the bottom slice)\n--- got ---\n%s\n--- want ---\n%s", h, got, want)
		}
	}
}

// TestModel_liveFollowTracksAppends asserts the follow seam re-anchors the
// viewport to the newest output after a committed (idle) append, not just while
// the run is streaming (issue #108 AC1 "while live", AC2 through resize): the
// persisted viewport is the scroll-state owner and GotoBottoms after content is
// appended so new output never leaves the user staring at a stale head.
func TestModel_liveFollowTracksAppends(t *testing.T) {
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 80, 8)
	// A long transcript guarantees an overflowed viewport.
	got, histContent, _, vh := followRendered(m)
	if n := len(histLines(histContent)); n <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", n, vh)
	}
	// The visible viewport is the bottom-anchored newest slice, holding the
	// newest committed output at the bottom even when idle.
	if want := visibleHistory(histContent, vh); got != want {
		t.Errorf("idle follow != bottom-anchored newest slice\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestModel_followNoScrollUINavigates asserts issue #108 AC3: no paging/scroll
// UI is added — the model's Update never routes scroll/paging keys into the
// persisted viewport, so native terminal scroll remains the only navigation
// path (ADR-0006 decision 6). Feeding scroll keys while idle must leave the
// viewport's offset untouched.
func TestModel_followNoScrollUINavigates(t *testing.T) {
	m := busyStreamingModel(t)
	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "final answer"})
	m = asModel(t, nm)
	vp := m.histViewport
	if vp == nil {
		t.Fatalf("persisted viewport must be present")
	}
	// Paging/scroll keys relayed to the TUI must not move the viewport: native
	// scroll is the navigation path, so these keys are inert for the follow UI.
	for _, k := range []tea.KeyType{tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown, tea.KeyEnd, tea.KeyHome} {
		before := vp.YOffset
		m = mustUpdate(t, m, tea.KeyMsg{Type: k})
		if vp.YOffset != before {
			t.Errorf("scroll/paging key %v moved the persisted viewport (no-scroll-UI violated): %d -> %d", k, before, vp.YOffset)
		}
	}
}

// TestModel_followViewportPersisted asserts the history viewport is backed by a
// persisted bubbletea/viewport component carrying real scroll state (ADR-0006
// decision 6's T04 seam), not the stateless always-bottom string slice it
// replaces.
func TestModel_followViewportPersisted(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "answer"}, nil
		},
	})
	if m.histViewport == nil {
		t.Fatalf("model must own a persisted viewport component")
	}
	if m.histViewport.Width != 0 || m.histViewport.Height != 0 {
		t.Errorf("fresh viewport should start unsized until the first resize, got %dx%d", m.histViewport.Width, m.histViewport.Height)
	}
}
