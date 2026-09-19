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

func TestRoleMark_charter(t *testing.T) {
	if got := userRoleMark(); got != lookup("userRole") {
		t.Errorf("userRoleMark() = %q, want %q", got, lookup("userRole"))
	}
	if got := assistantRoleMark(); got != lookup("assistantRole") {
		t.Errorf("assistantRoleMark() = %q, want %q", got, lookup("assistantRole"))
	}
}

func TestToolIcon_charter(t *testing.T) {
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
		if got := toolIcon(c.name); got != c.want {
			t.Errorf("toolIcon(%q) = %q, want %q", c.name, got, c.want)
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

// TestToolEntry_noIconInResultBody asserts that the category icons are chrome
// only: they render on the entry head and never leak into the copyable
// tool-result body, while emoji the tool itself wrote into its result survive
// verbatim.
func TestToolEntry_noIconInResultBody(t *testing.T) {
	e := toolEntry{
		name:     "bash",
		args:     `{"command":"echo hi"}`,
		result:   "ok\n🎉 user emoji survives",
		complete: true,
		lines:    2,
	}
	out := renderToolEntry(defaultTheme, e, true, time.Now(), 80, false, false)
	plain := ansiStrip(out)

	bodyStart := strings.Index(plain, "ok")
	if bodyStart < 0 {
		t.Fatalf("result body not found in output: %q", plain)
	}
	body := plain[bodyStart:]
	for _, icon := range []string{"🐚\ufe0f", "🌐\ufe0f", "✨\ufe0f", "🧰\ufe0f", "🧠\ufe0f"} {
		if strings.Contains(body, icon) {
			t.Errorf("injected chrome icon %q found inside tool-result body: %q", icon, body)
		}
	}
	if !strings.Contains(body, "🎉 user emoji survives") {
		t.Errorf("received emoji must survive verbatim in the result body, got: %q", body)
	}
}
