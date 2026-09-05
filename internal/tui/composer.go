package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

func composerPanelBodyWidth(bandWidth int) int {
	w := bandWidth - 2
	if w < 1 {
		w = 1
	}
	return w
}

func renderTitledPanel(title string, width int, style lipgloss.Style, body string) string {
	if width < 2 {
		width = 2
	}
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	h := g("─", "-")
	titleText := h + " " + title + " "
	if lipgloss.Width(titleText) > width-2 {
		titleText = h
	}
	topFill := width - 2 - lipgloss.Width(titleText)
	if topFill < 0 {
		topFill = 0
	}
	// Render the top border in two passes: the title carries its own inline
	// styling (the forge glint), whose closing reset would otherwise strip the
	// panel color from the trailing fill and corner. Re-applying the panel style
	// to the tail keeps the top border the same color as the sides and bottom.
	var b strings.Builder
	b.WriteString(style.Render(g("╭", "+") + titleText))
	b.WriteString(style.Render(strings.Repeat(h, topFill) + g("╮", "+")))
	for _, line := range strings.Split(body, "\n") {
		plainLine := ansiStrip(line)
		if lipgloss.Width(plainLine) > inner {
			line = truncateWidth(plainLine, inner-1) + g("…", "...")
		}
		pad := inner - lipgloss.Width(line)
		if pad < 0 {
			pad = 0
		}
		b.WriteByte('\n')
		b.WriteString(style.Render(g("│", "|")))
		b.WriteString(line)
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(style.Render(g("│", "|")))
	}
	b.WriteByte('\n')
	b.WriteString(style.Render(g("╰", "+") + strings.Repeat(h, inner) + g("╯", "+")))
	return b.String()
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

// renderBand renders the fixed bottom band: completion candidates above the composer panel, then contextual composer hints and transient feedback below it.
func (m Model) renderBand(b *strings.Builder) {
	var inner strings.Builder
	if m.tx.busy {
		body := forgeBusyLine(m.tx.spinner, m.tx.phase()) + " · " + m.tx.theme.statusStyle.Render("Hold steady — composer locked during forging")
		style := lipgloss.NewStyle().Foreground(dimmed(m.tx.theme.accent, 0.45))
		inner.WriteString(renderTitledPanel(m.forgeTitle(), m.tx.bandWidth(), style, body))
	} else {
		if m.slash.isOpen() {
			inner.WriteString(renderTitledPanel("Commands", m.tx.bandWidth(), m.tx.theme.bandSeparatorStyle, m.slash.RenderCompletionBody(m.tx.theme)))
			inner.WriteByte('\n')
		} else if m.mention.isOpen() {
			inner.WriteString(renderTitledPanel("Workspace mentions", m.tx.bandWidth(), m.tx.theme.bandSeparatorStyle, m.mention.RenderCompletionBody(m.tx.theme)))
			inner.WriteByte('\n')
		}
		inner.WriteString(renderTitledPanel("Ask Eitri", m.tx.bandWidth(), m.tx.theme.bandSeparatorStyle, m.composer.View()))
	}
	inner.WriteByte('\n')
	inner.WriteString(m.renderBandStatusRow())
	if m.feedback.text != "" {
		inner.WriteString("\n" + m.renderFeedback())
	}
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
	cur.X++
	var band strings.Builder
	m.renderBand(&band)
	pre := m.composerPreRows()
	cur.Y += lineCount(content) - lineCount(band.String()) + pre
	return cur
}

// renderBandStatusRow renders the band's status row underneath the composer (or
// forge) panel in both idle and busy states: the contextual key hints on the
// left and the workspace path right-aligned on the right. It duplicates nothing
// the right rail owns — no provider/model, no elapsed counter, no token stats —
// and no phase badge: the phase already reads from the forge and the busy copy,
// so this row pins only the workspace, the one global path not otherwise
// surfaced.
func (m Model) renderBandStatusRow() string {
	w := m.tx.bandWidth()
	right := ""
	if ws := m.deps.WorkspacePath; ws != "" {
		// Cap the right zone so the left hints keep room; an over-long workspace
		// is trimmed from its head so the path tail survives.
		budget := w / 2
		if budget < 1 {
			budget = 1
		}
		if ansi.StringWidth(ws) > budget {
			ws = truncateFront(ws, budget)
		}
		right = ws
	}
	left := fitBandLine(m.composerHint(), w-ansi.StringWidth(right))
	return m.tx.theme.statusStyle.Render(left + right)
}

func (m Model) renderFeedback() string {
	switch m.feedback.kind {
	case feedbackSuccess:
		return m.tx.theme.outcomeOKStyle.Render(fitBandLine(g("✓ ", "OK ")+m.feedback.text, m.tx.bandWidth()))
	case feedbackFailure:
		return m.tx.theme.outcomeErrStyle.Render(fitBandLine(g("✗ ", "ERR ")+m.feedback.text, m.tx.bandWidth()))
	default:
		return m.tx.theme.statusStyle.Render(fitBandLine(m.feedback.text, m.tx.bandWidth()))
	}
}

func fitBandLine(s string, width int) string {
	if width < 1 {
		width = 1
	}
	if lipgloss.Width(s) > width {
		// Reserve the ellipsis's own width so the filled line stays exactly
		// width columns even when the ASCII ellipsis is wider than one rune.
		ell := g("…", "...")
		keep := width - lipgloss.Width(ell)
		if keep < 1 {
			keep = 1
		}
		s = truncateWidth(s, keep) + ell
	}
	pad := width - lipgloss.Width(s)
	if pad < 0 {
		pad = 0
	}
	return s + strings.Repeat(" ", pad)
}

// composerPreRows returns how many band rows render above the textarea caret origin: one row per slash-completion candidate, with the composer panel top border already reflected by the rendered band origin.
func (m Model) composerPreRows() int {
	n := 0
	if m.slash.isOpen() {
		n += m.slash.CandidateCount() + 2 // slash popover borders
	} else if m.mention.isOpen() {
		n += m.mention.CandidateCount() + 2 // mention popover borders
	}
	n++ // titled composer panel top border
	return n
}

// forgeElapsed renders the busy timer suffix (" · 1m 05s elapsed") once the turn
// is underway, empty before the first busy frame so a freshly forged title still
// reads plainly.
func forgeElapsed(m Model) string {
	if m.tx.busyStartedAt.IsZero() {
		return ""
	}
	return " · " + formatElapsed(time.Since(m.tx.busyStartedAt)) + " elapsed"
}

func (m Model) composerHint() string {
	sep := g(" · ", " . ")
	if m.tx.busy {
		return "ctrl+c stop" + sep + "pgup read history" + sep + "end follow"
	}
	if m.slash.isOpen() || m.mention.isOpen() {
		return g("↑/↓", "up/down") + " navigate" + sep + "tab/enter select" + sep + "esc close"
	}
	return "enter send" + sep + "shift+enter newline" + sep + "ctrl+e expand/collapse" + sep + "ctrl+s settings"
}

// forgeTitle gives the busy state a restrained metallic glint. The spinner's
// existing cadence drives a slower back-and-forth sweep, while reduced-motion
// keeps the stronger static hierarchy without animation.
func (m Model) forgeTitle() string {
	accent := m.tx.theme.accent
	muted := lipgloss.NewStyle().Foreground(dimmed(accent, 0.65))
	strong := lipgloss.NewStyle().Foreground(accent)
	elapsed := forgeElapsed(m)
	if !motionEnabled() {
		return muted.Render("⚒  Eitri is ") + strong.Bold(true).Render("forging") + muted.Render(elapsed)
	}

	// Sweep over visible glyphs rather than spaces so the three-cell glint keeps
	// its shape as it crosses the whole title; the elapsed readout rides the
	// same sweep so the timer moves with the forge cadence instead of drifting.
	title := []rune("⚒  Eitri is forging" + elapsed)

	var glyphs []int
	for i, r := range title {
		if r != ' ' {
			glyphs = append(glyphs, i)
		}
	}
	path := len(glyphs)*2 - 2
	step := (m.tx.forgeFrame / 2) % path
	pos := step
	forward := pos < len(glyphs)
	if !forward {
		pos = path - pos
	}
	start := pos - 1
	if start < 0 {
		start = 0
	} else if start > len(glyphs)-3 {
		start = len(glyphs) - 3
	}

	levels := []float64{0.82, 0.92, 1.0}
	if !forward {
		levels = []float64{1.0, 0.92, 0.82}
	}
	highlights := make(map[int]float64, 3)
	for i := range 3 {
		highlights[glyphs[start+i]] = levels[i]
	}

	var b strings.Builder
	for i, r := range title {
		style := muted
		if level, ok := highlights[i]; ok {
			style = lipgloss.NewStyle().Foreground(dimmed(accent, level))
			if level == 1 {
				style = style.Bold(true)
			}
		}
		b.WriteString(style.Render(string(r)))
	}
	return b.String()
}
