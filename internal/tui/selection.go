package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// This file implements the T6 drag-select copy seam (issue #124): a click-drag
// over the history viewport highlights a cell range, and releasing the drag
// copies the selected plain-text range to the clipboard. Selection is
// hand-rolled from raw mouse cell state over the wrapped-lines transcript,
// built on bubbletea v2's per-type mouse messages (tea.MouseClickMsg /
// MouseMotionMsg / MouseReleaseMsg).
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
type dragSelect struct {
	anchorLine, anchorCol int
	endLine, endCol       int
	moved                 bool
}

// selRange returns the normalized selection as ordered [start,end] cells:
// start precedes end in reading order regardless of drag direction.
func (d *dragSelect) selRange() (startLine, startCol, endLine, endCol int) {
	if d.anchorLine < d.endLine || (d.anchorLine == d.endLine && d.anchorCol <= d.endCol) {
		return d.anchorLine, d.anchorCol, d.endLine, d.endCol
	}
	return d.endLine, d.endCol, d.anchorLine, d.anchorCol
}

// updateMouse applies one mouse event to the model: wheel events scroll the
// history viewport (T2, issue #120; forwarded to the Transcript, issue #244);
// a left-button click inside the history
// region starts a drag selection, motion extends it (clamped to the rendered
// content), and release copies the selected plain-text range to the clipboard
// through the same seam as Ctrl+O and /copy (T6, issue #124). Events outside
// the history region and wheel events never touch the composer, so editing
// focus is preserved (issue #124 AC4). Mouse input is ignored while the
// Settings surface or the continuation prompt owns the screen. In bubbletea v2
// the mouse event is an interface: wheel/click/motion/release arrive as their
// own concrete message types instead of a single MouseMsg with an Action.
func (m *Model) updateMouse(msg tea.MouseMsg) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		m.navigateMouse(msg)
		return
	case tea.MouseClickMsg:
		// A left-button press starts a drag selection over the history.
		if m.settings != nil || m.prompting {
			return
		}
		if msg.Button != tea.MouseLeft {
			return
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			m.dragSel = nil
			return
		}
		m.dragSel = &dragSelect{
			anchorLine: line, anchorCol: col,
			endLine: line, endCol: col,
		}
	case tea.MouseMotionMsg:
		if m.dragSel == nil {
			return
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			return // the drag left the region; keep the last valid end
		}
		m.dragSel.endLine = line
		m.dragSel.endCol = col
		m.dragSel.moved = true
	case tea.MouseReleaseMsg:
		d := m.dragSel
		m.dragSel = nil
		if d == nil {
			return
		}
		if d.moved {
			m.copySelection(*d)
			return
		}
		// A plain click (press+release on one cell, no drag) toggles the tool
		// entry under the pointer: click-to-expand a collapsed tool result, or
		// collapse an expanded one (benchmark §4.4 mouse ergonomics). Clicks
		// off any tool row stay inert, preserving drag-select's copy semantics.
		if idx, _, ok := m.toolEntryAtLine(d.anchorLine); ok {
			m.toggleToolEntry(idx)
		}
	}
}

// mouseToContent maps a screen cell to history-content coordinates: line is
// the full content line under the pointer (viewport offset + row within the
// scroll region) and col the cell within that line's plain text, clamped to
// the rendered content. ok is false when the pointer is outside the history
// viewport region — over the review overlay above it or the fixed bottom band
// below it — or the viewport has not been sized yet.
func (m *Model) mouseToContent(x, y int) (line, col int, ok bool) {
	vp := m.histViewport
	if vp == nil || vp.Height() <= 0 || m.height <= 0 {
		return 0, 0, false
	}
	// The scroll region occupies the rows between the review overlay (when
	// open, issue #90) and the fixed bottom band; mirror renderPane's region
	// math so screen rows map to the viewport's visible lines exactly.
	bandLines := m.bandHeight()
	reviewLines := 0
	if m.review != nil {
		var review strings.Builder
		m.renderReview(&review)
		reviewLines = m.reviewRegionRows(review.String(), bandLines)
	}
	if y < reviewLines || y >= m.height-bandLines {
		return 0, 0, false
	}
	row := y - reviewLines
	if row < 0 || row >= vp.Height() {
		return 0, 0, false
	}
	line = vp.YOffset() + row
	if line < 0 || line >= len(m.historyPlainLines()) {
		return 0, 0, false
	}
	col = x
	// Clamp to the plain width of the content line and the viewport's own
	// horizontal clip (transcript width), so a pointer over the rail or past a
	// short line selects to that line's last visible cell.
	width := lipgloss.Width(m.historyPlainLines()[line])
	if tw := m.transcriptWidth(); tw > 0 && width > tw {
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
	return line, col, true
}

// historyPlainLines returns the history scroll content as plain text per
// rendered row (ANSI stripped) — the coordinate space drag selection maps
// into. The split matches the persisted viewport's own line split exactly, so
// content line indexes agree between selection and the rendered transcript.
// It is lazy + cached (issue #242): it reads the persistent layout cache,
// rebuilding it once per transcript change via recordLayout so a drag's motion
// events reuse the recorded plain-row space instead of re-rendering each one.
func (m *Model) historyPlainLines() []string {
	m.ensureLayout()
	return m.layout.plain
}

// copySelection copies the plain text covered by a finished drag selection to
// the clipboard through the same seam as Ctrl+O and /copy (issue #124 AC2):
// a single-line range copies the cell substring; a multi-line range joins the
// per-row slices with newlines, reproducing exactly the wrapped rows the user
// saw on screen. The outcome is surfaced as the same band status note
// ("copied" / "copy failed: …") the other copy paths use.
func (m *Model) copySelection(d dragSelect) {
	startLine, startCol, endLine, endCol := d.selRange()
	lines := m.historyPlainLines()
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
		s := lines[startLine]
		if startCol < 0 || startCol >= len(s) || endCol < 0 || endCol >= len(s) {
			m.savedMsg = "copy failed: selection out of range"
			return
		}
		b.WriteString(s[startCol : endCol+1])
	} else {
		// First row: from startCol to the end of the row.
		if startCol >= 0 && startCol < len(lines[startLine]) {
			b.WriteString(lines[startLine][startCol:])
		}
		for i := startLine + 1; i < endLine && i < len(lines); i++ {
			b.WriteString("\n")
			b.WriteString(lines[i])
		}
		if endLine < len(lines) {
			b.WriteString("\n")
			if endCol >= 0 && endCol < len(lines[endLine]) {
				b.WriteString(lines[endLine][:endCol+1])
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
// it to the visible window (issue #124 AC1). Lines and cells outside the range
// keep their exact original bytes, so surrounding styling never breaks.
func (m Model) highlightSelection(content string) string {
	return newTranscript(m).highlightSelection(content)
}

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
// the cell grid a drag selection reads and copies (issue #124 AC3: selection
// accounts for ANSI-rendered text, so the copied snippet is exactly the
// displayed characters with no escape residue).
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
