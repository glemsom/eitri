package tui

import (
	"strings"
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
func helpView() string {
	var b strings.Builder

	// COMMANDS section
	b.WriteString(sectionEmoji("COMMANDS") + " COMMANDS\n")
	commands := []struct{ cmd, desc string }{
		{"/settings", "open settings panel"},
		{"/copy", "copy transcript to clipboard"},
		{"/login", "interactive provider login"},
		{"/help", "show this help message"},
	}
	for _, c := range commands {
		b.WriteString("  " + commandEmoji(c.cmd) + " " + c.cmd + "  " + c.desc + "\n")
	}

	b.WriteString("\n" + hr() + "\n")
	// KEYBINDINGS section
	b.WriteString(sectionEmoji("KEYBINDINGS") + " KEYBINDINGS\n")
	keybindings := []struct{ key, desc string }{
		{"ctrl+s", "open settings"},
		{"ctrl+o", "copy transcript"},
		{"ctrl+e", "toggle expanded view"},
		{"tab", "toggle thinking"},
		{"shift+enter", "insert newline"},
		{"?", "show help"},
		{"pgup/pgdn", "scroll history"},
		{"ctrl+x", "narrow pane"},
		{"ctrl+z", "widen pane"},
	}
	for _, k := range keybindings {
		b.WriteString("  " + k.key + "  " + k.desc + "\n")
	}

	b.WriteString("\n" + hr() + "\n")
	// CONCEPTS section
	b.WriteString(sectionEmoji("CONCEPTS") + " CONCEPTS\n")
	concepts := []struct{ name, desc string }{
		{"ctrl+e mode", "expand/collapse all tool result cards"},
		{"drag-select", "click and drag to select text"},
		{"right rail", "stats, context, and model info"},
	}
	for _, c := range concepts {
		b.WriteString("  " + c.name + "  " + c.desc + "\n")
	}

	return b.String()
}
