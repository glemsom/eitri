package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func scrollOverflowModel(t *testing.T) Model {
	t.Helper()
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 120, 12)
	got, _, _ := followRendered(m)
	if vp := m.tx.histViewport; vp.TotalLineCount() <= vp.Height() {
		t.Fatalf("test must overflow: viewport lines (%d) should exceed height (%d), got render:\n%s", mustVpLines(m), mustVpHeight(m), got)
	}
	return m
}

func mustVpLines(m Model) int  { m.tx.ensureViewportSynced(); return m.tx.histViewport.TotalLineCount() }
func mustVpHeight(m Model) int { return m.tx.histViewport.Height() }

func scrollOffset(m Model) int {
	m.tx.ensureViewportSynced()
	return m.tx.histViewport.YOffset()
}

// atBottom answers whether the transcript is scrolled to the newest content,
// forcing the persisted viewport in sync first: the busy+follow fast path
// (renderHistoryViewport) renders the bottom directly without touching
// histViewport's own YOffset/lines, so a bare AtBottom() read taken right
// after a render (with no intervening scroll/click action, which would have
// synced it as a side effect) can see stale pre-burst state.
func atBottom(m Model) bool {
	m.tx.ensureViewportSynced()
	return m.tx.histViewport.AtBottom()
}

func TestScroll_pagingKeysNavigateTranscript(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	start := scrollOffset(m)
	if start <= 0 {
		t.Fatalf("overflowed follow should start scrolled to the bottom, got offset %d", start)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	up := scrollOffset(m)
	if up >= start {
		t.Errorf("PgUp must move the transcript up: offset %d -> %d", start, up)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if top := scrollOffset(m); top != 0 {
		t.Errorf("Home should jump to the transcript top, got offset %d", top)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	down := scrollOffset(m)
	if down <= 0 {
		t.Errorf("PgDn must move the transcript down from the top, got offset %d", down)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !atBottom(m) {
		t.Errorf("End should jump to the transcript bottom, got offset %d", scrollOffset(m))
	}
	if !m.tx.histFollow {
		t.Errorf("End reaching the bottom should re-engage follow, got histFollow=false")
	}
}

func TestScroll_scrollUpBreaksFollow(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	start := scrollOffset(m)

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.tx.histFollow {
		t.Errorf("scrolling up must break follow (histFollow should be false)")
	}
	up := scrollOffset(m)
	if up >= start {
		t.Fatalf("PgUp should have moved up, offset %d -> %d", start, up)
	}
	followRendered(m)
	if got := scrollOffset(m); got != up {
		t.Errorf("scroll-up must hold the reading offset across a re-render, got %d, want %d", got, up)
	}
}

func wheelMsg(up bool) tea.Msg {
	return wheelMsgAt(up, 0)
}

func wheelMsgAt(up bool, y int) tea.Msg {
	btn := tea.MouseWheelDown
	if up {
		btn = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{Button: btn, Y: y, X: 2}
}

func TestScroll_mouseWheelNavigatesTranscript(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	start := scrollOffset(m)

	m = mustUpdate(t, m, wheelMsg(true))
	if up := scrollOffset(m); up >= start {
		t.Errorf("wheel up must scroll up, offset %d -> %d", start, up)
	}
	if m.tx.histFollow {
		t.Errorf("wheel up must break follow, got histFollow=true")
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	for i := 0; i < 50 && !atBottom(m); i++ {
		m = mustUpdate(t, m, wheelMsg(false))
	}
	if !atBottom(m) {
		t.Fatalf("wheel down should reach the bottom, got offset %d", scrollOffset(m))
	}
	if !m.tx.histFollow {
		t.Errorf("reaching the bottom by wheel down should re-engage follow, got histFollow=false")
	}
}

func TestScroll_mouseWheelMovesHalfOfPreviousStep(t *testing.T) {
	t.Parallel()
	tx := Transcript{
		histFollow: true,
		width:      80,
		height:     80,
	}
	tx.histViewport.SetHeight(40)
	tx.histViewport.SetWidth(80)
	tx.histViewport.SetContent(strings.Repeat("row\n", 200))
	tx.histViewport.GotoBottom()
	start := tx.histViewport.YOffset()
	want := tx.histViewport.Height() / 40
	if want < 1 {
		want = 1
	}

	tx.navigateMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if got := start - tx.histViewport.YOffset(); got != want {
		t.Fatalf("one wheel tick moved %d rows, want %d", got, want)
	}
}

func TestScroll_pgKeysMoveByHalfPage(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	half := mustVpHeight(m) / 2
	if half < 1 {
		half = 1
	}

	bottom := scrollOffset(m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if got := bottom - scrollOffset(m); got != half {
		t.Errorf("PgUp moved %d rows, want exactly half the visible transcript (%d rows)", got, half)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if got := scrollOffset(m); got != half {
		t.Errorf("PgDn from the top moved to offset %d, want exactly half the visible transcript (%d rows)", got, half)
	}
}

func TestScroll_mouseWheelRoutesThroughRegionSeam(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	regionHeight := m.tx.scrollRegionHeight(m.bandHeight())
	if regionHeight <= 0 {
		t.Fatalf("precondition: region must have height, got %d", regionHeight)
	}

	start := scrollOffset(m)
	m = mustUpdate(t, m, wheelMsgAt(true, 0))
	if up := scrollOffset(m); up >= start {
		t.Errorf("wheel up over the history region must scroll, offset %d -> %d", start, up)
	}

	offsetBefore := scrollOffset(m)
	followBefore := m.tx.histFollow
	m = mustUpdate(t, m, wheelMsgAt(true, regionHeight))
	if scrollOffset(m) != offsetBefore {
		t.Errorf("wheel over the band (row %d) must not scroll the history, offset changed %d -> %d", regionHeight, offsetBefore, scrollOffset(m))
	}
	if m.tx.histFollow != followBefore {
		t.Errorf("wheel over the band must not change follow, got %v -> %v", followBefore, m.tx.histFollow)
	}

	m = mustUpdate(t, m, wheelMsgAt(true, m.tx.height))
	if scrollOffset(m) != offsetBefore {
		t.Errorf("wheel past the terminal bottom must not scroll, offset changed to %d", scrollOffset(m))
	}
}

func TestScroll_newSubmitRefollowsNewest(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.tx.histFollow {
		t.Fatalf("precondition: PgUp must break follow")
	}

	m = typeText(t, m, "new turn")
	m = submitAndWait(t, m)
	if !m.tx.histFollow {
		t.Errorf("a new submit must re-engage follow, got histFollow=false")
	}
	got, _, _ := followRendered(m)
	if !atBottom(m) {
		t.Errorf("a new submit should re-follow to the newest output, got offset %d (not at bottom)", scrollOffset(m))
	}
	if row := newestNonBlank(got); !strings.Contains(row, "new") {
		t.Errorf("a new submit should re-follow the newest answer, got last row %q\n%s", row, got)
	}
}

func TestScroll_navigationDoesNotStealComposerFocus(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t)
	m = typeText(t, m, "typed")
	if got := m.composer.Value(); got != "typed" {
		t.Errorf("composer must keep focus for typing after scroll setup, got %q", got)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	if got := m.composer.Value(); got != "typed" {
		t.Errorf("paging must not touch the composer value, got %q", got)
	}
	before := m.composer.LineInfo().ColumnOffset
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if after := m.composer.LineInfo().ColumnOffset; after >= before {
		t.Errorf("left arrow should move the composer cursor left, got col %d -> %d", before, after)
	}
}

// TestScroll_viewDeclaresMouseCellMotion locks the terminal-facing seam that
// actually delivers wheel scroll and drag-select: the model's View must declare
// cell-motion mode so bubbletea v2 turns on SGR mouse reporting and routes wheel
// events into navigateMouse. Dropping it (as the rail-drag removal did, issue
// #334) disables mouse input entirely even though the wheel handlers still exist
// — unit tests constructing MouseWheelMsg directly bypass the terminal and pass
// either way, so this regression needs a View-level assertion.
func TestScroll_viewDeclaresMouseCellMotion(t *testing.T) {
	t.Parallel()
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{}, nil
	})
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("View().MouseMode = %v, want %v (mouse reporting must be enabled for wheel scroll)", got, tea.MouseModeCellMotion)
	}
}
