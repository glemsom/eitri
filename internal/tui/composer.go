package tui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Composer + bottom band: the fixed-height input surface the Model renders
// beneath the transcript. The composer is a multi-line textarea that grows
// with its draft up to maxComposerRows, then scrolls internally so a long
// draft never spills into the transcript; the band around it carries the
// status strip, the slash-command completion list, and the accent separator.
// Everything here is rendering-only — key routing stays in Model.Update.

// Composer caret style policy: the composer's hardware caret is deliberately a steady (non-blinking) block rather than whatever the textarea or terminal defaults would draw.
const (
	composerCaretShape = tea.CursorBlock
	composerCaretBlink = false
)

// minComposerRows is how tall the composer rests when the draft is empty, so the input field reads as a multi-line composer rather than a single-line prompt.
const minComposerRows = 2

// maxComposerRows is how tall the composer may grow inside the fixed bottom band before it scrolls internally: a long draft never spills into the transcript — the textarea's own viewport scrolls past this bound, and the band stays pinned while the history viewport yields rows.
const maxComposerRows = 8

// composerByteOffset computes the byte offset of the textarea's caret in its
// value, so mention parsing can map the cursor position into the string.
func (m Model) composerByteOffset() int {
	value := m.composer.Value()
	row := m.composer.Line()
	col := m.composer.Column()
	off := 0
	for i := 0; i < row && i < strings.Count(value, "\n")+1; i++ {
		idx := strings.IndexByte(value[off:], '\n')
		if idx < 0 {
			break
		}
		off += idx + 1
	}
	line := value[off:]
	if eol := strings.IndexByte(line, '\n'); eol >= 0 {
		line = line[:eol]
	}
	runes := []rune(line)
	if col > len(runes) {
		col = len(runes)
	}
	return off + len(string(runes[:col]))
}

// syncComposerHeight grows the composer with its draft up to maxComposerRows, then lets the textarea scroll internally: an empty draft rests at minComposerRows, each new line adds a row up to the bound, and beyond it the composer's internal viewport scrolls so the band never grows past the bound.
func (m *Model) syncComposerHeight() {
	rows := composerContentRows(m.composer)
	if rows > maxComposerRows {
		rows = maxComposerRows
	}
	if rows < minComposerRows {
		rows = minComposerRows
	}
	if m.tx.height > 0 {
		if lim := m.tx.height - 1; rows > lim {
			rows = lim
		}
	}
	if rows < 1 {
		rows = 1
	}
	m.composer.SetHeight(rows)
}

// composerContentRows estimates how many terminal rows the composer's current value occupies once word-wrapped at the composer width: one row per hard newline plus soft-wrap continuations, floored at one.
func composerContentRows(c textarea.Model) int {
	w := c.Width()
	if w < 1 {
		w = 1
	}
	rows := 0
	for _, line := range strings.Split(c.Value(), "\n") {
		width := lipgloss.Width(line)
		if width <= 0 {
			rows++
			continue
		}
		rows += (width + w - 1) / w
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

// bandHeight returns how many terminal rows the fixed bottom band (status strip, slash completion, composer) occupies, so the scroll region and the right rail can clamp to the rows it leaves behind.
func (m Model) bandHeight() int {
	var band strings.Builder
	m.renderBand(&band)
	return lineCount(band.String())
}

// renderPane renders the transcript + composer surface into the left pane.
func (m Model) renderPane() string {
	var band strings.Builder
	m.renderBand(&band)
	return m.tx.renderPane(band.String())
}

// syncComposerRail recolors the composer's prompt rail by editing state: the accent rail signals an editable composer, while a running turn makes the composer inert, so the rail dims to a muted accent (state-as-color — the mode-colored composer border pattern, benchmark §4.3).
func (m *Model) syncComposerRail() {
	c := m.tx.theme.accent
	if m.tx.busy {
		c = dimmed(m.tx.theme.accent, 0.45)
	}
	st := m.composer.Styles()
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(c)
	m.composer.SetStyles(st)
}

// renderBand renders the fixed bottom band: the hints-only status row (when telemetry is wired; the row carries keybinding hints plus the busy spinner, never telemetry numbers) plus the slash-command completion list and the composer, in that order.
func (m Model) renderBand(b *strings.Builder) {
	var inner strings.Builder
	statusRow := ""
	if m.telemetry != nil {
		if m.tx.busy {
			statusRow = m.tx.theme.bandStatusStyle.Render(busyLine(m.tx.spinner, m.tx.phase())) + "  "
		}
		hints := bandHints()
		if m.tx.busy {
			hints += g(" · ", " . ") + "ctrl+c stop"
		}
		statusRow += m.tx.theme.statusStyle.Render(hints)
		statusRow = lipgloss.NewStyle().Width(m.tx.bandWidth()).Render(statusRow)
		inner.WriteString(statusRow)
		inner.WriteString("\n")
	}
	m.slash.RenderCompletion(&inner, m.tx.theme, m.composer.Value())
	m.mention.RenderCompletion(&inner, m.tx.theme, m.composer.Value(), m.composerByteOffset())
	inner.WriteString(m.composer.View())
	if m.savedMsg != "" {
		inner.WriteString("\n" + m.tx.theme.statusStyle.Render(m.savedMsg))
	}
	tw := m.tx.bandWidth()
	if tw < 2 {
		tw = 2
	}
	b.WriteString(m.tx.theme.bandSeparatorStyle.Render(strings.Repeat(g("─", "-"), tw)))
	b.WriteString("\n")
	b.WriteString(inner.String())
}

// composerCursor returns the composer's hardware caret for the current frame, or nil when the composer is not the active editing surface .
func (m Model) composerCursor(content string) *tea.Cursor {
	if m.settings != nil || m.prompting || m.tx.busy {
		return nil
	}
	cur := m.composer.Cursor()
	if cur == nil {
		return nil
	}
	var band strings.Builder
	m.renderBand(&band)
	pre := m.composerPreRows()
	cur.Y += lineCount(content) - lineCount(band.String()) + pre
	return cur
}

// composerPreRows returns how many band rows render above the composer: the accent separator, the live status strip (when wired), and one row per slash-completion candidate .
func (m Model) composerPreRows() int {
	n := 1 // accent separator
	if m.telemetry != nil {
		n++
	}
	n += m.slash.CandidateCount(m.composer.Value())
	n += m.mention.CandidateCount(m.composer.Value(), m.composerByteOffset())
	return n
}
