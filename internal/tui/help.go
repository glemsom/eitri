package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// helpView renders a formatted, sectioned help message as escape-free plain
// text using Markdown syntax the transcript's Markdown→ANSI pass already
// understands (issue #387): `#` headers for section titles (rendered in the
// theme's accent color) and backtick code spans around command/key names
// (rendered with code styling, distinct from their descriptions) — while the
// KEYBINDINGS categories (issue #386) keep their grouped glyph labels. Pure
// function — produces content only, no wiring to the TUI event loop.
//
// The help content is deliberately stored as escape-free plain Markdown
// (issue #378) rather than ANSI-styled runs: the transcript runs every message
// through the Markdown→ANSI render pass, which would split and re-escape
// embedded escapes into literal `1;38;...` garbage on screen, and the
// clipboard path (transcriptText) would copy that raw escape junk. Storing
// escape-free Markdown is the single root fix covering both the on-screen
// display and the copy path, while the render pass supplies the styling.
// helpRow is one aligned line of the help output: a left key/label cell and a
// description. helpView pads each left cell to the section's widest so every
// description starts on a shared vertical ruler (issue #385).
type helpRow struct {
	key  string
	desc string
}

func helpView() string {
	var b strings.Builder

	// COMMANDS section.
	b.WriteString("# COMMANDS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`/settings`", "open settings panel"},
		{"`/copy`", "copy transcript to clipboard"},
		{"`/login`", "interactive provider login"},
		{"`/help`", "show this help message"},
	})

	// KEYBINDINGS section, grouped under labeled category sub-headers (issue
	// #386) so a user scanning /help can jump straight to the kind of action
	// they need (composing, navigating, resizing panes, or app actions).
	b.WriteString("\n# KEYBINDINGS\n\n")
	writeHelpCategory(&b, "COMPOSER", []helpRow{
		{"`tab`", "toggle thinking"},
		{"`shift+enter`", "insert newline"},
	})
	writeHelpCategory(&b, "NAVIGATION", []helpRow{
		{"`?`", "show help"},
		{"`pgup/pgdn`", "scroll history"},
	})
	writeHelpCategory(&b, "PANES", []helpRow{
		{"`ctrl+e`", "toggle expanded view"},
		{"`ctrl+x`", "narrow pane"},
		{"`ctrl+z`", "widen pane"},
	})
	writeHelpCategory(&b, "ACTIONS", []helpRow{
		{"`ctrl+s`", "open settings"},
		{"`ctrl+o`", "copy transcript"},
	})

	// CONCEPTS section.
	b.WriteString("\n# CONCEPTS\n\n")
	writeHelpRows(&b, []helpRow{
		{"`ctrl+e mode`", "expand/collapse all tool result cards"},
		{"`drag-select`", "click and drag to select text"},
		{"`right rail`", "stats, context, and model info"},
	})

	return b.String()
}

// writeHelpCategory writes one labeled KEYBINDINGS category: an indented
// emoji-prefixed category header followed by its aligned rows (issue #386).
// Each category aligns its own column, so descriptions align within a category
// while the category label gives a user a place to jump to.
func writeHelpCategory(b *strings.Builder, name string, rows []helpRow) {
	b.WriteString("  " + categoryEmoji(name) + " " + name + "\n")
	writeHelpRows(b, rows)
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
