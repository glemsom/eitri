package tui

import (
	"context"
	"strings"
	"testing"
	"time"

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

func TestGlyphInventory_iconContract(t *testing.T) {
	for name, ent := range glyphInventory {
		if ent.tier != "icon" {
			continue
		}
		// VS16-normalised: must end with U+FE0F.
		if !strings.HasSuffix(ent.utf8, "\ufe0f") {
			t.Errorf("glyphInventory[%q] icon %q is not VS16-normalised", name, ent.utf8)
		}
		// Width must be exactly 2.
		if got := ansi.StringWidth(ent.utf8); got != 2 {
			t.Errorf("glyphInventory[%q] icon %q width = %d, want 2", name, ent.utf8, got)
		}
		// No ZWJ (U+200D) or skin-tone modifiers (U+1F3FB–U+1F3FF).
		for _, r := range ent.utf8 {
			if r == '\u200d' {
				t.Errorf("glyphInventory[%q] icon %q contains ZWJ (U+200D)", name, ent.utf8)
			}
			if r >= '\U0001f3fb' && r <= '\U0001f3ff' {
				t.Errorf("glyphInventory[%q] icon %q contains skin-tone modifier %U", name, ent.utf8, r)
			}
		}
	}
}

func TestToolGlyph_charter(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"bash", "🐚\ufe0f"},
		{"open_in_browser", "🌐\ufe0f"},
		{"skill", "✨\ufe0f"},
		{"unknown", "🧰\ufe0f"},
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
	if !strings.Contains(content, "🐚\ufe0f bash") {
		t.Errorf("UTF-8 tool label missing, got: %q", content)
	}
	if !strings.Contains(content, "✓") {
		t.Errorf("UTF-8 outcome marker missing, got: %q", content)
	}
	if !strings.Contains(content, "│") {
		t.Errorf("UTF-8 border glyph missing, got: %q", content)
	}
}

// TestToolEntry_noIconInResultBody asserts that category icons are never
// injected into the copyable tool-result payload; they live only on the
// chrome head, never inside the card body.
func TestToolEntry_noIconInResultBody(t *testing.T) {
	// Render an expanded tool entry directly so the full result body is visible.
	e := toolEntry{
		name:     "bash",
		args:     `{"command":"echo hello"}`,
		result:   "hello\n🐚\ufe0f emoji in output",
		complete: true,
		lines:    2,
	}
	out := renderToolEntry(defaultTheme, e, true, time.Now(), 80, false, false)
	plain := ansiStrip(out)

	// The user-written emoji inside the result body must survive unchanged.
	if !strings.Contains(plain, "🐚\ufe0f emoji in output") {
		t.Errorf("user emoji inside result body was dropped or altered, got: %q", plain)
	}
	// No injected chrome icon should appear inside the body — the only 🐚 is
	// the one the tool wrote. We verify by checking the card-frame body only.
	bodyStart := strings.Index(plain, "hello")
	if bodyStart < 0 {
		t.Fatalf("result body not found in output: %q", plain)
	}
	body := plain[bodyStart:]
	chromeIcons := []string{"🐚\ufe0f", "🌐\ufe0f", "✨\ufe0f", "🧰\ufe0f", "🧠\ufe0f"}
	for _, icon := range chromeIcons {
		// The user emoji is expected; we are checking that the renderer did
		// not ADD any chrome icon beyond what the tool result already carries.
		// Since the result contains 🐚 once, a count > 1 would mean injection.
		if strings.Count(body, icon) > 1 {
			t.Errorf("injected chrome icon %q found inside tool-result body: %q", icon, body)
		}
	}
}
