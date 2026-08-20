package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
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
	cur := 0
	for i, r := range rs {
		w := lipgloss.Width(string(r))
		if displayCol < cur+w {
			return i
		}
		cur += w
	}
	return len(rs) - 1
}

// highlight wraps the cells covered by an in-progress drag in reverse video
// across the full rendered content; the persisted viewport clips it to the
// visible window. It is a no-op for an inactive selection.
func (s selectionWeaver) highlight(content string) string {
	if !s.active {
		return content
	}
	startLine, startCol, endLine, endCol := s.selRange()
	lines := strings.Split(content, "\n")
	if startLine >= len(lines) {
		return content
	}
	for i := startLine; i <= endLine && i < len(lines); i++ {
		from, to := startCol, endCol
		if i > startLine {
			from = 0
		}
		if i < endLine {
			to = len([]rune(ansiStrip(lines[i]))) - 1
		}
		lines[i] = highlightRange(lines[i], from, to)
	}
	return strings.Join(lines, "\n")
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

// highlightRange wraps the cells covered by a drag in reverse video across one
// line of rendered content.
func highlightRange(line string, from, to int) string {
	if from < 0 || to < 0 || from > to {
		return line
	}
	rs := []rune(line)
	var b strings.Builder
	cell := 0
	i := 0
	for i < len(rs) {
		if rs[i] == '\x1b' {
			n := consumeEscape(rs, i)
			b.WriteString(string(rs[i : i+n]))
			i += n
			continue
		}
		if cell == from {
			b.WriteString("\x1b[7m")
		}
		b.WriteRune(rs[i])
		if cell == to {
			b.WriteString("\x1b[27m")
		}
		cell++
		i++
	}
	return b.String()
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
