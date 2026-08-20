package tui

// scrollRegionHeight computes the height in rows of the history scroll region —
// the rows left over by the fixed bottom band — from a terminal height and the
// band's height. A nonpositive terminal height means no resize has landed yet
// and yields -1 (the caller leaves the viewport unclamped); a band that fills or
// exceeds the terminal yields 0.
func scrollRegionHeight(height, bandHeight int) int {
	if height <= 0 {
		return -1
	}
	vh := height - bandHeight
	if vh < 0 {
		return 0
	}
	return vh
}

// scrollRegion is the seam that owns the history viewport region: its height
// (the rows left over by the fixed bottom band), the region hit-test (whether a
// screen row is inside the region and which content line it maps to), and the
// region-membership check. Render, click-drag selection, and wheel scroll all
// route through one source so on-screen rows and content coordinates cannot
// drift apart. It is a pure value type: callers render their own viewport state
// onto it (region height, viewport scroll offset, and the plain content line
// count), so it is unit-testable with no Transcript scaffolding.
type scrollRegion struct {
	height  int // rows of the region; a nonpositive value means no region is set up yet
	yOffset int // first content line shown at the top of the region
	content int // number of plain content lines the region indexes
}

// inRegion answers whether a screen row y lies inside the history region.
func (r scrollRegion) inRegion(y int) bool {
	if r.height <= 0 {
		return false
	}
	return y >= 0 && y < r.height
}

// contentLineAtScreenRow is the region hit-test: it answers whether screen row y
// lies inside the history region and, if so, which content line it maps to.
func (r scrollRegion) contentLineAtScreenRow(y int) (line int, ok bool) {
	if !r.inRegion(y) {
		return 0, false
	}
	line = r.yOffset + y
	if line < 0 || line >= r.content {
		return 0, false
	}
	return line, true
}
