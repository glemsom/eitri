// Package tui provides the interactive fullscreen TUI a developer drives a
// session in. It is built on the Charm stack (Bubble Tea v2 + Lip Gloss v2 +
// Bubbles v2) with Glamour v2 over goldmark for Markdown→ANSI, and renders
// through the alternate screen (T1 pivot, issue #119) so every frame is a
// clean full-surface repaint into the alt buffer (docs/spec.md §9).
//
// The TUI sits on the same run engine as batch mode: it reads and writes the
// same session engine and transcript, so a conversation round-trips through
// the shared agent loop.
package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
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
		theme = config.DefaultTheme
	}
	// The "auto" theme (issue #129) resolves to dark/light by the terminal
	// background, mirroring glamour v1's WithAutoStyle. glamour v2 is pure — it
	// always renders the same output for the same style — and Bubble Tea v2
	// downsamples colors at the output layer, so the v1 WithColorProfile forcing
	// (termenv.ANSI256) is dropped (pass 4, issue #148). The TUI is the only
	// caller and always runs on a color-capable TTY (the boot guard refuses
	// non-interactive contexts), so no notty fallback is needed here.
	if theme == "auto" {
		theme = autoTheme()
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(theme),
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

// autoTheme resolves the "auto" theme once and caches it: background
// detection queries the terminal, and RenderMarkdown runs per frame during a
// stream, so the query must not repeat every render (termenv v1 cached the
// same detection internally). Detection happens on first use — always inside
// the interactive TUI, which the boot guard restricts to a real terminal.
var (
	autoThemeOnce     sync.Once
	autoThemeResolved string
)

func autoTheme() string {
	autoThemeOnce.Do(func() {
		if lipgloss.HasDarkBackground(os.Stdin, os.Stdout) {
			autoThemeResolved = "dark"
		} else {
			autoThemeResolved = "light"
		}
	})
	return autoThemeResolved
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
