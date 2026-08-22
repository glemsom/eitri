package tui

import (
	"context"
	"strings"
	"testing"
)

func TestGlyph_charter(t *testing.T) {
	cases := []struct{ utf8, ascii string }{
		{"⊕", "+"}, {"✓", "ok"}, {"✗", "X"}, {"🤔", "?"}, {"▸", ">"},
		{"▶", ">"}, {"─", "-"}, {"·", "."}, {"…", "..."}, {"│", "|"}, {"−", "-"},
		{"⚒", "+"}, {"──", "--"}, {"💬", ">"}, {"⌨", "k"}, {"⚙", "*"}, {"📋", "#"}, {"🔑", "+"}, {"❓", "?"},
	}
	for _, c := range cases {
		if got := g(c.utf8, c.ascii); got != c.utf8 {
			t.Errorf("g(%q,%q) without override = %q, want %q", c.utf8, c.ascii, got, c.utf8)
		}
	}
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, c := range cases {
		if got := g(c.utf8, c.ascii); got != c.ascii {
			t.Errorf("g(%q,%q) with override = %q, want %q", c.utf8, c.ascii, got, c.ascii)
		}
	}
}

func TestToolGlyph_charter(t *testing.T) {
	cases := []struct {
		name  string
		utf8  string
		ascii string
	}{
		{"bash", "🔧", "$"},
		{"read", "📖", "<"},
		{"write", "✏️", ">"},
		{"edit", "✂️", "~"},
		{"web_fetch", "🌐", "w"},
		{"open_in_browser", "🌍", "W"},
		{"skill", "⚡", "s"},
		{"unknown", "⊕", "+"},
	}
	for _, c := range cases {
		if got := toolGlyph(c.name); got != c.utf8 {
			t.Errorf("toolGlyph(%q) without override = %q, want %q", c.name, got, c.utf8)
		}
	}
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, c := range cases {
		if got := toolGlyph(c.name); got != c.ascii {
			t.Errorf("toolGlyph(%q) with override = %q, want %q", c.name, got, c.ascii)
		}
	}
}

func TestToolEntry_asciiGlyphs(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "ok (1ms)", Lines: 1})

	content := plain(view(m))
	if !strings.Contains(content, "$ bash") {
		t.Errorf("ASCII tool label missing, got: %q", content)
	}
	if strings.Contains(content, "⊕") || strings.Contains(content, "✓") || strings.Contains(content, "│") {
		t.Errorf("non-ASCII tool glyphs leaked under fallback, got: %q", content)
	}
	if !strings.Contains(content, " ok") {
		t.Errorf("ASCII outcome marker missing, got: %q", content)
	}
}
