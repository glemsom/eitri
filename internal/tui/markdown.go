// Package tui provides the interactive fullscreen TUI a developer drives a
// session in. It is built on the Charm stack (Bubble Tea + Lip Gloss +
// Bubbles) with Glamour over goldmark for Markdown→ANSI, and renders into the
// primary (normal) buffer using a differential renderer so the terminal's
// native selection, scrollback, and search are preserved (docs/spec.md §9,
// ticket #34).
//
// The TUI sits on the same run engine as batch mode: it reads and writes the
// same session engine and transcript, so a conversation round-trips through
// the shared agent loop.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/muesli/termenv"
)

// RenderMarkdown converts Markdown source to ANSI-styled terminal output at
// the given width. It is the Markdown→ANSI seam (Bubble Tea + Glamour over
// goldmark, custom-renderer allowed): a nil error is always returned unless
// the renderer cannot be constructed.
func RenderMarkdown(md string, width int) (string, error) {
	if width <= 0 {
		width = 100
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath("dark"),
		// Force a color profile: in a non-TTY/test sink glamour defaults to
		// no-color, but the TUI always renders to a color-capable terminal
		// (spec §9, Ghostty primary). ANSI-256 is the portable floor.
		glamour.WithColorProfile(termenv.ANSI256),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", fmt.Errorf("build markdown renderer: %w", err)
	}
	out, err := r.Render(md)
	if err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	return strings.TrimSuffix(out, "\n"), nil
}
