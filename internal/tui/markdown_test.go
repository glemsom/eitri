package tui

import (
	"strings"
	"testing"
)

// TestRenderMarkdown_representativeBlocks renders representative Markdown
// blocks (lists, code, bold) and asserts the ANSI output carries the expected
// terminal styles for each (ticket #34), via the engine-render seam.
func TestRenderMarkdown_representativeBlocks(t *testing.T) {
	in := "This is **bold** text.\n\n- first item\n- second item\n\n" +
		"```go\nfunc main() {}\n```"
	out, err := RenderMarkdown(in, 80, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	// Bold must carry explicit bold emphasis (SGR 1) on the wire, wherever the
	// color/style sequence places it (e.g. \x1b[38;5;252;1m).
	if !hasSGRBold(out) {
		t.Errorf("expected bold emphasis (SGR 1) in output, got: %q", out)
	}
	// A code block must carry at least one color SGR sequence (the syntax
	// highlight), i.e. some ESC[38;5/... or ESC[3...m foreground.
	if !containsSeq(out, "\x1b[38;5") && !containsClassicColor(out) {
		t.Errorf("expected code-block foreground styling in output, got: %q", out)
	}
	// A list must render a bullet glyph.
	if !hasBullet(out) {
		t.Errorf("expected a list bullet ('- ') in output, got: %q", out)
	}
}

// TestRenderMarkdown_allSupportedThemes renders a representative Markdown
// sample with each of the 7 supported themes and asserts the
// renderer never errors, so a user-selected theme always renders. "ascii" is
// deliberately excluded from the supported set.
func TestRenderMarkdown_allSupportedThemes(t *testing.T) {
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

// TestRenderMarkdown_emptyThemeIsDark verifies an empty theme renders exactly
// as the default dark theme : an unset config key must not change
// rendering.
func TestRenderMarkdown_emptyThemeIsDark(t *testing.T) {
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

// TestRenderMarkdown_invalidThemeFallsBackToDark verifies an unknown or
// excluded theme value renders as dark without ever returning an error (issue
// #129): "bogus" is not a glamour style and "ascii" is deliberately excluded.
func TestRenderMarkdown_invalidThemeFallsBackToDark(t *testing.T) {
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

// containsSeq reports whether s contains the literal escape-sequence substring.
func containsSeq(s, seq string) bool {
	for i := 0; i+len(seq) <= len(s); i++ {
		if s[i:i+len(seq)] == seq {
			return true
		}
	}
	return false
}

// containsClassicColor reports a classic (3-bit/4-bit) foreground color SGR.
func containsClassicColor(s string) bool {
	for _, c := range []string{"\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[34m", "\x1b[35m", "\x1b[36m", "\x1b[37m"} {
		if containsSeq(s, c) {
			return true
		}
	}
	return false
}

// hasSGRBold reports whether any SGR sequence sets bold (attribute 1). The
// dark style emits it as the trailing \;1m in a color+attribute sequence (e.g.
// \x1b[38;5;252;1m) or as \x1b[1m alone.
func hasSGRBold(s string) bool {
	return strings.Contains(s, ";1m") || strings.Contains(s, "\x1b[1m")
}

// hasBullet reports whether the rendered list shows a bullet glyph.
func hasBullet(s string) bool {
	return containsSeq(s, "- ") || containsSeq(s, "\u2022 ") || containsSeq(s, "\x1b[9")
}
