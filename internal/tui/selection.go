package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// This file implements the drag-select copy seam: a click-drag over the
// history viewport highlights a cell range, and releasing the drag copies the
// selected plain-text range to the clipboard. Selection is hand-rolled from
// raw mouse cell state over the wrapped-lines transcript, built on bubbletea
// v2's per-type mouse messages (tea.MouseClickMsg / MouseMotionMsg /
// MouseReleaseMsg).
//
// Coordinates are tracked in *content* space, not screen space: line indexes
// the full rendered history content (the same line array the persisted
// viewport owns) and col indexes the cell within that line's plain text. That
// keeps a selection stable while the user scrolls mid-drag, and lets the copy
// read exactly the plain cells the user sees (no partial-code artifacts).
// dragSelect is one in-progress click-drag selection over the history
// viewport. anchor is the content cell where the drag started; end tracks the
// live drag cell. moved reports whether the pointer actually dragged, so a
// plain click (press+release on one cell) never copies a one-cell snippet.
// active reports whether a selection is being drawn; an inactive zero-value
// dragSelect means no drag is in progress. The Transcript owns it as a plain
// value field (stable root), so an active selection keeps surviving value
// copies without heap indirection.
type dragSelect struct {
	active                bool
	anchorLine, anchorCol int
	endLine, endCol       int
	moved                 bool
}

// selRange returns the normalized selection as ordered [start,end] cells:
// start precedes end in reading order regardless of drag direction.
func (d dragSelect) selRange() (startLine, startCol, endLine, endCol int) {
	if d.anchorLine < d.endLine || (d.anchorLine == d.endLine && d.anchorCol <= d.endCol) {
		return d.anchorLine, d.anchorCol, d.endLine, d.endCol
	}
	return d.endLine, d.endCol, d.anchorLine, d.anchorCol
}

// updateMouse applies one mouse event to the model: wheel events scroll the
// history viewport; a left-button click inside the history region starts a
// drag selection, motion extends it (clamped to the rendered content), and
// release copies the selected plain-text range to the clipboard through the
// same seam as Ctrl+O and /copy. Events outside the history region and wheel
// events never touch the composer, so editing focus is preserved. Mouse input
// is ignored while the Settings surface or the continuation prompt owns the
// screen. In bubbletea v2 the mouse event is an interface: wheel/click/
// motion/release arrive as their own concrete message types instead of a
// single MouseMsg with an Action.
func (m *Model) updateMouse(msg tea.MouseMsg) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		// Wheel scroll lives on the owned Transcript.
		m.tx.navigateMouse(msg)
		return
	case tea.MouseClickMsg:
		if m.settings != nil || m.prompting {
			return
		}
		if msg.Button != tea.MouseLeft {
			return
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			m.tx.dragSel = dragSelect{}
			return
		}
		m.tx.dragSel = dragSelect{
			active:     true,
			anchorLine: line, anchorCol: col,
			endLine: line, endCol: col,
		}
	case tea.MouseMotionMsg:
		if !m.tx.dragSel.active {
			return
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			return // the drag left the region; keep the last valid end
		}
		m.tx.dragSel.endLine = line
		m.tx.dragSel.endCol = col
		m.tx.dragSel.moved = true
	case tea.MouseReleaseMsg:
		d := m.tx.dragSel
		m.tx.dragSel = dragSelect{}
		if !d.active {
			return
		}
		if d.moved {
			m.copySelection(d)
			return
		}
		// A plain click (press+release on one cell, no drag) toggles the tool
		// entry under the pointer: click-to-expand a collapsed tool result, or
		// collapse an expanded one (benchmark §4.4 mouse ergonomics). Clicks
		// off any tool row stay inert, preserving drag-select's copy semantics.
		if idx, _, ok := m.tx.toolEntryAtLine(d.anchorLine); ok {
			m.tx.toggleToolEntry(idx)
		}
	}
}

