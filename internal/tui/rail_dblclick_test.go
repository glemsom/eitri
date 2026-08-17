package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// This file covers the rail double-click reset seam: two clean clicks on the
// rail border inside the double-click window snap the rail back to its default
// width, so a dragged-too-far rail is one gesture away from home. Single-click
// drag-resize, history drag-select, and stale clicks far apart are untouched.

// railDblModel builds a sized model with a telemetry strip and a wired rail,
// plus one committed turn so the history viewport is hydrated — identical to
// railDragModel but with a controllable clock for double-click timing.
func railDblModel(t *testing.T) Model {
	t.Helper()
	m := railDragModel(t)
	m.now = func() time.Time { return fakeNow }
	return m
}

// fakeNow is the fixed clock railDblModel injects. Tests advance it by window
// increments to simulate the passage of time between clicks.
var fakeNow = time.Unix(1_700_000_000, 0)

func setNow(m Model, d time.Duration) Model {
	m.now = func() time.Time { return fakeNow.Add(d) }
	return m
}

// TestRailDbl_fastSecondPressResetsToDefault asserts the core seam: a press on
// the rail border, release, then a second press within the double-click window
// snaps the rail back to the default width. The reset runs on the second PRESS
// because that is when the gesture is recognized; motion and release after it
// behave like a fresh no-op drag.
func TestRailDbl_fastSecondPressResetsToDefault(t *testing.T) {
	m := railDblModel(t)
	m = railDragPressMotion(t, m, 40) // drag the rail well past default
	if got := m.tx.railWidthOrDefault(); got == defaultRailWidth {
		t.Fatalf("precondition: rail must be dragged off default, got %d", got)
	}

	border := m.tx.railBorderColumn()
	row := 0

	// First clean click: press + release, no motion.
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))

	// Second press inside the window: resets to the default width.
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth {
		t.Fatalf("double-click must reset rail to default width, got %d, want %d", got, defaultRailWidth)
	}
}

// TestRailDbl_singleDragClickDoesNotReset asserts a border drag (motion between
// press and release) is not a clean click and must never count toward a
// double-click reset: the drag keeps its dragged width and no reset state is
// armed.
func TestRailDbl_singleDragClickDoesNotReset(t *testing.T) {
	m := railDblModel(t)

	border := m.tx.railBorderColumn()
	row := 0

	// Drag gesture (motion present): resizes, releases with the width kept.
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("motion", border+10, row))
	m = mustUpdate(t, m, railDragMsg("release", border+10, row))
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth+10 {
		t.Fatalf("precondition: drag must keep width %d, got %d", defaultRailWidth+10, got)
	}

	// A second clean click (or drag) right after must NOT reset: no arm is set
	// because the first gesture had motion, so the width stays.
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth+10 {
		t.Errorf("drag-then-click must not reset: width %d, want %d", got, defaultRailWidth+10)
	}
}

// TestRailDbl_staleFirstClickExpires asserts the double-click window: a clean
// border click arms the reset, but the arm expires once the window elapses, so
// a later press is a fresh single click that starts a drag instead of resetting
// (reset must not fire on clicks that are not a double-click).
func TestRailDbl_staleFirstClickExpires(t *testing.T) {
	m := railDblModel(t)
	m = railDragPressMotion(t, m, 40) // drag off default so a reset would be visible
	if got := m.tx.railWidthOrDefault(); got == defaultRailWidth {
		t.Fatalf("precondition: rail must be dragged off default, got %d", got)
	}

	border := m.tx.railBorderColumn()
	row := 0

	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))

	// Let the window elapse, then press again: no reset, and the press starts a
	// drag (railDrag set) that a motion then applies.
	m = setNow(m, doubleClickWindow+time.Millisecond)
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	if got := m.tx.railWidthOrDefault(); got == defaultRailWidth {
		t.Fatal("expired click must not reset the rail")
	}
	if m.railDrag == nil {
		t.Fatal("press after the window must start a normal rail drag")
	}
	// Drag left from the dragged width (clamped at the terminal cap of
	// terminalWidth/2): a fresh drag anchored at the current width, proving
	// the expired click did not reset to default.
	from := m.tx.railWidthOrDefault()
	m = mustUpdate(t, m, railDragMsg("motion", border-5, row))
	if got := m.tx.railWidthOrDefault(); got != from-5 {
		t.Errorf("motion after expired click must drag from current width: got %d, want %d", got, from-5)
	}
}

