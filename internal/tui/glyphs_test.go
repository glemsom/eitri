package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestGlyphInventory_charter(t *testing.T) {
	for name, ent := range glyphInventory {
		if ent.utf8 == "" {
			t.Errorf("glyphInventory[%q] has empty UTF-8 form", name)
		}
		if got := lookup(name); got != ent.utf8 {
			t.Errorf("lookup(%q) = %q, want %q", name, got, ent.utf8)
		}
	}
}

func TestGlyphInventory_widthStability(t *testing.T) {
	for name, ent := range glyphInventory {
		if got := ansi.StringWidth(ent.utf8); got < 1 {
			t.Errorf("glyphInventory[%q] utf8=%q has zero or negative width", name, ent.utf8)
		}
	}
}

func TestToolGlyph_charter(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"bash", "❯"},
		{"open_in_browser", "◎"},
		{"unknown", "⊕"},
	}
	for _, c := range cases {
		if got := toolGlyph(c.name); got != c.want {
			t.Errorf("toolGlyph(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestToolEntry_rendersUtf8Glyphs(t *testing.T) {
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
	if !strings.Contains(content, "❯ bash") {
		t.Errorf("UTF-8 tool label missing, got: %q", content)
	}
	if !strings.Contains(content, "✓") {
		t.Errorf("UTF-8 outcome marker missing, got: %q", content)
	}
	if !strings.Contains(content, "│") {
		t.Errorf("UTF-8 border glyph missing, got: %q", content)
	}
}
