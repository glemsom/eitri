package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestModel_slashSettingsOpensSurface(t *testing.T) {
	t.Parallel()
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
	m = keypress(t, m, "enter")

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

func TestModel_slashCompletionListsCommands(t *testing.T) {
	t.Parallel()
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

	m = typeText(t, m, "/")
	content := view(m)
	if !strings.Contains(content, "/settings") || !strings.Contains(content, "/copy") || !strings.Contains(content, "/login") {
		t.Errorf("bare `/` completion should list built-in commands, got: %q", content)
	}
	if !strings.Contains(content, "/review") || !strings.Contains(content, "/plan") {
		t.Errorf("bare `/` completion should list detected skills, got: %q", content)
	}

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

func TestModel_slashTabCyclesSettingsAndSkills(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/")

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
	if got := m.composer.Value(); got != "/help" {
		t.Fatalf("fourth tab completion = %q, want /help", got)
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/review" {
		t.Fatalf("fifth tab completion = %q, want /review", got)
	}
	if !strings.Contains(view(m), "▸ /review") {
		t.Fatalf("completion selection marker missing, got: %q", view(m))
	}
}

func TestModel_slashLoginRunsLoginFlow(t *testing.T) {
	t.Parallel()
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

func TestModel_slashCompletionDismissedOnEmptyLine(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}},
	})
	m = resize(t, m)

	m = typeText(t, m, "/")
	if !strings.Contains(view(m), "/settings") {
		t.Fatalf("bare `/` should show the completion list, got: %q", view(m))
	}

	m = keypress(t, m, "backspace")
	if got := m.composer.Value(); got != "" {
		t.Fatalf("composer after backspace = %q, want empty", got)
	}
	content := view(m)
	if strings.Contains(content, "/settings") || strings.Contains(content, "/review") {
		t.Errorf("completion list should be dismissed on empty line, got: %q", content)
	}
}
