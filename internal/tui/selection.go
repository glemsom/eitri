package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// This file implements the drag-select copy seam: a click-drag over the
// history viewport highlights a cell range, and releasing the drag copies the
// selected plain-text range to the clipboard. Selection is hand-rolled from
// raw mouse cell state over the wrapped-lines transcript, built on bubbletea
// v2's per-type mouse messages (tea.MouseClickMsg / MouseMotionMsg /
// MouseReleaseMsg). The same mouse routing also owns the right-rail
// drag-resize — a left press on the rail's border column starts a width drag
// (see railDragFor), which is decided BEFORE the drag-select hit-test so the
// two gestures never overlap.
//
// Coordinates are tracked in *content* space, not screen space: line indexes
// the full rendered history content (the same line array the persisted
// viewport owns) and col indexes the cell within that line's plain text. That
// keeps a selection stable while the user scrolls mid-drag, and lets the copy
// read exactly the plain cells the user sees (no partial-code artifacts).

// railDrag is one in-progress mouse drag resizing the right rail: startWidth
// is the rail width when the press landed and startX the press column, so each
// motion computes newWidth = startWidth - (pointerX - startX) and applies it
// live through setRailWidth. It is tracked on the Model (not the Transcript)
// because it is pointer-button state, not transcript surface state: the
// transcript only ever sees the resulting setRailWidth writes, exactly like the
// drag-select state.
type railDrag struct {
	startWidth int
	startX     int
}

// doubleClickWindow is how close (in time) two clean border clicks must land
// to count as a double-click reset. Standard double-click intervals are tens
// to hundreds of milliseconds; 500ms is the usual desktop cutoff.
const doubleClickWindow = 500 * time.Millisecond

// minRailWidth is the narrowest the drag may shrink the rail to: the rail
// needs enough columns to stay a legible pane, so the drag stops at
// minRailWidth even when the pointer keeps pulling left.
const minRailWidth = 10

// maxRailWidth returns the widest the drag may grow the rail to: half the
// current terminal width, so the rail never dominates the surface. It is a
// function (not a constant) because the terminal resizes at runtime.
func maxRailWidth(terminalWidth int) int {
	return terminalWidth / 2
}

// railBorderHitZone is how close (in columns) a left press must land to the
// rail's left border to start a rail drag: 2 cells either side of the border
// column. A press further into the rail (or left of it, on the transcript)
// falls through to the existing drag-select / click paths.
const railBorderHitZone = 2

// railBorderColumn returns the screen column of the rail's left border: the
// terminal width minus the rail's current width (the rail strip occupies the
// rightmost railWidthOrDefault columns, and its left border is the first of
// them).
func (t Transcript) railBorderColumn() int {
	return t.width - t.railWidthOrDefault()
}

// railDragFor reports whether a left press at (x,y) starts a rail drag, and the
// drag state it starts with. A press starts a rail drag only when it lands
// within railBorderHitZone columns of the rail's left border and on a row the
// rail occupies (above the fixed bottom band, see inScrollRegion). Where it
// starts is decided here so a border press can never begin a text selection.
func (m Model) railDragFor(x, y int) (railDrag, bool) {
	if !m.tx.railVisible() || m.tx.width <= 0 {
		return railDrag{}, false
	}
	border := m.tx.railBorderColumn()
	if x < border-railBorderHitZone || x > border+railBorderHitZone {
		return railDrag{}, false
	}
	if !m.inScrollRegion(y) {
		return railDrag{}, false
	}
	return railDrag{startWidth: m.tx.railWidthOrDefault(), startX: x}, true
}

// disarmBorderClick clears any armed border double-click window. Called when
// a gesture proves the two border presses were not a double-click: the reset
// consumed it, the press ended in a drag, or an unrelated press landed
// elsewhere. A zero value is the "no arm" state, so the next border press
// starts a fresh pair.
func (m *Model) disarmBorderClick() {
	m.borderClick = time.Time{}
}

// inScrollRegion reports whether a screen row y falls inside the history scroll
// region: the rows above the fixed bottom band. Both the rail-drag hit test and
// the drag-select coordinate mapping guard against events that land on the
// band, so the boundary is decided once here and the two never drift.
func (m Model) inScrollRegion(y int) bool {
	return y >= 0 && y < m.tx.height-m.bandHeight()
}

