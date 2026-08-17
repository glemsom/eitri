// Package tui provides the interactive fullscreen TUI a developer drives a
// session in. It is built on the Charm stack (Bubble Tea v2 + Lip Gloss v2 +
// Bubbles v2) with Glamour v2 over goldmark for Markdown→ANSI, and renders
// through the alternate screen (T1 pivot, ) so every frame is a
// clean full-surface repaint into the alt buffer.
//
// The TUI sits on the same run engine as batch mode: it reads and writes the
// same session engine and transcript, so a conversation round-trips through
// the shared agent loop.
package tui

import (
	"fmt"
	"image/color"
	"os"
	"regexp"
	"strings"
	"sync"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

// supportedThemes lists the render themes the user can pick from (issue
// #129). These are glamour's built-in styles plus the chrome-only themes
// (nord/gruvbox/solarized) whose markdown body pairs with a glamour style and
// whose hues remap onto the chrome palette.
var supportedThemes = []string{
	"dark", "light", "dracula", "tokyo-night", "pink", "nord", "gruvbox", "solarized",
	"dark-daltonized", "light-daltonized", "notty", "auto",
}

// RenderMarkdown converts Markdown source to ANSI-styled terminal output at
// the given width, using the given theme. It is the Markdown→ANSI seam (Bubble
// Tea + Glamour over goldmark, custom-renderer allowed): a nil error is always
// returned unless the renderer cannot be constructed. An empty or unknown
// theme (including the excluded "ascii") falls back to "dark" and never
// errors .
func RenderMarkdown(md string, width int, theme string) (string, error) {
	if width <= 0 {
		width = 100
	}
	if !isSupportedTheme(theme) {
		theme = config.DefaultTheme
	}
	// The "auto" theme resolves to dark/light by the terminal
	// background, mirroring glamour v1's WithAutoStyle. glamour v2 is pure — it
	// always renders the same output for the same style — and Bubble Tea v2
	// downsamples colors at the output layer, so the v1 WithColorProfile forcing
	// (termenv.ANSI256) is dropped (pass 4, ). The TUI is the only
	// caller and always runs on a color-capable TTY (the boot guard refuses
	// non-interactive contexts), so no notty fallback is needed here.
	if theme == "auto" {
		theme = autoTheme()
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(glamourStyleFor(theme)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "", fmt.Errorf("build markdown renderer: %w", err)
	}
	out, err := r.Render(md)
	if err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	out = remapMarkdownColors(out, themeFor(theme))
	return strings.TrimSuffix(out, "\n"), nil
}

// glamourStyleFor maps a supported theme to the glamour style that renders its
// markdown body. Chrome-only themes (nord/gruvbox/solarized) pair with a
// glamour style of the same brightness family; the chrome palette then re-tints
// the body through remapMarkdownColors, so the pair never drifts.
func glamourStyleFor(theme string) string {
	switch theme {
	case "nord", "gruvbox", "solarized", "dark-daltonized":
		return "dark"
	case "light-daltonized":
		return "light"
	}
	return theme
}

// markdownRemapFor builds the per-theme remap from glamour's fixed 256-color
// semantic indices (per styles/{dark,light}.json) onto the active chrome
// palette's truecolor hues: glamour renders with its own ANSI-256
// indices (heading blue 38;5;39 = #00afff), which clash with the chrome
// palette's truecolor tokens — two blue families on one surface. Remapping the
// semantic indices (heading, link, code, image, …) to the matching chrome hues
// makes markdown and chrome read as one design system under every theme.
// Gray-ramp indices (240/243/244/251/252/187) stay untouched.
func markdownRemapFor(th Theme) map[string]string {
	rgb := func(c color.Color) string {
		r, g, b, _ := c.RGBA() // 16-bit channels per image/color
		return fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8)
	}
	return map[string]string{
		"38;5;30":  "38;2;" + rgb(th.file),   // link teal -> file
		"38;5;35":  "38;2;" + rgb(th.ok),     // h6/link_text green -> ok
		"38;5;39":  "38;2;" + rgb(th.accent), // heading blue -> accent
		"38;5;42":  "38;2;" + rgb(th.ok),     // code green -> ok
		"38;5;48":  "38;2;" + rgb(th.ok),     // name_function green -> ok
		"38;5;203": "38;2;" + rgb(th.error),  // code red -> error
		"38;5;212": "38;2;" + rgb(th.skill),  // image pink -> skill
		"38;5;228": "38;2;" + rgb(th.shell),  // h1 yellow -> shell
		"38;5;27":  "38;2;" + rgb(th.accent), // light heading -> accent
		"38;5;29":  "38;2;" + rgb(th.ok),     // light link_text -> ok
		"38;5;36":  "38;2;" + rgb(th.file),   // light link -> file
		"38;5;205": "38;2;" + rgb(th.skill),  // light image -> skill
	}
}

// sgrParamRe matches a full SGR sequence (ESC [ params m).
var sgrParamRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// remapMarkdownColors rewrites mapped 256-color foreground indices in a
// glamour-rendered string to the given theme's chrome-family truecolor
// equivalents. Unmapped indices and background codes pass through untouched. A
// 38;5;N token becomes 38;2;R;G;B wherever it appears in the params list.
func remapMarkdownColors(s string, th Theme) string {
	m := markdownRemapFor(th)
	if len(m) == 0 {
		return s
	}
	return sgrParamRe.ReplaceAllStringFunc(s, func(seq string) string {
		params := strings.Split(seq[2:len(seq)-1], ";")
		var out []string
		for i := 0; i < len(params); i++ {
			if params[i] == "38" && i+2 < len(params) && params[i+1] == "5" {
				if repl, ok := m["38;5;"+params[i+2]]; ok {
					out = append(out, strings.Split(repl, ";")...)
					i += 2
					continue
				}
			}
			out = append(out, params[i])
		}
		return "\x1b[" + strings.Join(out, ";") + "m"
	})
}

// bubbleBgSGR returns the SGR command that asserts the theme's bubble tint as
// the active background (48;2;R;G;B). It is the token that keeps glamour's
// per-token SGR output seated on the carded fill: glamour emits a fresh
// foreground color plus a full/empty SGR reset for essentially every styled
// run, and that reset also clears any running background. Without a re-assertion
// after each reset, the bubble background only ever shows in the 2-col gutters
// lipgloss paints and the prompt text falls through to the terminal's default
// background — a ragged un-filled card (benchmark §4.1).
func bubbleBgSGR(th Theme) string {
	r, g, b, _ := th.bubble.RGBA() // 16-bit channels per image/color
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// isSGRReset reports whether an SGR params list is a rendition reset — the
// empty list (\x1b[m), explicit 0, or an explicit default-foreground/background
// (39/49) — any of which would clear the carded bubble background if left alone.
// A 39 (default fg) keeps the background, but glamour never emits it in a way
// that matters here; treating it as a reset is harmless and keeps the fill safe.
func isSGRReset(params []string) bool {
	if len(params) == 0 {
		return true
	}
	if len(params) == 1 {
		switch params[0] {
		case "", "0", "39", "49":
			return true
		}
	}
	return false
}

// reattachBubbleBackground rewrites a glamour-rendered markdown block so the
// theme's bubble fill survives its SGR resets: every glamour reset is followed
// by a re-assertion of the bubble background, so text and inline styled runs all
// sit on the carded fill instead of default terminal background. It runs only on
// the user-prompt card (the agent pane has no Background and must stay as-is).
func reattachBubbleBackground(s string, th Theme) string {
	bg := bubbleBgSGR(th)
	return sgrParamRe.ReplaceAllStringFunc(s, func(seq string) string {
		params := strings.SplitN(seq[2:len(seq)-1], ";", -1)
		if !isSGRReset(params) {
			return seq
		}
		return seq + bg
	})
}

// renderUserPromptCard renders a user prompt as the carded bubble. glamour's
// per-token SGR resets would clear the card's Background, so reattachBubbleBackground
// re-asserts the bubble tint after every reset. glamour also pre-pads rows and
// lipgloss's Width() alignment then emits a couple of unstyled trailing cells at
// the right edge, so every glamour row is padded to the content width (w-4) with
// the bubble background and Width() is not used — lipgloss's 2-col padding closes
// the box with no extra width-fill left to trip on (benchmark §4.1).
func renderUserPromptCard(th Theme, md string, w int) string {
	out := reattachBubbleBackground(md, th)
	bg := bubbleBgSGR(th)
	cw := w - 4 // bubble content width: Width(w) minus 2-left + 2-right padding
	if cw > 0 {
		var lines []string
		for _, ln := range strings.Split(out, "\n") {
			pad := cw - lipgloss.Width(ln)
			if pad > 0 {
				ln += bg + strings.Repeat(" ", pad)
			}
			lines = append(lines, ln)
		}
		out = strings.Join(lines, "\n")
	}
	return th.userBubbleStyle.Render(strings.TrimRight(out, "\n"))
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
