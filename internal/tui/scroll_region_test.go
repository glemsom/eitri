package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

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
	tx.layout.dirty = true
	return tx
}

func TestScrollRegion_sizesViewportFromSeam(t *testing.T) {
	t.Parallel()
	tx := scrollRegionFixture(t, 12, 4)

	if got := tx.scrollRegionHeight(4); got != 8 {
		t.Errorf("scrollRegionHeight(band 4) = %d, want terminal 12 - band 4 = 8", got)
	}
	if vh := tx.histViewport.Height(); vh != 8 {
		t.Errorf("renderHistoryViewport must size the viewport to the seam's region height, got %d, want 8", vh)
	}

	tx.height = 0
	if got := tx.scrollRegionHeight(4); got != -1 {
		t.Errorf("pre-resize scrollRegionHeight = %d, want -1 (unclamped)", got)
	}
	tx.height = 4
	if got := tx.scrollRegionHeight(4); got != 0 {
		t.Errorf("band-fills-terminal scrollRegionHeight = %d, want 0", got)
	}
	if got := tx.railClampHeight(4); got != 0 {
		t.Errorf("railClampHeight must share the region seam's 0-row budget, got %d", got)
	}
}

func TestScrollRegion_seamMapsBandEdgeRows(t *testing.T) {
	t.Parallel()
	tx := scrollRegionFixture(t, 12, 4)
	inside := tx.height - 4 - 1

	line, ok := tx.contentLineAtScreenRow(inside)
	if !ok {
		t.Fatalf("screen row %d (just above the band) must map inside the region", inside)
	}
	if want := tx.histViewport.YOffset() + inside; line != want {
		t.Errorf("contentLineAtScreenRow(%d) = %d, want viewport offset + row = %d", inside, line, want)
	}

	for _, y := range []int{tx.height - 4, tx.height - 4 + 1, tx.height - 1, tx.height, tx.height + 8} {
		if _, ok := tx.contentLineAtScreenRow(y); ok {
			t.Errorf("row %d (in/near the fixed band) must map outside the region", y)
		}
	}
	if _, ok := tx.contentLineAtScreenRow(-1); ok {
		t.Errorf("row -1 must map outside the region")
	}
}

func TestScrollRegion_mouseRoutesThroughSeam(t *testing.T) {
	t.Parallel()
	m := scrollOverflowModel(t) // 120x12, telemetry band, hydrated viewport
	rows, top := historyContentRows(m)

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

	m = mustUpdate(t, m, dragMsg("press", 2, screenRow))
	if !m.tx.weaver.active {
		t.Fatalf("press on visible region row %d must start a drag", screenRow)
	}

	for _, y := range []int{m.tx.height - m.bandHeight(), m.tx.height - 1} {
		m = mustUpdate(t, m, dragMsg("press", 2, y))
		m = mustUpdate(t, m, dragMsg("motion", 20, y))
		if m.tx.weaver.active {
			t.Errorf("press on band row %d must not start a selection", y)
		}
	}
}
