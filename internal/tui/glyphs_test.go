package tui

import (
	"context"
	"strings"
	"testing"
)

// TestGlyph_charter asserts the decorative glyph charter (benchmark §3.6/§4.3):
// every non-ASCII glyph has an ASCII fallback selected under
// EITRI_ASCII_GLYPHS (or a non-UTF-8 locale), and the UTF-8 glyph otherwise.
func TestGlyph_charter(t *testing.T) {
	cases := []struct{ utf8, ascii string }{
		{"⊕", "+"}, {"✓", "ok"}, {"✗", "X"}, {"🤔", "?"}, {"▸", ">"},
		{"▶", ">"}, {"─", "-"}, {"·", "."}, {"…", "..."}, {"│", "|"}, {"−", "-"},
	}
	// No env override: the UTF-8 glyph (the test locale supports UTF-8).
	for _, c := range cases {
		if got := g(c.utf8, c.ascii); got != c.utf8 {
			t.Errorf("g(%q,%q) without override = %q, want %q", c.utf8, c.ascii, got, c.utf8)
		}
	}
	// Forced ASCII fallback.
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, c := range cases {
		if got := g(c.utf8, c.ascii); got != c.ascii {
			t.Errorf("g(%q,%q) with override = %q, want %q", c.utf8, c.ascii, got, c.ascii)
		}
	}
}

// TestToolEntry_asciiGlyphs asserts a whole tool entry degrades: the label
// becomes "+ name", the outcome "ok"/"X", and the pane border "|" — no
// non-ASCII glyph leaks under the forced fallback.
func TestToolEntry_asciiGlyphs(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Tools: NewToolFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "ok (1ms)", Lines: 1})

	content := plain(view(m))
	if !strings.Contains(content, "+ bash") {
		t.Errorf("ASCII tool label missing, got: %q", content)
	}
	if strings.Contains(content, "⊕") || strings.Contains(content, "✓") || strings.Contains(content, "│") {
		t.Errorf("non-ASCII tool glyphs leaked under fallback, got: %q", content)
	}
	if !strings.Contains(content, " ok") {
		t.Errorf("ASCII outcome marker missing, got: %q", content)
	}
}