// mouseToContent maps a screen cell to history-content coordinates: line is
// the full content line under the pointer (viewport offset + row within the
// scroll region) and col the CELL within that line's plain text, clamped to
// the rendered content. The mouse X is in screen display-width space; the
// returned col is converted to a RUNE INDEX into the line so every downstream
// consumer (highlight and copy) shares one coordinate space even when the row
// contains wide/multibyte characters. The row may itself fall
// inside the history region is decided by the Transcript's scroll-region
// hit-test seam (contentLineAtScreenRow), so the selection side reads the same
// region the render pass laid out instead of recomputing it from Model width
// math; ok is false over the fixed bottom band or before the viewport is sized.
func (m *Model) mouseToContent(x, y int) (line, col int, ok bool) {
	line, ok = m.tx.contentLineAtScreenRow(y)
	if !ok {
		return 0, 0, false
	}
	col = x
	// Clamp to the plain width of the content line and the viewport's own
	// horizontal clip (transcript width), so a pointer over the rail or past a
	// short line selects to that line's last visible cell.
	width := lipgloss.Width(m.tx.plainLines()[line])
	if tw := m.tx.transcriptWidth(); tw > 0 && width > tw {
		width = tw
	}
	if width <= 0 {
		// The row has no selectable cells; nothing to anchor a drag on.
		return 0, 0, false
	}
	if col < 0 {
		col = 0
	}
	if col > width-1 {
		col = width - 1
	}
	// Convert the clamped display-width column to a rune index so the stored
	// selection column is rune-safe and width-aware end to end.
	col = colToRuneIndex(m.tx.plainLines()[line], col)
	return line, col, true
}

// colToRuneIndex converts a display-width column (the client cell space a mouse
// event reports, and the space lipgloss counts) into a rune index into the
// plain line. Wide characters such as CJK, emoji, or unicode arrows occupy more
// display cells than runes (e.g. 你 = 2 display cells but 1 rune), so this walk
// sums each rune's display width and stops at the rune whose cell range
// contains the requested column, clamping past-the-end columns to the last
// rune. Selection coordinates are rune-indexed throughout the drag-select
// pipeline (see mouseToContent/copySelection/highlightSelection), so this
// conversion keeps highlight and copy aligned.
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
	// Past the end of the line's content: clamp to the last rune so a drag to
	// a short line's trailing padded cells selects up to its final rune.
	return len(rs) - 1
}

// copySelection copies the plain text covered by a finished drag selection to
// the clipboard through the same seam as Ctrl+O and /copy: a single-line
// range copies the rune substring; a multi-line range joins the per-row rune
// slices with newlines, reproducing exactly the wrapped rows the user saw on
// screen. Selection columns are RUNE INDEXES, so slices are taken from
// []rune — never from the raw bytes — keeping the copy byte-for-byte correct
// for wide/multibyte characters and never splitting a multibyte rune.
// Boundaries that exceed a row's rune length are rejected gracefully ("copy
// failed: selection out of range"). The outcome is surfaced as the same band
// status note ("copied" / "copy failed: …") the other copy paths use.
func (m *Model) copySelection(d dragSelect) {
	startLine, startCol, endLine, endCol := d.selRange()
	lines := m.tx.plainLines()
	if len(lines) == 0 {
		m.savedMsg = "copy failed: empty transcript"
		return
	}
	if startLine < 0 || startLine >= len(lines) {
		m.savedMsg = "copy failed: selection out of range"
		return
	}
	var b strings.Builder
	if startLine == endLine {
		rs := []rune(lines[startLine])
		if startCol < 0 || startCol >= len(rs) || endCol < 0 || endCol >= len(rs) || startCol > endCol {
			m.savedMsg = "copy failed: selection out of range"
			return
		}
		b.WriteString(string(rs[startCol : endCol+1]))
	} else {
		// First row: from startCol to the end of the row (rune slice, never bytes).
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
	text := b.String()
	if text == "" {
		return // selection covered no text; nothing to copy
	}
	if m.clipboard == nil {
		m.savedMsg = "copy failed: clipboard unavailable"
		return
	}
	if err := m.clipboard(text); err != nil {
		m.savedMsg = "copy failed: " + err.Error()
		return
	}
	m.savedMsg = "copied"
}

// highlightSelection wraps the cells covered by an in-progress drag in reverse
// video across the full rendered history content; the persisted viewport clips
// it to the visible window. Lines and cells outside the range
// keep their exact original bytes, so surrounding styling never breaks.
// highlightRange wraps the plain cells [from,to] (inclusive, 0-based) of an
// ANSI-styled line in reverse-video escapes, preserving every original
// sequence and character outside the range. Reverse video is used because it
// inverts whatever styling the underlying glamour/lipgloss rendering applied,
// so the highlight reads on any theme without computing contrast. A range past
// the end of the line or with from > to is a no-op.
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

// consumeEscape returns the rune length of the ANSI escape sequence starting
// at rs[i] (which must be ESC). It consumes CSI sequences (ESC [ ... final
// byte 0x40–0x7e), OSC sequences (ESC ] ... BEL or ST), and two-character
// escapes (ESC M, ESC 7, …). A bare trailing ESC consumes one rune.
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
