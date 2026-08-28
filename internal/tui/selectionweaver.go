package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selectionWeaver is the seam owning the drag-select copy logic: the
// selection store (the active flag plus the anchor/end cell range), the
// width-aware rune-index conversion from a mouse cell, and the rune-space
// highlight / copy operations that read that range. It is a pure value type:
// the caller seeds it through start/move, feeds it the rendered content, and
// it returns highlighted lines / the copied plain text — so it is unit-testable
// on its own without any Transcript or Model scaffolding.
type selectionWeaver struct {
	active                bool
	anchorLine, anchorCol int
	endLine, endCol       int
	moved                 bool
}

// start begins a drag selection at cell (line, col), resetting any prior
// anchor and end to the same point.
func (s *selectionWeaver) start(line, col int) {
	*s = selectionWeaver{active: true, anchorLine: line, anchorCol: col, endLine: line, endCol: col}
}

// move extends the in-progress drag's end cell to (line, col) and marks the
// selection as moved, so release distinguishes a drag (which copies) from a
// plain click (which toggles the block under the anchor).
func (s *selectionWeaver) move(line, col int) {
	s.endLine, s.endCol = line, col
	s.moved = true
}

// selRange returns the normalized selection as ordered [start,end] cells:
// start precedes end in reading order regardless of drag direction.
func (s selectionWeaver) selRange() (startLine, startCol, endLine, endCol int) {
	if s.anchorLine < s.endLine || (s.anchorLine == s.endLine && s.anchorCol <= s.endCol) {
		return s.anchorLine, s.anchorCol, s.endLine, s.endCol
	}
	return s.endLine, s.endCol, s.anchorLine, s.anchorCol
}

// colToRuneIndex converts a display-width column (the client cell space a mouse
// event reports, and the space lipgloss counts) into a rune index into the
// plain line.
func colToRuneIndex(line string, displayCol int) int {
	rs := []rune(line)
	if len(rs) == 0 {
		return 0
	}
	// Merge variation-selector pairs into one selectable unit: the cluster
	// (base + VS16) occupies the cells its combined width spans.
	type cluster struct { //nolint:revive // local clarity over a named type used once
		runeIdx int
		width   int
	}
	var clusters []cluster
	for i, r := range rs {
		if r == '\ufe0f' && len(clusters) > 0 {
			clusters[len(clusters)-1].width += runeCellWidth(r)
			continue
		}
		clusters = append(clusters, cluster{runeIdx: i, width: runeCellWidth(r)})
	}
	cur := 0
	for _, c := range clusters {
		if displayCol < cur+c.width {
			return c.runeIdx
		}
		cur += c.width
	}
	return len(rs) - 1
}

// highlight wraps the cells covered by an in-progress drag in the theme's
// selection background across the full rendered content; the persisted
// viewport clips it to the visible window. sel is the `48;...` background SGR
// to paint, so marking reads as a background-color change rather than the
// fg/bg swap of reverse video (which content resets would silently drop). It
// is a no-op for an inactive selection.
func (s selectionWeaver) highlight(content string, sel string) string {
	if !s.active {
		return content
	}
	lines := strings.Split(content, "\n")
	return strings.Join(s.highlightVisible(lines, 0, sel), "\n")
}

// highlightVisible paints the selection over a visible window of rendered rows.
// base is the content-line number of visible[0], so the in-progress selection
// stays stored in global transcript coordinates while mouse-motion frames only
// touch rows that can reach the terminal.
func (s selectionWeaver) highlightVisible(visible []string, base int, sel string) []string {
	if !s.active || len(visible) == 0 {
		return visible
	}
	startLine, startCol, endLine, endCol := s.selRange()
	for i := range visible {
		line := base + i
		if line < startLine || line > endLine {
			continue
		}
		from, to := startCol, endCol
		if line > startLine {
			from = 0
		}
		if line < endLine {
			to = len([]rune(ansiStrip(visible[i]))) - 1
		}
		visible[i] = highlightRange(visible[i], from, to, sel)
	}
	return visible
}

// coveredLines returns the plain text covered by a finished drag selection,
// and whether the range was in bounds. A single-line range copies the rune
// substring; a multi-line range joins the per-row rune slices with newlines,
// reproducing exactly the wrapped rows the user saw on screen. An out-of-range
// or empty selection reports ok=false; an in-bounds selection covering no text
// returns "" with ok=true (the caller copies nothing and stays silent).
func (s selectionWeaver) coveredLines(lines []string) (text string, ok bool) {
	startLine, startCol, endLine, endCol := s.selRange()
	if len(lines) == 0 || startLine < 0 || startLine >= len(lines) {
		return "", false
	}
	var b strings.Builder
	if startLine == endLine {
		rs := []rune(lines[startLine])
		if startCol < 0 || startCol >= len(rs) || endCol < 0 || endCol >= len(rs) || startCol > endCol {
			return "", false
		}
		b.WriteString(string(rs[startCol : endCol+1]))
	} else {
		first := []rune(lines[startLine])
		if startCol >= 0 && startCol < len(first) {
			b.WriteString(string(first[startCol:]))
		}
		for i := startLine + 1; i < endLine && i < len(lines); i++ {
			b.WriteString("\n")
			b.WriteString(lines[i])
		}
		if endLine < len(lines) {
			b.WriteString("\n")
			last := []rune(lines[endLine])
			if endCol >= 0 && endCol < len(last) {
				b.WriteString(string(last[:endCol+1]))
			}
		}
	}
	return b.String(), true
}

