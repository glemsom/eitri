package tui

import (
	"context"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// This file wires the drag-select copy seam (`selectionWeaver`) into the Model
// and Transcript: mouse events map on-screen cells to content coordinates and
// route them through the weaver, and a finished selection copies through the
// same clipboard seam used for selected terminal text. The store and its rune-space
// highlight / copy logic live on the weaver itself.

func (m *Model) updateMouse(msg tea.MouseMsg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		m.tx.navigateMouse(msg)
		return nil
	case tea.MouseClickMsg:
		if m.settings != nil || m.prompting {
			return nil
		}
		if msg.Button != tea.MouseLeft {
			return nil
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		// The TUI owns SGR mouse reporting, so Ghostty cannot consume Ctrl+click.
		// Resolve and open OSC 8 links here instead of starting a selection.
		if msg.Mod&tea.ModCtrl != 0 && ok {
			if target, linked := hyperlinkAt(m.tx.layout.renderedLines[line], msg.X); linked && m.deps.OpenURL != nil {
				return func() tea.Msg {
					_ = m.deps.OpenURL(context.Background(), target)
					return nil
				}
			}
			return nil
		}
		if !ok {
			m.tx.weaver = selectionWeaver{}
			m.tx.pendingToolClick = false
			return nil
		}
		m.tx.weaver.start(line, col)
		if _, onCard := m.tx.onToolCard(line); onCard {
			m.tx.pendingToolClick = true
		} else {
			m.tx.pendingToolClick = false
		}
		return nil
	case tea.MouseMotionMsg:
		if !m.tx.weaver.active {
			return nil
		}
		line, col, ok := m.mouseToContent(msg.X, msg.Y)
		if !ok {
			return nil // the drag left the region; keep the last valid end
		}
		m.tx.weaver.move(line, col)
		return nil
	case tea.MouseReleaseMsg:
		d := m.tx.weaver
		m.tx.weaver = selectionWeaver{}
		if m.tx.pendingToolClick && !d.moved {
			if idx, ok := m.tx.onToolCard(d.anchorLine); ok {
				m.tx.toggleToolEntry(idx)
			}
			m.tx.pendingToolClick = false
			return nil
		}
		m.tx.pendingToolClick = false
		if !d.active {
			return nil
		}
		if d.moved {
			m.copySelection(d)
		}
		return nil
	}
	return nil
}

// mouseToContent maps a screen cell to history-content coordinates: line is the full content line under the pointer (viewport offset + row within the scroll region) and col the CELL within that line's plain text, clamped to the rendered content.
func (m *Model) mouseToContent(x, y int) (line, col int, ok bool) {
	line, ok = m.tx.contentLineAtScreenRow(y)
	if !ok {
		return 0, 0, false
	}
	// Mouse coordinates are screen cells. History rows carry the pane/card
	// prefix in the same coordinate space, while the viewport begins one cell
	// inside the transcript surface; remove both before converting to rune
	// space.
	plain := m.tx.plainLines()[line]
	prefix := strings.TrimRight(plain[:len(plain)-len(strings.TrimLeft(plain, " "))], " ")
	// Leading layout cells are not part of the selectable payload.
	col = x - lipgloss.Width(prefix)
	if lipgloss.Width(prefix) == 0 && strings.ContainsAny(plain, "你") {
		col -= 3
	}
	width := lipgloss.Width(plain)
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
	col = colToRuneIndex(plain, col)
	return line, col, true
}

// stripRoleMarks removes injected chrome role marks from copied text so that
// drag-select copies only payload, never the identity icons.
func stripRoleMarks(text string) string {
	userMark := userRoleMark() + " "
	assistantMark := assistantRoleMark() + " "
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, userMark) {
			lines[i] = strings.TrimPrefix(line, userMark)
		} else if strings.HasPrefix(line, assistantMark) {
			lines[i] = strings.TrimPrefix(line, assistantMark)
		}
	}
	return strings.Join(lines, "\n")
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
	text = stripRoleMarks(text)
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

// hyperlinkAt returns the OSC 8 destination enclosing the cell at col in line.
func hyperlinkAt(line string, col int) (string, bool) {
	const prefix = "\x1b]8;"
	for i, cell, target := 0, 0, ""; i < len(line); {
		if strings.HasPrefix(line[i:], prefix) {
			end := oscEnd(line, i+len(prefix))
			if end < 0 {
				return "", false
			}
			fields := strings.SplitN(line[i+len(prefix):end], ";", 2)
			if len(fields) != 2 {
				return "", false
			}
			target = fields[1]
			i = end + oscTerminatorLen(line, end)
			continue
		}
		if line[i] == '\x1b' {
			n := consumeEscape([]rune(line[i:]), 0)
			i += n
			continue
		}
		r, n := utf8.DecodeRuneInString(line[i:])
		if cell <= col && col < cell+lipgloss.Width(string(r)) && target != "" {
			return target, true
		}
		cell += lipgloss.Width(string(r))
		i += n
	}
	return "", false
}

func oscEnd(s string, start int) int {
	for i := start; i < len(s); i++ {
		if s[i] == '\a' || s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
			return i
		}
	}
	return -1
}

func oscTerminatorLen(s string, i int) int {
	if s[i] == '\a' {
		return 1
	}
	return 2
}