// clampRailWidth clamps a requested rail width to the drag's legal range: the
// minRailWidth floor and a max of (a) half the terminal width and (b) what the
// transcript's 20-column readable floor allows — transcriptWidth floors at
// width-railWidth-1 >= 20, i.e. railWidth <= width-21, so a drag on a small
// terminal can never push the transcript below readable.
const minTranscriptWidth = 20

func clampRailWidth(w, terminalWidth int) int {
	max := maxRailWidth(terminalWidth)
	if capW := terminalWidth - minTranscriptWidth - 1; capW < max {
		max = capW
	}
	if w < minRailWidth {
		return minRailWidth
	}
	if w > max {
		return max
	}
	return w
}

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
		// A left-button press either starts a rail-width drag or a drag selection
		// over the history: the rail-border hit-test runs first so a press on the
		// border column starts the width drag, never a text selection. A second
		// clean border click inside the double-click window resets the rail to its
		// default width instead: the reset uses the same setRailWidth path as a
		// drag, so the transcript re-wraps and scroll/follow survive exactly as
		// they do after a drag.
		if m.settings != nil || m.prompting {
			return
		}
		if msg.Button != tea.MouseLeft {
			return
		}
		if d, ok := m.railDragFor(msg.X, msg.Y); ok {
			// A border press whose predecessor is still inside the window is the
			// second click of a double-click: snap the rail to the default width
			// and consume the window, so a third press starts a fresh pair. The
			// press still starts a drag anchored at the default width, so a
			// hold-and-drag after the reset resizes from home.
			if !m.borderClick.IsZero() && m.now().Sub(m.borderClick) <= doubleClickWindow {
				m.tx.setRailWidth(clampRailWidth(defaultRailWidth, m.tx.width))
				m.railDrag = &railDrag{startWidth: defaultRailWidth, startX: msg.X}
				m.tx.dragSel = nil
				m.disarmBorderClick()
				return
			}
			// First press of a pair (or a stale one): arm the window now. Motion
			// between press and release clears the arm, so only a clean
			// press+release can complete the pair.
			m.borderClick = m.now()
			m.railDrag = &d
			m.tx.dragSel = nil
			return
		}
		// A press outside the border's hit zone is an unrelated gesture
		// (history drag-select); it disarms any armed border double-click
		// window so an intervening transcript interaction can never pair with
		// a later border click into an accidental reset.
		m.disarmBorderClick()
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			m.tx.dragSel = nil
			return
		}
		m.tx.dragSel = &dragSelect{
			anchorLine: line, anchorCol: col,
			endLine: line, endCol: col,
		}
	case tea.MouseMotionMsg:
		// A live rail drag resizes the rail with the pointer; the width is
		// applied immediately via setRailWidth so the transcript re-wraps and
		// the rail re-renders every motion. Motion is only delivered while a
		// button is held, so railDrag nil here means the holding button is not
		// a border press.
		if m.railDrag != nil {
			// Motion between press and release means this border gesture is a
			// drag, not a click — disarm the window so the drag's press can
			// never pair with a later press into a reset.
			m.disarmBorderClick()
			m.tx.setRailWidth(clampRailWidth(m.railDrag.startWidth-(msg.X-m.railDrag.startX), m.tx.width))
			return
		}
		if m.tx.dragSel == nil {
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
		// Releasing a rail drag keeps the width already applied live during
		// motion; it only clears the drag state, so it never triggers the
		// drag-select copy or tool-entry click paths.
		if m.railDrag != nil {
			// The first press already armed the window; release leaves the arm
			// intact when the gesture was a clean click (no motion cleared it,
			// and a reset press cleared it via the reset branch), and there is
			// nothing to re-arm here. The drag state ends so the next border
			// press can be evaluated against the armed window.
			m.railDrag = nil
			return
		}
		d := m.tx.dragSel
		m.tx.dragSel = nil
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
// contains wide/multibyte characters. ok is false when the pointer
// is outside the history viewport region — over the fixed bottom band below
// it — or the viewport has not been sized yet.
func (m *Model) mouseToContent(x, y int) (line, col int, ok bool) {
	vp := m.tx.histViewport
	if vp == nil || vp.Height() <= 0 || m.tx.height <= 0 {
		return 0, 0, false
	}
	// The scroll region occupies the rows above the fixed bottom band; mirror
	// renderPane's region math so screen rows map to the viewport's visible
	// lines exactly.
	if !m.inScrollRegion(y) {
		return 0, 0, false
	}
	row := y
	if row < 0 || row >= vp.Height() {
		return 0, 0, false
	}
	line = vp.YOffset() + row
	if line < 0 || line >= len(m.tx.plainLines()) {
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
