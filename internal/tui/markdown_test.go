package tui

import (
	"image/color"
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestRenderMarkdown_representativeBlocks(t *testing.T) {
	t.Parallel()
	in := "This is **bold** text.\n\n- first item\n- second item\n\n" +
		"```go\nfunc main() {}\n```"
	out, err := RenderMarkdown(in, 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	if !hasSGRBold(out) {
		t.Errorf("expected bold emphasis (SGR 1) in output, got: %q", out)
	}
	if !containsSeq(out, "\x1b[38;5") && !containsClassicColor(out) {
		t.Errorf("expected code-block foreground styling in output, got: %q", out)
	}
	if !hasBullet(out) {
		t.Errorf("expected a list bullet ('- ') in output, got: %q", out)
	}
}

func TestRenderMarkdown_allSupportedThemes(t *testing.T) {
	t.Parallel()
	in := "# Heading\n\nSome **bold** and `code` text.\n"
	for _, theme := range []string{"dark", "light", "dracula", "tokyo-night", "pink", "notty", "auto"} {
		out, err := RenderMarkdown(in, 80, theme)
		if err != nil {
			t.Fatalf("RenderMarkdown(theme=%q): %v", theme, err)
		}
		if strings.TrimSpace(out) == "" {
			t.Fatalf("RenderMarkdown(theme=%q) rendered empty output", theme)
		}
	}
}

func TestRenderMarkdown_emptyThemeIsDark(t *testing.T) {
	t.Parallel()
	in := "# Heading\n\nSome **bold** text.\n"
	dark, err := RenderMarkdown(in, 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown(dark): %v", err)
	}
	empty, err := RenderMarkdown(in, 80, "")
	if err != nil {
		t.Fatalf("RenderMarkdown(empty): %v", err)
	}
	if empty != dark {
		t.Fatalf("RenderMarkdown(\"\") = %q, want dark output %q", empty, dark)
	}
}

func TestRenderMarkdown_invalidThemeFallsBackToDark(t *testing.T) {
	t.Parallel()
	in := "# Heading\n\nSome **bold** text.\n"
	dark, err := RenderMarkdown(in, 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown(dark): %v", err)
	}
	for _, theme := range []string{"bogus", "ascii", "DARK", ""} {
		out, err := RenderMarkdown(in, 80, theme)
		if err != nil {
			t.Fatalf("RenderMarkdown(theme=%q) errored: %v, want dark fallback", theme, err)
		}
		if out != dark {
			t.Fatalf("RenderMarkdown(theme=%q) = %q, want dark fallback %q", theme, out, dark)
		}
	}
}

func containsSeq(s, seq string) bool {
	for i := 0; i+len(seq) <= len(s); i++ {
		if s[i:i+len(seq)] == seq {
			return true
		}
	}
	return false
}

func containsClassicColor(s string) bool {
	for _, c := range []string{"\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[34m", "\x1b[35m", "\x1b[36m", "\x1b[37m"} {
		if containsSeq(s, c) {
			return true
		}
	}
	return false
}

// TestRemapMarkdownColors_matchesReference guards the fast-path rewrite of
// remapMarkdownColors: the streaming live tail re-renders the reasoning/answer
// block every delta, and the old regexp-based implementation rebuilt every SGR
// (split + join per sequence) even when it carried no mapped 256-color index —
// ~28ms for one 8KiB block, pinning a core during long streaming (render
// diagnostics). The manual scanner is only correct if it emits byte-identical
// output to the reference implementation on every theme, well-formed input, and
// malformed input.
func TestRemapMarkdownColors_matchesReference(t *testing.T) {
	t.Parallel()
	for _, theme := range []string{"dark", "light", "dracula", "tokyo-night", "pink", "nord", "gruvbox", "solarized", "dark-daltonized", "light-daltonized", "notty"} {
		th := themeFor(theme)
		inputs := []string{
			"# H **b** `c` *i*",
			"## heading `code` and **bold** and normal tokens\nX\n",
			"", "plain no escapes",
			"\x1b[1m\x1b[38;5;39mhi\x1b[0m",
			"\x1b[38;5;30mlink\x1b[m", "\x1b", "\x1bX", "\x1b[", "\x1b[999m",
			"a\x1b[38;5;27;1mb\x1b[0m", "\x1b[38;5;999mc", // unmapped index & index+bold
		}
		for _, in := range inputs {
			s, err := RenderMarkdown(in, 100, theme)
			if err != nil {
				t.Fatalf("RenderMarkdown(theme=%q): %v", theme, err)
			}
			if got, want := remapMarkdownColors(s, th), remapMarkdownColorsReference(s, th); got != want {
				t.Errorf("theme=%q input=%q\n got=%q\nwant=%q", theme, in, got, want)
			}
		}
	}
}

// remapMarkdownColorsReference is the pre-optimization implementation, kept as
// the authoritative spec of the rewrite so the fast path can be regression-tested
// against it.
func remapMarkdownColorsReference(s string, th Theme) string {
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

func hasSGRBold(s string) bool {
	return strings.Contains(s, ";1m") || strings.Contains(s, "\x1b[1m")
}

func hasBullet(s string) bool {
	return containsSeq(s, "- ") || containsSeq(s, "\u2022 ") || containsSeq(s, "\x1b[9")
}

// TestRenderMarkdown_noUnmanagedBaseText asserts that every supported theme
// maps glamour's base body text indices (252 for dark, 234 for light) and
// related structural indices onto the theme's text token, so no rendered
// markdown color sits outside the active palette.
func TestRenderMarkdown_noUnmanagedBaseText(t *testing.T) {
	t.Parallel()
	in := "Hello world, a short line.\n\n- first item\n- second item\n\n> blockquote\n\n---\n\n`code`\n\n```go\nfunc main() {}\n```\n\n[link](http://x.com)\n\n![img](http://x.com/a.png)\n"
	for _, theme := range []string{"dark", "light", "dracula", "tokyo-night", "pink", "nord", "gruvbox", "solarized", "dark-daltonized", "light-daltonized", "notty", "auto"} {
		out, err := RenderMarkdown(in, 40, theme)
		if err != nil {
			t.Fatalf("RenderMarkdown(theme=%q): %v", theme, err)
		}
		for _, unmanaged := range []string{"\x1b[38;5;252m", "\x1b[38;5;234m"} {
			if strings.Contains(out, unmanaged) {
				t.Errorf("theme=%q: output contains unmanaged base-text %q", theme, unmanaged)
			}
		}
	}
}

// TestTheme_textContrast asserts that each theme's body text color has a
// WCAG 2.1 contrast ratio of at least 4.5:1 against the theme's intended
// terminal background.
func TestTheme_textContrast(t *testing.T) {
	t.Parallel()
	backgrounds := map[string]color.Color{
		"dark":             hexColor("#1A1B26"),
		"dracula":          hexColor("#282A36"),
		"tokyo-night":      hexColor("#1A1B26"),
		"pink":             hexColor("#1A1018"),
		"light":            hexColor("#FFFFFF"),
		"nord":             hexColor("#2E3440"),
		"gruvbox":          hexColor("#282828"),
		"solarized":        hexColor("#002B36"),
		"dark-daltonized":  hexColor("#1A1B26"),
		"light-daltonized": hexColor("#FFFFFF"),
	}
	for name, bg := range backgrounds {
		th := themeFor(name)
		got := contrastRatio(th.text, bg)
		if got < 4.5 {
			t.Errorf("theme=%q text contrast = %.2f:1, want >= 4.5:1", name, got)
		}
	}
}

func hexColor(s string) color.Color {
	c := lipgloss.Color(s)
	r, g, b, _ := c.RGBA()
	return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 255}
}

func contrastRatio(a, b color.Color) float64 {
	l1 := relativeLuminance(a)
	l2 := relativeLuminance(b)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	// image/color returns 16-bit channels; convert to 8-bit sRGB.
	rs := float64(r>>8) / 255.0
	gs := float64(g>>8) / 255.0
	bs := float64(b>>8) / 255.0
	return 0.2126*linearize(rs) + 0.7152*linearize(gs) + 0.0722*linearize(bs)
}

func linearize(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}
