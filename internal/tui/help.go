package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

type helpRow struct {
	key  string
	desc string
}

type helpKeybindingCategory struct {
	name string
	rows []helpRow
}

// helpKeybindingCategories is the authoritative list of every keybinding the
// TUI honors. It is the single source for both help rendering and completeness
// tests.
var helpKeybindingCategories = []helpKeybindingCategory{
	{"COMPOSER", []helpRow{
		{"`up/down`", "navigate completion candidates; recall a prior/next prompt when the completion list is closed"},
		{"`tab`", "accept highlighted completion or cycle block focus when composer is empty"},
		{"`esc`", "close completion list, close mention dropdown, or stop a running turn"},
		{"`enter`", "submit draft or toggle focused block when empty"},
		{"`shift+enter`", "insert newline"},
	}},
	{"NAVIGATION", []helpRow{
		{"`pgup/pgdn`", "scroll history"},
		{"`home/end`", "jump to oldest/newest history"},
		{"`mouse wheel`", "scroll history"},
	}},
	{"PANES", []helpRow{
		{"`ctrl+e`", "toggle expanded/collapsed view"},
		{"`ctrl+x`", "narrow pane"},
		{"`ctrl+z`", "widen pane"},
	}},
	{"ACTIONS", []helpRow{
		{"`ctrl+c`", "stop a running turn, or quit when idle"},
	}},
}

func helpView() string {
	var b strings.Builder

	b.WriteString("# COMMANDS\n\n")
	cmdRows := make([]helpRow, len(BuiltinSlashCommands))
	for i, bc := range BuiltinSlashCommands {
		cmdRows[i] = helpRow{"`" + "/" + bc.Name + "`", bc.Desc}
	}
	writeHelpRows(&b, cmdRows)
	b.WriteString("  Type `/` to see all commands, including any discovered skills.\n")

	b.WriteString("\n# KEYBINDINGS\n\n")
	for _, cat := range helpKeybindingCategories {
		writeHelpCategory(&b, cat.name, cat.rows)
	}

	b.WriteString("\n# WORKSPACE MENTIONS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`@`", "type @ at a word boundary to open the file mention dropdown"},
		{"`up/down`", "navigate mention candidates"},
		{"`tab/enter`", "accept the highlighted mention"},
		{"`esc`", "close the mention dropdown"},
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

// writeHelpRows pads each key cell to the section's widest key, so every description lands on one vertical ruler.
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
