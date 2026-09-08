// Package tui provides the interactive fullscreen TUI a developer drives a session in.
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

var supportedThemes = []string{
	"dark", "light", "dracula", "tokyo-night", "pink", "nord", "gruvbox", "solarized",
	"dark-daltonized", "light-daltonized", "notty", "auto",
}

type markdownRendererCacheKey struct {
	theme string
	width int
}

type cachedMarkdownRenderer struct {
	mu sync.Mutex
	r  *glamour.TermRenderer
}

var markdownRendererCache sync.Map

// RenderMarkdown converts Markdown source to ANSI-styled terminal output at the given width, using the given theme.
func RenderMarkdown(md string, width int, theme string) (string, error) {
	if width <= 0 {
		width = 100
	}
	if !isSupportedTheme(theme) {
		theme = config.DefaultTheme
	}
	if theme == "auto" {
		theme = autoTheme()
	}
	r, err := markdownRendererFor(markdownRendererCacheKey{theme: theme, width: width})
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	out, err := r.r.Render(md)
	r.mu.Unlock()
	if err != nil {
		return "", fmt.Errorf("render markdown: %w", err)
	}
	out = remapMarkdownColors(out, themeFor(theme))
	return strings.TrimSuffix(out, "\n"), nil
}

func markdownRendererFor(key markdownRendererCacheKey) (*cachedMarkdownRenderer, error) {
	if r, ok := markdownRendererCache.Load(key); ok {
		return r.(*cachedMarkdownRenderer), nil
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(glamourStyleFor(key.theme)),
		glamour.WithWordWrap(key.width),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil, fmt.Errorf("build markdown renderer: %w", err)
	}
	cached := &cachedMarkdownRenderer{r: r}
	actual, _ := markdownRendererCache.LoadOrStore(key, cached)
	return actual.(*cachedMarkdownRenderer), nil
}

// glamourStyleFor maps a supported theme to the glamour style that renders its markdown body.
func glamourStyleFor(theme string) string {
	switch theme {
	case "nord", "gruvbox", "solarized", "dark-daltonized":
		return "dark"
	case "light-daltonized":
		return "light"
	}
	return theme
}

// markdownRemapFor builds the per-theme remap from glamour's fixed 256-color semantic indices (per styles/{dark,light}.json) onto the active chrome palette's truecolor hues: glamour renders with its own ANSI-256 indices (heading blue 38;5;39 = #00afff), which clash with the chrome palette's truecolor tokens — two blue families on one surface.
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

// sgrParamRe matches a full SGR sequence (ESC [ params m). Used by
// reattachBubbleBackground, which rewrites every SGR so a fast-path is unnecessary.
var sgrParamRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// remapMarkdownColors rewrites mapped 256-color foreground indices in a glamour-rendered string to the given theme's chrome-family truecolor equivalents.
//
// This runs once per markdown render inside the streaming live tail (every delta
// invalidates the block cache), so it is deliberately allocation-light. It does a
// single manual pass hunting for ESC [ … m sequences and fast-paths past any
// sequence that carries no foreground 256-color index: such a sequence is copied
// byte-for-byte instead of being split, reassembled, and re-joined. Only a
// sequence that actually contains a mapped `38;5;N` index is rewritten. The old
// regexp-based ReplaceAllStringFunc rebuilt every SGR — split + join per sequence,
// plus a full regexp scan of the whole string — turning one 8KiB block's remap
// into ~28ms and pinning a core during long streaming reasoning.
func remapMarkdownColors(s string, th Theme) string {
	m := markdownRemapFor(th)
	if len(m) == 0 || !strings.Contains(s, "38;5;") {
		return s
	}

	if strings.IndexByte(s, 0x1b) < 0 {
		return s
	}
	b := strings.Builder{}
	b.Grow(len(s))
	for len(s) > 0 {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := i + 1
		// Mirror sgrParamRe exactly: ESC [ params m where params are digits and
		// semicolons. A bare ESC or a sequence that strays outside the [0-9;]
		// charset is passed through byte-by-byte so malformed-input behavior is
		// unchanged from the regexp version.
		if j >= len(s) || s[j] != '[' {
			b.WriteByte(0x1b)
			s = s[j:]
			continue
		}
		j++
		start := j
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';') {
			j++
		}
		if j >= len(s) || s[j] != 'm' {
			b.WriteByte(0x1b)
			s = s[i+1:]
			continue
		}
		seq := s[i : j+1] // "\x1b[paramsm" — params run [start, j)
		// Only a params substring that actually carries a mapped foreground
		// 256-color index is rewritten; any other SGR (bold, bg, classic fg,
		// reset) is copied verbatim instead of being pulled apart and rebuilt.
		if strings.Contains(s[start:j], "38;5;") {
			b.WriteString(remapSGR(seq, m))
		} else {
			b.WriteString(seq)
		}
		s = s[j+1:]
	}
	return b.String()
}

