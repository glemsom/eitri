package tui

import (
	"strings"
	"testing"
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

func hasSGRBold(s string) bool {
	return strings.Contains(s, ";1m") || strings.Contains(s, "\x1b[1m")
}

func hasBullet(s string) bool {
	return containsSeq(s, "- ") || containsSeq(s, "\u2022 ") || containsSeq(s, "\x1b[9")
}
