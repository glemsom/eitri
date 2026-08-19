package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// helpView renders a formatted, sectioned help message as escape-free plain
// text: COMMANDS, KEYBINDINGS, and CONCEPTS sections plus the four built-in
// slash entries. Pure function — produces content only, no wiring to the TUI
// event loop.
//
// The help content is deliberately stored as plain text (issue #378) rather
// than ANSI-styled runs: the transcript runs every message through the
// Markdown→ANSI render pass, which would split and re-escape embedded escapes
// into literal `1;38;...` garbage on screen, and the clipboard path
// (transcriptText) would copy that raw escape junk. Storing escape-free text
// is the single root fix covering both the on-screen display and the copy
// path, at the cost of the help block's accent/faint styling.
// helpRow is one aligned line of the help output: a left key/label cell and a
// description. helpView pads each left cell to the section's widest so every
// description starts on a shared vertical ruler (issue #385).
type helpRow struct {
	key  string
	desc string
}

func helpView() string {
	var b strings.Builder

	// COMMANDS section
	b.WriteString(sectionEmoji("COMMANDS") + " COMMANDS\n")
	writeHelpRows(&b, []helpRow{
		{commandEmoji("/settings") + " /settings", "open settings panel"},
		{commandEmoji("/copy") + " /copy", "copy transcript to clipboard"},
		{commandEmoji("/login") + " /login", "interactive provider login"},
		{commandEmoji("/help") + " /help", "show this help message"},
	})

	b.WriteString("\n" + hr() + "\n")
	// KEYBINDINGS section
	b.WriteString(sectionEmoji("KEYBINDINGS") + " KEYBINDINGS\n")
	writeHelpRows(&b, []helpRow{
		{"ctrl+s", "open settings"},
		{"ctrl+o", "copy transcript"},
		{"ctrl+e", "toggle expanded view"},
		{"tab", "toggle thinking"},
		{"shift+enter", "insert newline"},
		{"?", "show help"},
		{"pgup/pgdn", "scroll history"},
		{"ctrl+x", "narrow pane"},
		{"ctrl+z", "widen pane"},
	})

	b.WriteString("\n" + hr() + "\n")
	// CONCEPTS section
	b.WriteString(sectionEmoji("CONCEPTS") + " CONCEPTS\n")
	writeHelpRows(&b, []helpRow{
		{"ctrl+e mode", "expand/collapse all tool result cards"},
		{"drag-select", "click and drag to select text"},
		{"right rail", "stats, context, and model info"},
	})

	return b.String()
}

// writeHelpRows writes each row left-aligned under a shared column so every
// description starts at the same column: the left cell is padded to the visual
// width of the section's widest cell, then separated from the description by a
// two-space gap — keeping the `key  description` shape while giving the section
// one vertical ruler. Width is measured by lipgloss.Width, not byte length, so
// double-width emoji hold the ruler in both UTF-8 and ASCII-glyph modes.
// Output stays escape-free plain text (issue #378).
func writeHelpRows(b *strings.Builder, rows []helpRow) {
	col := 0
	for _, r := range rows {
		if w := lipgloss.Width(r.key); w > col {
			col = w
		}
	}
	for _, r := range rows {
		b.WriteString("  ")
		b.WriteString(r.key)
		if pad := col - lipgloss.Width(r.key); pad > 0 {
			b.WriteString(strings.Repeat(" ", pad))
		}
		b.WriteString("  " + r.desc + "\n")
	}
}
