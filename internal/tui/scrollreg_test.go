package tui

import "testing"

func TestScrollRegionHeight_computesBandLeftover(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		height, band int
		want         int
	}{
		{"terminal taller than band", 12, 4, 8},
		{"band fills terminal", 4, 4, 0},
		{"band exceeds terminal", 3, 4, 0},
		{"no resize yet", 0, 4, -1},
		{"negative height", -2, 4, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := scrollRegionHeight(c.height, c.band); got != c.want {
				t.Errorf("scrollRegionHeight(%d, %d) = %d, want %d", c.height, c.band, got, c.want)
			}
		})
	}
}

func TestScrollRegion_inRegion(t *testing.T) {
	t.Parallel()
	r := scrollRegion{height: 8}
	for _, y := range []int{0, 7} {
		if !r.inRegion(y) {
			t.Errorf("row %d must be inside a region of height 8", y)
		}
	}
	for _, y := range []int{-1, 8, 100} {
		if r.inRegion(y) {
			t.Errorf("row %d must be outside a region of height 8", y)
		}
	}
	if (scrollRegion{}).inRegion(0) {
		t.Errorf("zero-value region (height 0) must reject every row")
	}
}

func TestScrollRegion_contentLineAtScreenRow(t *testing.T) {
	t.Parallel()
	r := scrollRegion{height: 8, yOffset: 20, content: 40}
	if line, ok := r.contentLineAtScreenRow(0); !ok || line != 20 {
		t.Errorf("row 0 maps to content line 20 (yOffset), got %d ok=%v", line, ok)
	}
	if line, ok := r.contentLineAtScreenRow(7); !ok || line != 27 {
		t.Errorf("row 7 maps to content line 27, got %d ok=%v", line, ok)
	}
	for _, y := range []int{-1, 8} {
		if _, ok := r.contentLineAtScreenRow(y); ok {
			t.Errorf("row %d lies outside the region and must not map", y)
		}
	}
	if _, ok := r.contentLineAtScreenRow(25); ok {
		t.Errorf("row 25 maps past the last content line (40) and must fail")
	}
	if _, ok := (scrollRegion{height: 4, yOffset: -3}).contentLineAtScreenRow(0); ok {
		t.Errorf("a negative content line must fail")
	}
}
