package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// HelpOverlay owns the open help surface: the rendered reference split into
// lines with a scroll offset, so `/help` reads in a dedicated scrollable
// overlay instead of being appended to the transcript. Keeping it out of the
// message stream means repeated `/help` never clutters the conversation or the
// model's context.
type HelpOverlay struct {
	theme   Theme
	mdTheme string // glamour theme name used to render the reference
	source  string // the raw reference, re-rendered when the width changes
	lines   []string
	width   int
	height  int
	offset  int
}

// openHelpOverlay builds the overlay from the authoritative help reference,
// borrowing the live chrome theme, the configured Markdown theme, and the
// current viewport. The reference is rendered through the same Markdown
// pipeline the transcript uses, so code spans and headers read identically
// whether help came from `/help` or a message.
func openHelpOverlay(theme Theme, mdTheme string, width, height int) *HelpOverlay {
	h := &HelpOverlay{theme: theme, mdTheme: mdTheme, source: helpView(), width: width, height: height}
	h.render()
	return h
}

// render (re)computes the display lines from the source at the current width.
func (h *HelpOverlay) render() {
	text := h.source
	if h.width > 0 {
		if out, err := RenderMarkdown(text, h.width, h.mdTheme); err == nil {
			text = out
		}
	}
	h.lines = strings.Split(text, "\n")
	h.scroll(0) // re-clamp after a re-render
}

// viewRows returns how many reference lines fit above the footer hint row.
func (h *HelpOverlay) viewRows() int {
	if h.height <= 0 {
		return len(h.lines)
	}
	if rows := h.height - 1; rows >= 1 {
		return rows
	}
	return 1
}

// maxOffset is the largest valid scroll offset: the first line index whose
// window still reaches the end of the reference.
func (h *HelpOverlay) maxOffset() int {
	if m := len(h.lines) - h.viewRows(); m > 0 {
		return m
	}
	return 0
}

// scroll moves the viewport by delta lines and clamps it to the reference.
func (h *HelpOverlay) scroll(delta int) {
	h.offset += delta
	if h.offset < 0 {
		h.offset = 0
	}
	if h.offset > h.maxOffset() {
		h.offset = h.maxOffset()
	}
}

// Handle routes one message into the overlay and reports whether the user
// dismissed it. Every key is handled (the overlay owns the keyboard while open).
func (h *HelpOverlay) Handle(msg tea.Msg) (closed bool, handled bool) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		if m.Width != h.width {
			h.width = m.Width
			h.render()
		}
		h.height = m.Height
		h.scroll(0) // re-clamp after a resize shrinks or grows the window
		return false, true
	case tea.KeyPressMsg:
		switch m.String() {
		case "esc", "ctrl+c", "q":
			return true, true
		case "up":
			h.scroll(-1)
		case "down":
			h.scroll(1)
		case "pgup":
			h.scroll(-h.viewRows())
		case "pgdown":
			h.scroll(h.viewRows())
		case "home":
			h.offset = 0
		case "end":
			h.offset = h.maxOffset()
		}
		return false, true
	}
	return false, false
}

// View renders the visible window of the reference plus the footer hint row.
func (h *HelpOverlay) View() string {
	start := h.offset
	if start > len(h.lines) {
		start = len(h.lines)
	}
	end := start + h.viewRows()
	if end > len(h.lines) {
		end = len(h.lines)
	}
	body := strings.Join(h.lines[start:end], "\n")
	return body + "\n" + h.footer() + "\n"
}

// footer renders the scroll/close hint with a position readout, shown only when
// the reference overflows the viewport.
func (h *HelpOverlay) footer() string {
	sep := g(" · ", " . ")
	hint := g("↑/↓", "up/down") + " scroll" + sep + "pgup/pgdn page" + sep + "esc close"
	if rows := h.viewRows(); len(h.lines) > rows {
		last := min(h.offset+rows, len(h.lines))
		hint += fmt.Sprintf("   %d–%d/%d", h.offset+1, last, len(h.lines))
	}
	return h.theme.statusStyle.Render(hint)
}

// startHelp opens the `/help` reference as a dedicated scrollable overlay.
func (m Model) startHelp() (tea.Model, tea.Cmd) {
	m.help = openHelpOverlay(m.tx.theme, m.tx.configTheme, m.tx.width, m.tx.height)
	return m, nil
}

// updateHelp routes one message through the open help overlay; a dismiss closes
// it and re-arms the face upload hidden while the overlay was up.
func (m Model) updateHelp(msg tea.Msg) (tea.Model, tea.Cmd) {
	closed, _ := m.help.Handle(msg)
	if closed {
		m.help = nil
		return m, m.queueFaceDrawCmd()
	}
	return m, nil
}
