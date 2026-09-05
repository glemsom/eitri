package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type helpRow struct {
	key  string
	desc string
}

func helpView() string {
	var b strings.Builder

	b.WriteString("# COMMANDS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`/settings`", "open settings panel"},
		{"`/new`", "start a fresh session (clears this conversation)"},
		{"`/login`", "interactive provider login"},
		{"`/help`", "show this help message"},
	})

	b.WriteString("\n# KEYBINDINGS\n\n")
	writeHelpCategory(&b, "COMPOSER", []helpRow{
		{"`up/down`", "navigate completion candidates; recall a prior/next prompt when the completion list is closed"},
		{"`tab/enter`", "accept highlighted completion"},
		{"`esc`", "close completion list"},
		{"`tab`", "cycle block focus when composer is empty"},
		{"`enter`", "submit draft or toggle focused block when empty"},
		{"`shift+enter`", "insert newline"},
	})
	writeHelpCategory(&b, "NAVIGATION", []helpRow{
		{"`pgup/pgdn`", "scroll history"},
	})
	writeHelpCategory(&b, "PANES", []helpRow{
		{"`ctrl+e`", "toggle expanded/collapsed view"},
		{"`ctrl+x`", "narrow pane"},
		{"`ctrl+z`", "widen pane"},
	})
	writeHelpCategory(&b, "ACTIONS", []helpRow{
		{"`ctrl+s`", "open settings"},
	})

	b.WriteString("\n# CONCEPTS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`expanded mode`", "ctrl+e toggles all tool and reasoning blocks"},
		{"`block focus`", "tab to focus, enter to expand one block"},
		{"`drag-select`", "click and drag to select text"},
		{"`right rail`", "stats, context, and model info"},
	})

	return b.String()
}

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
