package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestGlyphInventory_charter(t *testing.T) {
	for name, ent := range glyphInventory {
		if ent.ascii == "" {
			t.Errorf("glyphInventory[%q] has empty ASCII fallback", name)
		}
		if got := g(ent.utf8, ent.ascii); got != ent.utf8 {
			t.Errorf("g(%q,%q) without override = %q, want %q", ent.utf8, ent.ascii, got, ent.utf8)
		}
	}
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	for name, ent := range glyphInventory {
		if got := g(ent.utf8, ent.ascii); got != ent.ascii {
			t.Errorf("g(%q,%q) with override = %q, want %q", ent.utf8, ent.ascii, got, ent.ascii)
		}
		if got := lookup(name); got != ent.ascii {
			t.Errorf("lookup(%q) with override = %q, want %q", name, got, ent.ascii)
		}
	}
}

func TestGlyphInventory_widthStability(t *testing.T) {
	for name, ent := range glyphInventory {
		got := ansi.StringWidth(ent.utf8)
		if got != ent.width {
			t.Errorf("glyphInventory[%q] utf8=%q ansi.StringWidth=%d, declared width=%d", name, ent.utf8, got, ent.width)
		}
	}
}

func TestToolGlyph_charter(t *testing.T) {
	cases := []struct {
		name  string
		utf8  string
		ascii string
	}{
		{"bash", "❯", "$"},
		{"open_in_browser", "◎", "W"},
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
