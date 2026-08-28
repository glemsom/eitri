package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// helpView renders a formatted, sectioned help message as escape-free plain text using Markdown syntax the transcript's Markdown→ANSI pass already understands (issue #387): `#` headers for section titles (rendered in the theme's accent color) and backtick code spans around command/key names (rendered with code styling, distinct from their descriptions) — while the KEYBINDINGS categories (issue #386) keep their grouped glyph labels.
type helpRow struct {
	key  string
	desc string
}

func helpView() string {
	var b strings.Builder

	b.WriteString("# COMMANDS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`/settings`", "open settings panel"},
		{"`/copy`", "copy transcript to clipboard"},
		{"`/login`", "interactive provider login"},
		{"`/help`", "show this help message"},
	})

	b.WriteString("\n# KEYBINDINGS\n\n")
	writeHelpCategory(&b, "COMPOSER", []helpRow{
		{"`up/down`", "navigate completion candidates"},
		{"`tab/enter`", "accept highlighted completion"},
		{"`esc`", "close completion list"},
		{"`tab`", "cycle block focus when composer is empty"},
		{"`enter`", "submit draft or toggle focused block when empty"},
		{"`shift+enter`", "insert newline"},
	})
	writeHelpCategory(&b, "NAVIGATION", []helpRow{
		{"`?`", "show help"},
		{"`pgup/pgdn`", "scroll history"},
	})
	writeHelpCategory(&b, "PANES", []helpRow{
		{"`e`", "expand all blocks"},
		{"`E`", "collapse all blocks"},
		{"`ctrl+e`", "toggle expanded view"},
		{"`ctrl+x`", "narrow pane"},
		{"`ctrl+z`", "widen pane"},
	})
	writeHelpCategory(&b, "ACTIONS", []helpRow{
		{"`ctrl+s`", "open settings"},
		{"`ctrl+o`", "copy transcript"},
	})

	b.WriteString("\n# CONCEPTS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`expanded mode`", "e/E or ctrl+e expand or collapse all blocks"},
		{"`block focus`", "tab to focus, enter to expand one block"},
		{"`drag-select`", "click and drag to select text"},
		{"`right rail`", "stats, context, and model info"},
	})

	return b.String()
}

// writeHelpCategory writes one labeled KEYBINDINGS category: an indented emoji-prefixed category header followed by its aligned rows (issue #386).
func writeHelpCategory(b *strings.Builder, name string, rows []helpRow) {
	b.WriteString("  " + categoryEmoji(name) + " " + name + "\n")
	writeHelpRows(b, rows)
}

// writeHelpRows writes each row left-aligned under a shared column so every description starts at the same column: the left cell is padded to the visual width of the section's widest cell, then separated from the description by a two-space gap — keeping the `key description` shape while giving the section one vertical ruler.
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
