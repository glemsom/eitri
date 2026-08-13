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

// supportedThemes lists the render themes the user can pick from (issue
// #129). These are glamour's built-in styles minus "ascii", which is
// deliberately excluded.
var supportedThemes = []string{
	"dark", "light", "dracula", "tokyo-night", "pink", "notty", "auto",
}

// RenderMarkdown converts Markdown source to ANSI-styled terminal output at
// the given width, using the given theme. It is the Markdown→ANSI seam (Bubble
// Tea + Glamour over goldmark, custom-renderer allowed): a nil error is always
// returned unless the renderer cannot be constructed. An empty or unknown
// theme (including the excluded "ascii") falls back to "dark" and never
// errors (issue #129).
func RenderMarkdown(md string, width int, theme string) (string, error) {
	if width <= 0 {
		width = 100
	}
	if !isSupportedTheme(theme) {
		theme = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(theme),
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

// isSupportedTheme reports whether theme is one of the 7 supported render
// themes. Anything else — empty, "ascii", or an unknown name — is rejected so
// the caller can fall back to "dark" without glamour erroring (glamour's
// WithStylePath treats an unknown name as a file path and fails).
func isSupportedTheme(theme string) bool {
	for _, t := range supportedThemes {
		if t == theme {
			return true
		}
	}
	return false
}
