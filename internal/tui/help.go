package tui

import (
	"strings"
)

// helpView renders a formatted, sectioned help message with accent headers and
// faint body text. Pure function — produces content only, no wiring to the TUI
// event loop.
func helpView(th Theme) string {
	var b strings.Builder

	// COMMANDS section
	b.WriteString(th.headerStyle.Render(sectionEmoji("COMMANDS") + " COMMANDS"))
	b.WriteString("\n")
	commands := []struct{ cmd, desc string }{
		{"/settings", "open settings panel"},
		{"/copy", "copy transcript to clipboard"},
		{"/login", "interactive provider login"},
		{"/help", "show this help message"},
	}
	for _, c := range commands {
		b.WriteString(th.statusStyle.Render("  " + commandEmoji(c.cmd) + " " + c.cmd + "  " + c.desc))
		b.WriteString("\n")
	}

	b.WriteString("\n" + hr() + "\n")
	// KEYBINDINGS section
	b.WriteString(th.headerStyle.Render(sectionEmoji("KEYBINDINGS") + " KEYBINDINGS"))
	b.WriteString("\n")
	keybindings := []struct{ key, desc string }{
		{"ctrl+s", "open settings"},
		{"ctrl+o", "copy transcript"},
		{"ctrl+e", "toggle expanded view"},
		{"tab", "toggle thinking"},
		{"shift+enter", "insert newline"},
		{"?", "show help"},
		{"pgup/pgdn", "scroll history"},
		{"ctrl+z", "narrow pane"},
		{"ctrl+x", "widen pane"},
	}
	for _, k := range keybindings {
		b.WriteString(th.statusStyle.Render("  " + k.key + "  " + k.desc))
		b.WriteString("\n")
	}

	b.WriteString("\n" + hr() + "\n")
	// CONCEPTS section
	b.WriteString(th.headerStyle.Render(sectionEmoji("CONCEPTS") + " CONCEPTS"))
	b.WriteString("\n")
	concepts := []struct{ name, desc string }{
		{"ctrl+e mode", "expand/collapse all tool result cards"},
		{"drag-select", "click and drag to select text"},
		{"right rail", "stats, context, and model info"},
	}
	for _, c := range concepts {
		b.WriteString(th.statusStyle.Render("  " + c.name + "  " + c.desc))
		b.WriteString("\n")
	}

	return b.String()
}
