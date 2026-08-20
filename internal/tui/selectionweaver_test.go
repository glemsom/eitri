package tui

import (
	"strings"
	"testing"
)

// TestSelectionWeaver_selsRangeNormalizes verifies the selectionWeaver seam's
// range normalization: regardless of drag direction, selRange returns the
// ordered [start,end] cells in reading order.
func TestSelectionWeaver_selsRangeNormalizes(t *testing.T) {
	t.Parallel()
	var s selectionWeaver
	s.start(2, 5)
	s.move(0, 1)
	sl, sc, el, ec := s.selRange()
	if sl != 0 || sc != 1 || el != 2 || ec != 5 {
		t.Errorf("forward-normalized selRange = (%d,%d)-(%d,%d), want (0,1)-(2,5)", sl, sc, el, ec)
	}
	s.start(0, 1)
	s.move(2, 5)
	sl, sc, el, ec = s.selRange()
	if sl != 0 || sc != 1 || el != 2 || ec != 5 {
		t.Errorf("backward-normalized selRange = (%d,%d)-(%d,%d), want (0,1)-(2,5)", sl, sc, el, ec)
	}
	if s.active != true {
		t.Error("a started + moved weaver stays active")
	}
}

// TestSelectionWeaver_highlightWrapsRange verifies the seam's highlight runs in
// rune space: it wraps exactly the selected cells across the covered rows in
// reverse video, never touching the rows outside the range or the row's own
// escape sequences.
func TestSelectionWeaver_highlightWrapsRange(t *testing.T) {
	t.Parallel()
	var s selectionWeaver
	s.start(0, 0)
	s.move(1, 2)
	content := "abcdef\n\x1b[31mgh\x1b[0mijkl\nzz"
	got := s.highlight(content)
	lines := strings.Split(got, "\n")
	if spans := reverseVideoSpans(lines[0]); strings.Join(spans, "") != "abcdef" {
		t.Errorf("row 0 highlight spans = %q, want %q", spans, "abcdef")
	}
	if spans := reverseVideoSpans(lines[1]); strings.Join(spans, "") != "ghi" {
		t.Errorf("row 1 highlight spans = %q, want %q (rune-space, display-width(gh)=2)", spans, "ghi")
	}
	if !strings.Contains(lines[1], "\x1b[31m") {
		t.Errorf("row 1 must keep its own escape sequences, got %q", lines[1])
	}
	if plain := ansiStrip(lines[2]); plain != "zz" {
		t.Errorf("row outside the range must be untouched, got %q", lines[2])
	}
	// An inactive weaver is a no-op.
	var idle selectionWeaver
	if got := idle.highlight(content); got != content {
		t.Errorf("inactive highlight must return content unchanged, got %q", got)
	}
}

// TestSelectionWeaver_coveredLinesCopiesRuneSpace verifies the seam's copy in
// rune space: a single-line range copies the rune substring and a multi-line
// range joins the per-row rune slices with newlines, reproducing exactly the
// wrapped rows seen on screen (wide chars selected exactly).
func TestSelectionWeaver_coveredLinesCopiesRuneSpace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		run   func(s *selectionWeaver)
		lines []string
		want  string
		ok    bool
	}{
		{
			name:  "single line rune range",
			run:   func(s *selectionWeaver) { s.start(0, 2); s.move(0, 4) },
			lines: []string{"ab你defg"},
			want:  "你de",
			ok:    true,
		},
		{
			name:  "multi line joins with newlines",
			run:   func(s *selectionWeaver) { s.start(0, 3); s.move(1, 2) },
			lines: []string{"abcdef", "ghijkl"},
			want:  "def\nghi",
			ok:    true,
		},
		{
			name:  "out of bounds reports failure",
			run:   func(s *selectionWeaver) { s.start(9, 0); s.move(9, 1) },
			lines: []string{"abc"},
			want:  "",
			ok:    false,
		},
		{
			name:  "empty transcript reports failure",
			run:   func(s *selectionWeaver) { s.start(0, 0); s.move(0, 1) },
			lines: nil,
			want:  "",
			ok:    false,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var s selectionWeaver
			c.run(&s)
			got, ok := s.coveredLines(c.lines)
			if ok != c.ok || got != c.want {
				t.Errorf("coveredLines = %q/%v, want %q/%v", got, ok, c.want, c.ok)
			}
		})
	}
}
