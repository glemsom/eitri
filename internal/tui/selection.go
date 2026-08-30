package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// This file wires the drag-select copy seam (`selectionWeaver`) into the Model
// and Transcript: mouse events map on-screen cells to content coordinates and
// route them through the weaver, and a finished selection copies through the
// same clipboard seam as Ctrl+O and /copy. The store and its rune-space
// highlight / copy logic live on the weaver itself.

// updateMouse applies one mouse event to the model: wheel events scroll the history viewport; a left-button click inside the history region starts a drag selection, motion extends it (clamped to the rendered content), and release copies the selected plain-text range to the clipboard through the same seam as Ctrl+O and /copy.
func (m *Model) updateMouse(msg tea.MouseMsg) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
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
			m.tx.weaver = selectionWeaver{}
			return
		}
		m.tx.weaver.start(line, col)
	case tea.MouseMotionMsg:
		if !m.tx.weaver.active {
			return
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			return // the drag left the region; keep the last valid end
		}
		m.tx.weaver.move(line, col)
	case tea.MouseReleaseMsg:
		d := m.tx.weaver
		m.tx.weaver = selectionWeaver{}
		if !d.active {
			return
		}
		if d.moved {
			m.copySelection(d)
			return
		}
		if idx, _, ok := m.tx.toolEntryAtLine(d.anchorLine); ok {
			m.tx.toggleToolEntry(idx)
		}
	}
}

// mouseToContent maps a screen cell to history-content coordinates: line is the full content line under the pointer (viewport offset + row within the scroll region) and col the CELL within that line's plain text, clamped to the rendered content.
func (m *Model) mouseToContent(x, y int) (line, col int, ok bool) {
	line, ok = m.tx.contentLineAtScreenRow(y)
	if !ok {
		return 0, 0, false
	}
	col = x
	width := lipgloss.Width(m.tx.plainLines()[line])
	if tw := m.tx.transcriptWidth(); tw > 0 && width > tw {
		width = tw
	}
	if width <= 0 {
		return 0, 0, false
	}
	if col < 0 {
		col = 0
	}
	if col > width-1 {
		col = width - 1
	}
	col = colToRuneIndex(m.tx.plainLines()[line], col)
	return line, col, true
}

// copySelection copies the plain text covered by a finished drag selection to the clipboard through the same seam as Ctrl+O and /copy: the weaver computes the rune-space plain text, and the model owns the clipboard / status side effects.
func (m *Model) copySelection(d selectionWeaver) {
	lines := m.tx.plainLines()
	text, ok := d.coveredLines(lines)
	if !ok {
		if len(lines) == 0 {
			m.feedback = failureFeedback("copy failed: empty transcript")
		} else {
			m.feedback = failureFeedback("copy failed: selection out of range")
		}
		return
	}
	if text == "" {
		return // selection covered no text; nothing to copy
	}
	if m.clipboard == nil {
		m.feedback = failureFeedback("copy failed: clipboard unavailable")
		return
	}
	if err := m.clipboard(text); err != nil {
		m.feedback = failureFeedback("copy failed: " + err.Error())
		return
	}
	m.feedback = successFeedback("copied")
}
