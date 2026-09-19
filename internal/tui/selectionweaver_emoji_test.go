package tui

import (
	"strings"
	"testing"
)

// cellSpans walks rendered content in display-cell space (emoji = 2 cells,
// VS16 pairs = 2 cells) and reports the [start,end) cell range painted inside
// reverse video, or ok=false if no reverse span exists.
func cellSpans(s string) (start, end int, ok bool) {
	in := false
	cell := 0
	i := 0
	rs := []rune(s)
	for i < len(rs) {
		if rs[i] == '\x1b' {
			n := consumeEscape(rs, i)
			seq := string(rs[i : i+n])
			if !in && strings.HasPrefix(seq, "\x1b[48;") {
				in = true
				start = cell
			} else if in && seq == "\x1b[49m" {
				in = false
				end = cell
				return start, end, true
			}
			i += n
			continue
		}
		w := runeCellWidth(rs[i])
		cell += w
		i++
	}
	_ = in
	return 0, 0, false
}

// TestHighlightRange_cellSpaceWithEmoji locks drag selection on emoji lines:
// a mouse drag over display cells [0,10) must convert through colToRuneIndex
// and paint a reverse-video span covering exactly those 10 display cells,
// even though an emoji in range occupies two cells. Regression: the old
// colToRuneIndex counted one cell per rune (VS16 pairs as one), so every
// VS16 emoji shifted the highlight background one column off.
func TestHighlightRange_cellSpaceWithEmoji(t *testing.T) {
	cases := []struct {
		name string
		line string
	}{
		{"plain", strings.Repeat("b", 20)},
		{"wide emoji", "😀 " + strings.Repeat("b", 18)},
		{"vs16 emoji", "✏️ " + strings.Repeat("b", 18)},
		{"vs16 warning", "⚠️ " + strings.Repeat("b", 18)},
		{"emoji mid-line", strings.Repeat("b", 5) + "🧭" + strings.Repeat("b", 14)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const wantCells = 10 // mouse drag over display cells [0,10)
			from := colToRuneIndex(tc.line, 0)
			to := colToRuneIndex(tc.line, wantCells-1)
			hl := highlightRange(tc.line, from, to, testSel)
			start, end, ok := cellSpans(hl)
			if !ok {
				t.Fatalf("no reverse-video span found in %q", hl)
			}
			if start != 0 || end-start != wantCells {
				t.Errorf("highlight covers %d cells [%d,%d), want %d cells from 0 (line %q)",
					end-start, start, end, wantCells, tc.line)
			}
		})
	}
}

// TestColToRuneIndex_emojiCells locks mouse-cell → rune-index conversion to
// display-cell space: after a two-cell emoji the cursor column counts two
// cells, so the rune index must skip past the whole emoji cluster.
func TestColToRuneIndex_emojiCells(t *testing.T) {
	line := "✏️ abc" // cells: ✏️=2, space=1, a=1...
	tests := []struct {
		col  int
		want int // rune index into []rune(line): ✏,\ufe0f,' ','a','b','c'
	}{
		{col: 0, want: 0},  // on first cell of the emoji
		{col: 1, want: 0},  // second cell of the emoji: same selectable unit, base rune
		{col: 2, want: 2},  // ' ' (first cell past the emoji)
		{col: 4, want: 4},  // 'b'
		{col: 99, want: 5}, // clamped past end
	}
	for _, tt := range tests {
		if got := colToRuneIndex(line, tt.col); got != tt.want {
			t.Errorf("colToRuneIndex(%q, %d) = %d, want %d", line, tt.col, got, tt.want)
		}
	}
}

// TestCoveredLines_emojiCellAlignment locks that drag-select cell alignment
// around a two-cell VS16 emoji returns the correct rune substring: both
// display cells of the emoji map to the base rune, so selecting through the
// cluster must include the variation selector.
func TestCoveredLines_emojiCellAlignment(t *testing.T) {
	line := "ab✏️cd" // runes: a,b,✏,\ufe0f,c,d
	cases := []struct {
		name     string
		from, to int // rune indices after colToRuneIndex conversion
		want     string
	}{
		{"before emoji", 0, 1, "ab"},
		{"spanning emoji", 0, 3, "ab✏️"},
		{"after emoji", 0, 5, "ab✏️cd"},
		{"emoji only", 2, 3, "✏️"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := selectionWeaver{active: true, anchorLine: 0, anchorCol: tc.from, endLine: 0, endCol: tc.to}
			got, ok := s.coveredLines([]string{line})
			if !ok {
				t.Fatalf("coveredLines ok = false, want true")
			}
			if got != tc.want {
				t.Errorf("coveredLines = %q, want %q", got, tc.want)
			}
		})
	}
}
