package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// This file covers the T2 scroll-navigation seam: the history
// viewport is user-navigable (mouse wheel + PgUp/PgDn/Home/End), scrolling up
// breaks the follow position so reading stays put, and a new submit re-follows
// the newest output. Navigation drives the persisted bubbletea/viewport seam
// (renderHistoryViewport / renderPane) without stealing composer input focus.

// scrollOverflowModel builds a model with a long committed history that overflows
// a short viewport, then renders once so the persisted viewport is hydrated with
// content, so keyboard/mouse navigation can move it.
func scrollOverflowModel(t *testing.T) Model {
	t.Helper()
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 120, 12)
	// Hydrate the persisted viewport with the current content so navigation has
	// a real scroll range.
	got, _, _ := followRendered(m)
	if vp := m.tx.histViewport; vp.TotalLineCount() <= vp.Height() {
		t.Fatalf("test must overflow: viewport lines (%d) should exceed height (%d), got render:\n%s", mustVpLines(m), mustVpHeight(m), got)
	}
	return m
}

func mustVpLines(m Model) int  { return m.tx.histViewport.TotalLineCount() }
func mustVpHeight(m Model) int { return m.tx.histViewport.Height() }

// scrollOffset returns the persisted viewport's current Y offset.
func scrollOffset(m Model) int {
	return m.tx.histViewport.YOffset()
}

// TestScroll_pagingKeysNavigateTranscript asserts PgUp/PgDn move the transcript
// by a viewport's worth of lines, and Home/End jump to the top/bottom — the
// keyed navigation seam .
func TestScroll_pagingKeysNavigateTranscript(t *testing.T) {
	m := scrollOverflowModel(t)
	// Fresh model follows the newest output: starting offset is at the bottom.
	start := scrollOffset(m)
	if start <= 0 {
		t.Fatalf("overflowed follow should start scrolled to the bottom, got offset %d", start)
	}

	// PgUp moves up by (roughly) a viewport height.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	up := scrollOffset(m)
	if up >= start {
		t.Errorf("PgUp must move the transcript up: offset %d -> %d", start, up)
	}

	// Home jumps straight to the top.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if top := scrollOffset(m); top != 0 {
		t.Errorf("Home should jump to the transcript top, got offset %d", top)
	}

	// PgDn moves back down by (roughly) a viewport height.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	down := scrollOffset(m)
	if down <= 0 {
		t.Errorf("PgDn must move the transcript down from the top, got offset %d", down)
	}

	// End jumps to the newest output (the bottom).
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.tx.histViewport.AtBottom() {
		t.Errorf("End should jump to the transcript bottom, got offset %d", scrollOffset(m))
	}
	if !m.tx.histFollow {
		t.Errorf("End reaching the bottom should re-engage follow, got histFollow=false")
	}
}

// TestScroll_scrollUpBreaksFollow asserts scrolling up breaks the follow
// position: after a PgUp/Home the persisted viewport stops re-anchoring to the
// newest output, so a re-render holds the earlier reading offset instead of
// being yanked back down to the newest .
func TestScroll_scrollUpBreaksFollow(t *testing.T) {
	m := scrollOverflowModel(t)
	start := scrollOffset(m)

	// Scroll up and confirm follow broke.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.tx.histFollow {
		t.Errorf("scrolling up must break follow (histFollow should be false)")
	}
	up := scrollOffset(m)
	if up >= start {
		t.Fatalf("PgUp should have moved up, offset %d -> %d", start, up)
	}
	// Re-render through the viewport seam: follow is broken, so the offset must
	// stay at the earlier reading position (not re-anchor to the newest).
	followRendered(m)
	if got := scrollOffset(m); got != up {
		t.Errorf("scroll-up must hold the reading offset across a re-render, got %d, want %d", got, up)
	}
}

// wheelMsg builds a mouse-wheel scroll event so the T2 wheel seam can be
// driven through the model's Update . bubbletea v2 delivers
// the wheel as its own tea.MouseWheelMsg (pass 2, ).
func wheelMsg(up bool) tea.Msg {
	return wheelMsgAt(up, 0)
}

// wheelMsgAt builds a mouse-wheel event with an explicit Y row so a test can
// place the pointer over a specific screen row (inside the history region or
// over the fixed bottom band) and assert the wheel only scrolls the history
// when it is over the region.
func wheelMsgAt(up bool, y int) tea.Msg {
	btn := tea.MouseWheelDown
	if up {
		btn = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{Button: btn, Y: y, X: 2}
}

// TestScroll_mouseWheelNavigatesTranscript asserts the mouse wheel scrolls the
// transcript and breaks follow when scrolling up: wheel-up
// moves the viewport toward older output and stops re-anchoring to the newest;
// wheel-down reaches the bottom re-engages follow.
func TestScroll_mouseWheelNavigatesTranscript(t *testing.T) {
	m := scrollOverflowModel(t)
	start := scrollOffset(m)

	// Wheel up scrolls toward older output and breaks follow.
	m = mustUpdate(t, m, wheelMsg(true))
	if up := scrollOffset(m); up >= start {
		t.Errorf("wheel up must scroll up, offset %d -> %d", start, up)
	}
	if m.tx.histFollow {
		t.Errorf("wheel up must break follow, got histFollow=true")
	}

	// Wheel down scrolls back toward the newest; reaching the bottom re-engages
	// follow. Repeatedly scroll down to the bottom from the top to guarantee it.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	for i := 0; i < 50 && !m.tx.histViewport.AtBottom(); i++ {
		m = mustUpdate(t, m, wheelMsg(false))
	}
	if !m.tx.histViewport.AtBottom() {
		t.Fatalf("wheel down should reach the bottom, got offset %d", scrollOffset(m))
	}
	if !m.tx.histFollow {
		t.Errorf("reaching the bottom by wheel down should re-engage follow, got histFollow=false")
	}
}