// runeCellWidth reports the display-cell contribution of one rune in a
// terminal grid. A variation selector contributes the second cell of the
// emoji-presentation pair it extends (the base glyph's own width comes from
// ansi.StringWidth); every other rune's footprint is its standard cell width.
func runeCellWidth(r rune) int {
	if r == '\ufe0f' {
		return 1
	}
	return ansi.StringWidth(string(r))
}

// highlightRange paints the cells covered by a drag in the selection
// background sel across one line of rendered content. from/to are rune indices
// (the selection store's column space); they are painted at their display-cell
// positions, so wide emoji shift the marked span in cell space instead of
// misaligning it. Rendered content intersperses SGR resets (\x1b[m / \x1b[0m)
// per styled token; a bare reverse-video wrap would be dropped at the first
// reset, so the selection background is re-asserted after every escape inside
// the range and the content's own background (bubble fill, inline-code chip)
// is restored just past the range end.
func highlightRange(line string, from, to int, sel string) string {
	if from < 0 || to < 0 || from > to {
		return line
	}
	rs := []rune(line)
	var b strings.Builder
	var contentBG string // content's own bg SGR payload at the cursor ("" = default)
	in := false
	i, r := 0, 0 // raw rune index; printable-rune index (escapes excluded)
	for i < len(rs) {
		if rs[i] == '\x1b' {
			n := consumeEscape(rs, i)
			seq := string(rs[i : i+n])
			b.WriteString(seq)
			if len(seq) > 2 && seq[:2] == "\x1b[" && seq[len(seq)-1] == 'm' {
				if payload, changed := sgrBackground(seq); changed {
					contentBG = payload
				}
			}
			if in {
				// Re-assert the selection background so a content reset or
				// color change never unwrites the marking mid-range.
				b.WriteString(sel)
			}
			i += n
			continue
		}
		if r == from {
			in = true
			b.WriteString(sel)
		}
		b.WriteRune(rs[i])
		if r == to && in {
			// Restore the content's own background active at the end of the
			// range, so a styled region (bubble fill, inline-code chip) keeps
			// its fill once the marking stops.
			if contentBG == "" {
				b.WriteString("\x1b[49m") // back to the terminal default background
			} else {
				b.WriteString("\x1b[" + contentBG + "m")
			}
			in = false
		}
		r++
		i++
	}
	return b.String()
}

// sgrBackground computes the background state an SGR sequence leaves active:
// payload is the `48;...` payload of any background it sets ("" when it clears
// the background to default), and changed reports whether the sequence touched
// the background at all. A bare \x1b[m or a 0/49 parameter resets the
// background to default; a 48;5;n or 48;2;r;g;b (or legacy 48;n) sets one; any
// other SGR leaves the background untouched.
func sgrBackground(seq string) (payload string, changed bool) {
	p := seq
	if len(p) > 0 && p[len(p)-1] == 'm' {
		p = p[:len(p)-1]
	}
	if len(p) > 2 {
		p = p[2:] // drop "\x1b["
	}
	params := strings.Split(p, ";")
	if len(params) == 1 && params[0] == "" {
		return "", true // bare \x1b[m resets all attributes
	}
	for i := 0; i < len(params); i++ {
		switch params[i] {
		case "0", "49":
			return "", true // full reset / explicit default background
		case "48":
			if i+1 >= len(params) {
				return "", true
			}
			switch params[i+1] {
			case "2": // 48;2;r;g;b truecolor
				if i+4 < len(params) {
					return "48;2;" + params[i+2] + ";" + params[i+3] + ";" + params[i+4], true
				}
			case "5": // 48;5;n indexed
				if i+2 < len(params) {
					return "48;5;" + params[i+2], true
				}
			default: // 48;n legacy palette
				return "48;" + params[i+1], true
			}
			return "", true
		}
	}
	return "", false
}

// ansiStrip removes ANSI escape sequences from s, returning the plain text —
// the cell grid a drag selection reads and copies.
func ansiStrip(s string) string {
	rs := []rune(s)
	var b strings.Builder
	i := 0
	for i < len(rs) {
		if rs[i] != '\x1b' {
			b.WriteRune(rs[i])
			i++
			continue
		}
		i += consumeEscape(rs, i)
	}
	return b.String()
}

// consumeEscape returns the rune length of the ANSI escape sequence starting at
// rs[i] (which must be ESC).
func consumeEscape(rs []rune, i int) int {
	if rs[i] != '\x1b' || i+1 >= len(rs) {
		return 1
	}
	switch rs[i+1] {
	case '[':
		j := i + 2
		for j < len(rs) && !(rs[j] >= 0x40 && rs[j] <= 0x7e) {
			j++
		}
		if j < len(rs) {
			j++ // the final byte
		}
		return j - i
	case ']':
		j := i + 2
		for j < len(rs) {
			if rs[j] == '\a' {
				return j - i + 1
			}
			if rs[j] == '\x1b' && j+1 < len(rs) && rs[j+1] == '\\' {
				return j - i + 2
			}
			j++
		}
		return j - i
	default:
		return 2
	}
}
