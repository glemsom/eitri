package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// This file covers the scroll-region seam (issue #391): the Transcript owns
// one helper answering "is a screen row inside the history scroll region, and
// which content line does it map to", and the render path lays out the history
// pane through the same seam — so render and hit-test read one region source
// instead of the selection side re-deriving the region from Model width math.
// One region source means drag coordinates and on-screen rows can never drift
// apart.

// scrollRegionFixture builds a standalone Transcript with a sized terminal and
// renders its history once so the persisted viewport is hydrated with the
// region height the render seam computed. The history is forced to overflow
// the region so every screen row in the region maps to a real content line.
func scrollRegionFixture(t *testing.T, height, band int) *Transcript {
	t.Helper()
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:         th,
		configTheme:   config.DefaultTheme,
		workspacePath: "/tmp/acme",
		messages: []message{
			{role: "you", content: strings.Repeat("q", 60)},
			{role: "eitri", content: strings.Repeat("first answer ", 30)},
			{role: "you", content: strings.Repeat("r", 60)},
			{role: "eitri", content: strings.Repeat("second reply ", 30)},
		},
		width:        80,
		height:       height,
		histViewport: newHistoryViewport(),
	}
	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	out := tx.renderHistoryViewport(hist.String(), band)
	if out == "" {
		t.Fatalf("fixture rendered empty history at height %d band %d", height, band)
	}
	if vp := tx.histViewport; vp.TotalLineCount() <= vp.Height() {
		t.Fatalf("fixture must overflow the region: %d lines in %d rows", vp.TotalLineCount(), vp.Height())
	}
	// Any transcript change leaves the shared layout cache dirty (appends,
	// tool updates, rail width, expand mode), so the hit-test's plain-row space
	// builds on first use — mirror production state before returning.
	tx.layout.dirty = true
	return tx
}

// TestScrollRegion_sizesViewportFromSeam asserts the render path consumes the
// scroll-region seam: renderHistoryViewport sizes the persisted viewport to
// scrollRegionHeight(band) — terminal height minus the fixed bottom band — so
// the region height, the persisted window, and the hit-test share one value.
func TestScrollRegion_sizesViewportFromSeam(t *testing.T) {
	tx := scrollRegionFixture(t, 12, 4)

	if got := tx.scrollRegionHeight(4); got != 8 {
		t.Errorf("scrollRegionHeight(band 4) = %d, want terminal 12 - band 4 = 8", got)
	}
	// The persisted viewport the hit-test reads is sized by the same seam.
	if vh := tx.histViewport.Height(); vh != 8 {
		t.Errorf("renderHistoryViewport must size the viewport to the seam's region height, got %d, want 8", vh)
	}

	// Pre-resize the region is unclamped (-1); a band that fills the terminal
	// leaves a zero-row region.
	tx.height = 0
	if got := tx.scrollRegionHeight(4); got != -1 {
		t.Errorf("pre-resize scrollRegionHeight = %d, want -1 (unclamped)", got)
	}
	tx.height = 4
	if got := tx.scrollRegionHeight(4); got != 0 {
		t.Errorf("band-fills-terminal scrollRegionHeight = %d, want 0", got)
	}
	// The rail clamps to the same single region computation, never a second copy.
	if got := tx.railClampHeight(4); got != 0 {
		t.Errorf("railClampHeight must share the region seam's 0-row budget, got %d", got)
	}
}

// TestScrollRegion_seamMapsBandEdgeRows asserts the hit-test seam: a screen
// row just above the fixed bottom band maps inside the history region to the
// content line under it; the band's first row and everything beyond map
// outside.
func TestScrollRegion_seamMapsBandEdgeRows(t *testing.T) {
	tx := scrollRegionFixture(t, 12, 4)
	// Region height = 12 - 4 = 8 rows (0..7); row 7 is the last region row and
	// row 8 is the band's first row.
	inside := tx.height - 4 - 1

	line, ok := tx.contentLineAtScreenRow(inside)
	if !ok {
		t.Fatalf("screen row %d (just above the band) must map inside the region", inside)
	}
	if want := tx.histViewport.YOffset() + inside; line != want {
		t.Errorf("contentLineAtScreenRow(%d) = %d, want viewport offset + row = %d", inside, line, want)
	}

	// The fixed band's rows and rows past the terminal map outside.
	for _, y := range []int{tx.height - 4, tx.height - 4 + 1, tx.height - 1, tx.height, tx.height + 8} {
		if _, ok := tx.contentLineAtScreenRow(y); ok {
			t.Errorf("row %d (in/near the fixed band) must map outside the region", y)
		}
	}
	// Negative rows map outside too.
	if _, ok := tx.contentLineAtScreenRow(-1); ok {
		t.Errorf("row -1 must map outside the region")
	}
}

// TestScrollRegion_mouseRoutesThroughSeam asserts the Model mouse hit-test
// routes through the Transcript seam: a press on a visible text row inside the
// region starts a drag, while a press on the band's own rows never starts a
// selection — the selection side reads the region through the seam instead of
// re-deriving it from Model width math. The precise band-edge boundary (row
// just above maps inside, band row maps outside) is pinned at the seam level
// by TestScrollRegion_seamMapsBandEdgeRows.
func TestScrollRegion_mouseRoutesThroughSeam(t *testing.T) {
	m := scrollOverflowModel(t) // 120x12, telemetry band, hydrated viewport
	rows, top := historyContentRows(m)

	// Pick a non-blank content row that is visible inside the region.
	target := -1
	for i := top; i < top+m.tx.histViewport.Height() && i < len(rows); i++ {
		if strings.TrimSpace(rows[i]) != "" {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatalf("no selectable visible row in the region")
	}
	screenRow := target - top
	if screenRow >= m.tx.height-m.bandHeight() {
		t.Fatalf("selectable row %d must sit inside the region", screenRow)
	}

	// A press on a visible region row starts a drag.
	m = mustUpdate(t, m, dragMsg("press", 2, screenRow))
	if !m.tx.dragSel.active {
		t.Fatalf("press on visible region row %d must start a drag", screenRow)
	}

	// A press on the band's own rows never starts a selection.
	for _, y := range []int{m.tx.height - m.bandHeight(), m.tx.height - 1} {
		m = mustUpdate(t, m, dragMsg("press", 2, y))
		m = mustUpdate(t, m, dragMsg("motion", 20, y))
		if m.tx.dragSel.active {
			t.Errorf("press on band row %d must not start a selection", y)
		}
	}
}