// remapSGR rewrites every `38;5;<index>` foreground spec inside one SGR sequence
// to the mapped truecolor equivalent (or leaves it in place when unmapped). It
// scans the params substring token-by-token (no allocation for Split/Join) to
// keep the streaming render path cheap: this runs on every glamour render, and
// the prior Split/Join implementation was the dominant allocator in a live
// TUI CPU/heap profile.
func remapSGR(seq string, m map[string]string) string {
	sub := seq[2 : len(seq)-1]
	var b strings.Builder
	b.Grow(len(seq) + 16)
	b.WriteByte(0x1b)
	b.WriteByte('[')
	for i := 0; i < len(sub); {
		// token [i,j)
		j := i
		for j < len(sub) && sub[j] != ';' {
			j++
		}
		tok := sub[i:j]
		if tok == "38" {
			// Look ahead for the `5;<index>` part of a possible `38;5;<index>` triple.
			n1s := j + 1
			n1e := n1s
			for n1e < len(sub) && sub[n1e] != ';' {
				n1e++
			}
			if n1s < len(sub) && sub[n1s:n1e] == "5" {
				n2s := n1e + 1
				n2e := n2s
				for n2e < len(sub) && sub[n2e] != ';' {
					n2e++
				}
				if n2s < len(sub) {
					if repl, ok := m["38;5;"+sub[n2s:n2e]]; ok {
						b.WriteString(repl)
						if n2e < len(sub) {
							b.WriteByte(';')
						}
						i = n2e + 1
						continue
					}
				}
			}
		}
		b.WriteString(tok)
		if j < len(sub) {
			b.WriteByte(';')
		}
		i = j + 1
	}
	b.WriteByte('m')
	return b.String()
}

// bubbleBgSGR returns the SGR command that asserts the theme's bubble tint as the active background (48;2;R;G;B).
func bubbleBgSGR(th Theme) string {
	r, g, b, _ := th.bubble.RGBA() // 16-bit channels per image/color
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// isSGRReset reports whether an SGR params list is a rendition reset — the empty list (\x1b[m), explicit 0, or an explicit default-foreground/background (39/49) — any of which would clear the carded bubble background if left alone.
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

// reattachBubbleBackground rewrites a glamour-rendered markdown block so the theme's bubble fill survives both SGR resets and glamour's per-span background colors. Two failure modes are covered: (1) every glamour reset is followed by a re-assertion of the bubble background so unstyled text sits on the carded fill instead of the default terminal background; (2) any SGR that carries a background-color param (`48;5;X` or `48;2;R;G;B`) is rewritten to the bubble tint, so glamour's inline `code` style — which emits `38;2;...;48;5;236` in the dark theme and `48;5;254` in the light theme — no longer paints a contrasting patch inside the user card. Without (2) the inline code cells render as bright text on a slightly-darker fill, which the eye reads as a colored block inside the bubble.
func reattachBubbleBackground(s string, th Theme) string {
	bg := bubbleBgSGR(th)
	// bg is "\x1b[48;2;R;G;Bm"; bubbleBgParams is the inside ("48;2;R;G;B")
	// we splice into any SGR that already carries a background-color param.
	bubbleBgParams := strings.TrimSuffix(strings.TrimPrefix(bg, "\x1b["), "m")
	return sgrParamRe.ReplaceAllStringFunc(s, func(seq string) string {
		params := strings.SplitN(seq[2:len(seq)-1], ";", -1)
		if isSGRReset(params) {
			return seq + bg
		}
		// Rewrite any background-color param to the bubble tint; keep the
		// rest of the SGR (fg color, bold, etc.) intact.
		var out []string
		rewrote := false
		for i := 0; i < len(params); {
			if params[i] == "48" && i+1 < len(params) {
				switch {
				case params[i+1] == "5" && i+2 < len(params):
					out = append(out, bubbleBgParams)
					i += 3
					rewrote = true
					continue
				case params[i+1] == "2" && i+4 < len(params):
					out = append(out, bubbleBgParams)
					i += 5
					rewrote = true
					continue
				}
			}
			out = append(out, params[i])
			i++
		}
		if !rewrote {
			return seq
		}
		return "\x1b[" + strings.Join(out, ";") + "m"
	})
}

// RenderPromptMarkdown returns a user-entered prompt for the prompt card while preserving the literal text the user submitted. Assistant text renders as Markdown, but the prompt echo is a record of input; rendering it through glamour would turn Markdown-looking input such as "- [ ] task" into a list item and drop the leading dash.
func RenderPromptMarkdown(prompt string, width int, theme string) (string, error) {
	return lipgloss.NewStyle().Width(width).Render(prompt), nil
}

// renderUserPromptCard renders a user prompt as the carded bubble. glamour's per-token SGR resets would clear the card's Background, so reattachBubbleBackground re-asserts the bubble tint after every reset. glamour also pre-pads rows and lipgloss's Width() alignment then emits a couple of unstyled trailing cells at the right edge, so every glamour row is padded to the content width (w-4) with the bubble background and Width() is not used — lipgloss's 2-col padding closes the box with no extra width-fill left to trip on (benchmark §4.1).
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

// autoTheme resolves the "auto" theme once and caches it: background detection queries the terminal, and RenderMarkdown runs per frame during a stream, so the query must not repeat every render (termenv v1 cached the same detection internally).
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

// isSupportedTheme reports whether theme is one of the 7 supported render themes.
func isSupportedTheme(theme string) bool {
	for _, t := range supportedThemes {
		if t == theme {
			return true
		}
	}
	return false
}
