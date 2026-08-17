package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// TestModel_slashSettingsOpensSurface verifies the `/settings` slash command
// opens the Settings surface , routing to the same settings
// panel ctrl+s opens — never sent as a chat prompt to the engine seam.
func TestModel_slashSettingsOpensSurface(t *testing.T) {
	var prompted string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"grok-2"},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "/settings")
	// Enter routes the slash command; the settings surface opens synchronously
	// (no engine turn), so a plain Enter keypress suffices.
	m = keypress(t, m, "enter")

	// The settings surface must open (not send /settings to the engine).
	if m.settings == nil {
		t.Fatalf("`/settings` did not open the Settings surface")
	}
	if prompted != "" {
		t.Fatalf("`/settings` must not reach the engine seam, got prompt %q", prompted)
	}
	if !strings.Contains(view(m), "Eitri Settings") {
		t.Fatalf("settings view %q missing title", view(m))
	}
}

// TestModel_slashCompletionListsCommands verifies typing `/` surfaces a
// completion list combining the built-in `/settings`, `/copy`, and `/login`
// commands with any matching skill : the list is visible
// without forcing a tab, and a plain prompt that merely starts with `/` is
// still sent normally so slash handling never swallows user
// input.
func TestModel_slashCompletionListsCommands(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{
			{Name: "review"},
			{Name: "plan"},
		}},
	})
	m = resize(t, m)

	// A bare `/` lists the built-in commands and the detected skills.
	m = typeText(t, m, "/")
	content := view(m)
	if !strings.Contains(content, "/settings") || !strings.Contains(content, "/copy") || !strings.Contains(content, "/login") {
		t.Errorf("bare `/` completion should list built-in commands, got: %q", content)
	}
	if !strings.Contains(content, "/review") || !strings.Contains(content, "/plan") {
		t.Errorf("bare `/` completion should list detected skills, got: %q", content)
	}

	// A non-command slash line (e.g. a real path) still submits as a normal
	// prompt: slash handling must not compete with typing , so
	// only known built-ins (`/settings`, `/copy`, `/login`) and detected
	// `/skillname` commands are intercepted.
	var prompted string
	m = NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/usr/bin/env")
	out, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	asModel(t, out)
	if cmd == nil {
		t.Fatalf("`/usr/bin/env` should submit as a normal turn, got nil cmd")
	}
	cmd()
	if prompted != "/usr/bin/env" {
		t.Errorf("non-command slash line reached engine with %q, want /usr/bin/env", prompted)
	}
}

// TestModel_slashTabCyclesSettingsAndSkills verifies tab on a bare `/` walks the
// full completion list — the built-in `/settings`, `/copy`, and `/login`
// commands ahead of any matching skill — filling
// the composer candidate-by-candidate.
func TestModel_slashTabCyclesSettingsAndSkills(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/")

	// First tab picks `/settings` (first built-in); the second tab advances to
	// `/copy`; the third hits `/login`; the fourth reaches the matching skill.
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/settings" {
		t.Fatalf("first tab completion = %q, want /settings", got)
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/copy" {
		t.Fatalf("second tab completion = %q, want /copy", got)
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/login" {
		t.Fatalf("third tab completion = %q, want /login", got)
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/review" {
		t.Fatalf("fourth tab completion = %q, want /review", got)
	}
	// The completed line renders with the selected candidate marked up.
	if !strings.Contains(view(m), "▸ /review") {
		t.Fatalf("completion selection marker missing, got: %q", view(m))
	}
}

// TestModel_slashLoginRunsLoginFlow verifies `/login` routes to the built-in
// login seam instead of the engine, surfaces the device-flow code, and applies
// the returned config for later turns.
func TestModel_slashLoginRunsLoginFlow(t *testing.T) {
	var prompted string
	var applied config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		Config: config.Config{Provider: "github-copilot"},
		Login: func(_ context.Context, onCode func(LoginCode)) (config.Config, error) {
			onCode(LoginCode{UserCode: "ZZ-AA", VerificationURI: "https://github.com/login/device"})
			return config.Config{Provider: "github-copilot", Copilot: config.CopilotConfig{AccessToken: "fresh"}}, nil
		},
		SaveBack: func(c config.Config) { applied = c },
	})
	m = resize(t, m)
	m = typeText(t, m, "/login")
	m = submitAndWait(t, m)

	if prompted != "" {
		t.Fatalf("`/login` must not reach engine seam, got prompt %q", prompted)
	}
	if m.deps.Config.Copilot.AccessToken != "fresh" {
		t.Fatalf("model config access token = %q, want fresh", m.deps.Config.Copilot.AccessToken)
	}
	if applied.Copilot.AccessToken != "fresh" {
		t.Fatalf("applied config access token = %q, want fresh", applied.Copilot.AccessToken)
	}
	content := plain(view(m))
	if !strings.Contains(content, "https://github.com/login/device") || !strings.Contains(content, "ZZ-AA") {
		t.Fatalf("login flow note missing device code, got: %q", content)
	}
	if !strings.Contains(content, "login") || !strings.Contains(content, "saved") {
		t.Fatalf("login success note missing, got: %q", content)
	}
}

// TestModel_slashCompletionDismissedOnEmptyLine verifies emptying the composer
// line dismisses the slash-completion list on the next render : the
// list appears when a bare `/` is typed, then disappears once the line is
// deleted back to empty. The completion list and its reserved rows must key off
// the current composer value, not a stale `/...` prefix.
func TestModel_slashCompletionDismissedOnEmptyLine(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}},
	})
	m = resize(t, m)

	// Typing a bare `/` surfaces the completion list.
	m = typeText(t, m, "/")
	if !strings.Contains(view(m), "/settings") {
		t.Fatalf("bare `/` should show the completion list, got: %q", view(m))
	}

	// Deleting back to an empty composer line must dismiss the list on the
	// next render: no stale candidate rows survive the emptied input.
	m = keypress(t, m, "backspace")
	if got := m.composer.Value(); got != "" {
		t.Fatalf("composer after backspace = %q, want empty", got)
	}
	content := view(m)
	if strings.Contains(content, "/settings") || strings.Contains(content, "/review") {
		t.Errorf("completion list should be dismissed on empty line, got: %q", content)
	}
}
