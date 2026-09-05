package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// This file wires the drag-select copy seam (`selectionWeaver`) into the Model
// and Transcript: mouse events map on-screen cells to content coordinates and
// route them through the weaver, and a finished selection copies through the
// same clipboard seam used for selected terminal text. The store and its rune-space
// highlight / copy logic live on the weaver itself.

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
			m.tx.pendingToolClick = false
			return
		}
		m.tx.weaver.start(line, col)
		if _, onCard := m.tx.onToolCard(line); onCard {
			m.tx.pendingToolClick = true
		} else {
			m.tx.pendingToolClick = false
		}
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
		if m.tx.pendingToolClick && !d.moved {
			if idx, ok := m.tx.onToolCard(d.anchorLine); ok {
				m.tx.toggleToolEntry(idx)
			}
			m.tx.pendingToolClick = false
			return
		}
		m.tx.pendingToolClick = false
		if !d.active {
			return
		}
		if d.moved {
			m.copySelection(d)
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
