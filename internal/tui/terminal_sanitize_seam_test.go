package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestRenderMarkdown_sanitizesUntrustedTerminalControls(t *testing.T) {
	t.Parallel()
	input := "before\x00\a\b\f\v\rafter\tcolumn\nnext" +
		"\x1b[2JCSI\x1b]52;c;clipboard\aOSC-BEL\x1b]8;;https://example.test\x1b\\OSC-ST" +
		"\x1bPpayload\x1b\\DCS\x1b_apc\x1b\\APC\x1b^pm\x1b\\PM\x1bXsos\x1b\\SOS" +
		"\x9b2Jraw-CSI\x9dclipboard\x9craw-OSC\x90secret\x9craw-DCS"

	out, err := RenderMarkdown(input, 100, "dark")
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	plain := ansiStrip(out)
	for _, want := range []string{"beforeaftercolumn", "nextCSIOSC-BELOSC-STDCSAPCPMSOSraw-CSIraw-OSCraw-DCS"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered text missing %q: %q", want, plain)
		}
	}
	for _, sequence := range terminalControls(input) {
		if strings.Contains(out, sequence) {
			t.Errorf("rendered markdown retains terminal control %q in %q", sequence, out)
		}
	}
}

func TestRenderPromptMarkdown_sanitizesStandaloneC1Controls(t *testing.T) {
	t.Parallel()
	input := "before" + string([]byte{0x7f, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d, 0x8e, 0x8f, 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x99, 0x9a}) + "after"
	out, err := RenderPromptMarkdown(input, 100, "dark")
	if err != nil {
		t.Fatalf("RenderPromptMarkdown: %v", err)
	}
	if plain := ansiStrip(out); !strings.Contains(plain, "beforeafter") {
		t.Errorf("rendered prompt = %q, want retained text without controls", plain)
	}
	for _, control := range []byte{0x7f, 0x80, 0x81, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89, 0x8a, 0x8b, 0x8c, 0x8d, 0x8e, 0x8f, 0x91, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x99, 0x9a} {
		if strings.Contains(out, string(control)) {
			t.Errorf("rendered prompt retains terminal control %#x in %q", control, out)
		}
	}
}

func TestModelView_sanitizesPromptStreamAndToolDisplayText(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn:   func(context.Context, string, string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "prompt retained")
	m, _ = submitBusy(t, m)
	// Set raw provider/input values immediately before the public View seam.
	// Bubble Tea's textarea correctly consumes ESC key input itself.
	m.tx.messages[0].content = "prompt\x1b]52;c;clipboard\a retained"
	m = applyDelta(t, m, "model\x1b[2J text\x1bPsecret\x1b\\ retained")
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{
		Name: "bash\x1b^metadata\x1b\\", Args: `{"command":"echo \u001b]0;title\u0007 retained"}`,
	}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "bash\x1b^metadata\x1b\\", Result: "result\x1b_apc\x1b\\ retained", Lines: 1,
	}})
	// The result is intentionally hidden by default; render it through the
	// public Model view after expanding all tool entries.
	next, _ := m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = asModel(t, next)

	out := view(m)
	plain := ansiStrip(out)
	for _, want := range []string{"prompt retained", "model text retained", "echo  retained", "result retained", "bash"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered view missing retained text %q: %q", want, plain)
		}
	}
	for _, sequence := range []string{"\x1b]52;c;clipboard\a", "\x1b[2J", "\x1bPsecret\x1b\\", "\x1b^metadata\x1b\\", "\x1b]0;title\a", "\x1b_apc\x1b\\"} {
		if strings.Contains(out, sequence) {
			t.Errorf("rendered view retains terminal control %q in %q", sequence, out)
		}
	}
}

func TestSettingsOverlayView_sanitizesConfigText(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.Provider = "custom-openai"
	cfg.Model = "model\x1b]52;c;clipboard\a retained"
	cfg.CustomOpenAI.BaseURL = "https://example.test/\u009b2J retained"
	o, _ := openSettingsOverlay(cfg, nil, defaultTheme, nil, Dependencies{})

	out := o.View()
	plain := ansiStrip(out)
	for _, want := range []string{"custom-openai", "model retained", "https://example.test/ retained"} {
		if !strings.Contains(plain, want) {
			t.Errorf("settings view missing retained text %q: %q", want, plain)
		}
	}
	for _, sequence := range []string{"\x1b]52;c;clipboard\a", "\u009b2J"} {
		if strings.Contains(out, sequence) {
			t.Errorf("settings view retains terminal control %q in %q", sequence, out)
		}
	}
}

func TestRailRendering_sanitizesProviderAndModelText(t *testing.T) {
	t.Parallel()
	r := NewRail("provider\u009d\u009c retained", "model\u009b2J retained", "low", true, "session\x1bP\x1b\\ retained", "/tmp/session")
	out := r.render(nil, defaultTheme, defaultRailWidth)
	plain := ansiStrip(out)
	for _, want := range []string{"provider", "model retained", "session retained"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rail missing retained text %q: %q", want, plain)
		}
	}
	for _, sequence := range []string{"\u009d\u009c", "\u009b2J", "\x1bP\x1b\\"} {
		if strings.Contains(out, sequence) {
			t.Errorf("rail retains terminal control %q in %q", sequence, out)
		}
	}
}

func terminalControls(s string) []string {
	return []string{"\x00", "\b", "\f", "\v", "\r", "\x1b[2J", "\x1b]52;c;clipboard\a", "\x1b]8;;https://example.test\x1b\\", "\x1bPpayload\x1b\\", "\x1b_apc\x1b\\", "\x1b^pm\x1b\\", "\x1bXsos\x1b\\", "\x9b2J", "\x9dclipboard\x9c", "\x90secret\x9c"}
}