// TestRailDbl_secondClickWithoutReleaseStartsDrag asserts the reset fires on
// the second press, and the press after a reset still starts a normal rail
// drag (the gesture continues as a drag if the user holds and moves) — the
// reset must not leave the pointer dead.
func TestRailDbl_secondClickWithoutReleaseStartsDrag(t *testing.T) {
	m := railDblModel(t)
	m = railDragPressMotion(t, m, 30)

	border := m.tx.railBorderColumn()
	row := 0

	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))

	m = mustUpdate(t, m, railDragMsg("press", border, row)) // reset fires here
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth {
		t.Fatalf("reset must fire on second press, got width %d", got)
	}
	if m.railDrag == nil {
		t.Fatal("second press must still start a rail drag")
	}
	// Holding and dragging after the reset resizes from the default width.
	m = mustUpdate(t, m, railDragMsg("motion", border+8, row))
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth+8 {
		t.Errorf("drag after reset must resize from default: width %d, want %d", got, defaultRailWidth+8)
	}
}

// TestRailDbl_thirdPressAfterResetStartsFreshPair asserts the double-click
// reset consumes the window: a third border press right after a reset (even
// one that ends in a clean click) must not re-reset, because the reset cleared
// the armed window and the new press starts a fresh pair.
func TestRailDbl_thirdPressAfterResetStartsFreshPair(t *testing.T) {
	m := railDblModel(t)
	m = railDragPressMotion(t, m, 40) // drag off default so a reset is visible

	border := m.tx.railBorderColumn()
	row := 0

	// Double-click: press+release, then press (reset fires).
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth {
		t.Fatalf("double-click must reset, got %d", got)
	}

	// Release the reset press, then a third press inside the old window: it
	// must NOT reset again (it is the first press of a fresh pair) and must
	// start a normal drag from the default width.
	m = mustUpdate(t, m, railDragMsg("release", border, row))
	border = m.tx.railBorderColumn() // reset moved the border back to default
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth {
		t.Errorf("third press must not re-reset: width %d, want %d", got, defaultRailWidth)
	}
	if m.railDrag == nil {
		t.Fatal("third press must start a normal rail drag")
	}
}

// TestRailDbl_interveningOffBorderPressDisarms asserts an unrelated press
// between the two border clicks disarms the double-click window: after a clean
// border click, a press on the history content (drag-select) followed by a
// border press must NOT reset — the intervening gesture proves the two border
// clicks were not a double-click.
func TestRailDbl_interveningOffBorderPressDisarms(t *testing.T) {
	m := railDblModel(t)
	m = railDragPressMotion(t, m, 40) // drag off default so a reset is visible

	border := m.tx.railBorderColumn()
	row := 0

	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))

	// A press on the history content (far from the border) is an unrelated
	// gesture: it must disarm the armed window.
	m = mustUpdate(t, m, railDragMsg("press", border-10, row))
	m = mustUpdate(t, m, railDragMsg("release", border-10, row))

	// A border press right after: must NOT reset (disarmed), and must start a
	// normal drag.
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	if got := m.tx.railWidthOrDefault(); got == defaultRailWidth {
		t.Fatal("intervening off-border press must disarm the double-click window")
	}
	if m.railDrag == nil {
		t.Fatal("border press after disarm must start a normal rail drag")
	}
}

// TestRailDbl_resetKeepsScrollAndFollow asserts the reset preserves the
// transcript reading state: after scrolling to a middle offset with follow
// broken, the double-click reset re-wraps (layout dirty) but keeps the offset
// and follow state, and End still reaches the bottom.
func TestRailDbl_resetKeepsScrollAndFollow(t *testing.T) {
	m := newTallHistoryModel(t)
	m.tx.rail = NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m.tx.railWidth = defaultRailWidth + 25
	m.now = func() time.Time { return fakeNow }
	m = resizeTo(t, m, 120, 12)

	_, histContent, vh := followRendered(m)
	if lineCount(histContent) <= vh {
		t.Fatalf("test must overflow: history (%d lines) should exceed viewport height (%d)", lineCount(histContent), vh)
	}

	m = mustUpdate(t, m, wheelMsg(true))
	before := scrollOffset(m)
	if before <= 0 || m.tx.histFollow {
		t.Fatalf("precondition: need a broken-follow middle offset, got offset %d follow %v", before, m.tx.histFollow)
	}

	border := m.tx.railBorderColumn()
	row := 0
	m = mustUpdate(t, m, railDragMsg("press", border, row))
	m = mustUpdate(t, m, railDragMsg("release", border, row))
	m = mustUpdate(t, m, railDragMsg("press", border, row)) // reset

	if got := m.tx.railWidthOrDefault(); got != defaultRailWidth {
		t.Fatalf("reset must restore default width, got %d", got)
	}
	if !m.tx.layout.dirty {
		t.Error("reset must mark the layout dirty (transcript re-wrap)")
	}
	if after := scrollOffset(m); after != before {
		t.Errorf("reset must keep the reading offset: got %d, want %d", after, before)
	}
	if m.tx.histFollow {
		t.Error("reset must not re-engage follow")
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.tx.histViewport.AtBottom() {
		t.Errorf("End after reset must reach the bottom, got offset %d", scrollOffset(m))
	}
	if !m.tx.histFollow {
		t.Error("reaching the bottom after reset must re-engage follow")
	}
}
