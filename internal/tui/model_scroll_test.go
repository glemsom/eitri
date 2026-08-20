package tui

import (
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

func mustVpLines(m Model) int  { return m.tx.histViewport.TotalLineCount() }
func mustVpHeight(m Model) int { return m.tx.histViewport.Height() }

func scrollOffset(m Model) int {
	return m.tx.histViewport.YOffset()
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
	if !m.tx.histViewport.AtBottom() {
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
	if !m.tx.histViewport.AtBottom() {
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