// TestScroll_mouseWheelRoutesThroughRegionSeam asserts the wheel scrolls only
// when the pointer is over the history scroll region, as decided by the
// Transcript's scroll-region hit-test seam: a wheel event over a visible region
// row scrolls the viewport, while a wheel event over the fixed bottom band's
// rows leaves the viewport untouched. The input side reads the same region the
// render pass laid out through the seam instead of re-deriving it from Model
// width math, so a wheeled row and a dragged row can never disagree about where
// the region ends.
func TestScroll_mouseWheelRoutesThroughRegionSeam(t *testing.T) {
	m := scrollOverflowModel(t)
	regionHeight := m.tx.scrollRegionHeight(m.bandHeight())
	if regionHeight <= 0 {
		t.Fatalf("precondition: region must have height, got %d", regionHeight)
	}

	// A wheel-up over a visible region row (row 0) scrolls toward older output
	// and breaks follow.
	start := scrollOffset(m)
	m = mustUpdate(t, m, wheelMsgAt(true, 0))
	if up := scrollOffset(m); up >= start {
		t.Errorf("wheel up over the history region must scroll, offset %d -> %d", start, up)
	}

	// A wheel-up over the band's first row (just past the region) must not
	// scroll at all: the pointer is outside the history region, so the wheel
	// leaves the viewport (and follow) untouched.
	offsetBefore := scrollOffset(m)
	followBefore := m.tx.histFollow
	m = mustUpdate(t, m, wheelMsgAt(true, regionHeight))
	if scrollOffset(m) != offsetBefore {
		t.Errorf("wheel over the band (row %d) must not scroll the history, offset changed %d -> %d", regionHeight, offsetBefore, scrollOffset(m))
	}
	if m.tx.histFollow != followBefore {
		t.Errorf("wheel over the band must not change follow, got %v -> %v", followBefore, m.tx.histFollow)
	}

	// A wheel-up one row past the terminal's bottom edge is also outside the
	// region and must not scroll.
	m = mustUpdate(t, m, wheelMsgAt(true, m.tx.height))
	if scrollOffset(m) != offsetBefore {
		t.Errorf("wheel past the terminal bottom must not scroll, offset changed to %d", scrollOffset(m))
	}
}

// TestScroll_newSubmitRefollowsNewest asserts a new submitted turn re-engages
// the follow position:
// after scrolling up, submitting a new prompt returns the viewport to the newest
// output instead of holding the stale reading offset.
func TestScroll_newSubmitRefollowsNewest(t *testing.T) {
	m := scrollOverflowModel(t)
	// Scroll up and confirm follow is broken.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.tx.histFollow {
		t.Fatalf("precondition: PgUp must break follow")
	}

	// Type a new prompt and submit a real turn.
	m = typeText(t, m, "new turn")
	m = submitAndWait(t, m)
	if !m.tx.histFollow {
		t.Errorf("a new submit must re-engage follow, got histFollow=false")
	}
	// The next render re-anchors the viewport to the newest output (bottom)
	// rather than holding the stale reading offset — the flag flips follow on
	// submit, the render performs the GotoBottom.
	got, _, _ := followRendered(m)
	if !m.tx.histViewport.AtBottom() {
		t.Errorf("a new submit should re-follow to the newest output, got offset %d (not at bottom)", scrollOffset(m))
	}
	// The newest turn's answer is the last visible content row. Glamour's
	// per-word styling runs split contiguous words, so match on the answer word.
	if row := newestNonBlank(got); !strings.Contains(row, "new") {
		t.Errorf("a new submit should re-follow the newest answer, got last row %q\n%s", row, got)
	}
}

// TestScroll_navigationDoesNotStealComposerFocus asserts T2 navigation does not
// corrupt composer input focus: arrow keys still edit the
// composer, and paging keys that navigate the transcript leave the composer's
// focused value untouched.
func TestScroll_navigationDoesNotStealComposerFocus(t *testing.T) {
	m := scrollOverflowModel(t)
	// Focus stays on the composer: typing still lands in the composer.
	m = typeText(t, m, "typed")
	if got := m.composer.Value(); got != "typed" {
		t.Errorf("composer must keep focus for typing after scroll setup, got %q", got)
	}

	// Paging the transcript leaves the composer value (and focus) untouched.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if got := m.composer.Value(); got != "typed" {
		t.Errorf("paging must not touch the composer value, got %q", got)
	}
	// Arrow keys still move the composer cursor (transcript arrow navigation
	// is intentionally not wired so composer editing keeps primacy, AC4).
	before := m.composer.LineInfo().ColumnOffset
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if after := m.composer.LineInfo().ColumnOffset; after >= before {
		t.Errorf("left arrow should move the composer cursor left, got col %d -> %d", before, after)
	}
}
