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
	if !strings.Contains(content, "/settings") || !strings.Contains(content, "/copy") || !strings.Contains(content, "/login") || !strings.Contains(content, "/new") {
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

func TestModel_slashNavigateAndAcceptWithTab(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}},
	})
	m = resize(t, m)
	m = typeText(t, m, "/")

	m = keypress(t, m, "down")
	if !strings.Contains(view(m), "▸ /copy") {
		t.Fatalf("arrow navigation did not highlight /copy, got: %q", view(m))
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/copy" {
		t.Fatalf("tab completion = %q, want /copy", got)
	}
	if m.slash.isOpen() {
		t.Fatal("tab completion should close slash dropdown")
	}
}

func TestModel_slashAcceptWithEnterThenSubmit(t *testing.T) {
	t.Parallel()
	var prompted string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items:    []SkillItem{{Name: "review"}},
			Activate: func(context.Context, string) (string, error) { return "payload", nil },
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/r")
	m = keypress(t, m, "enter")
	if got := m.composer.Value(); got != "/review" {
		t.Fatalf("enter completion = %q, want /review", got)
	}
	if prompted != "" {
		t.Fatalf("first enter submitted prompt %q", prompted)
	}
	m = submitAndWait(t, m)
	if prompted != "apply the review skill" {
		t.Fatalf("second enter turn prompt = %q, want skill activation prompt", prompted)
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

func TestModel_slashCompletionDismissedByEscape(t *testing.T) {
	t.Parallel()
	m := resize(t, NewModelCfg(Dependencies{Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}}}))
	m = typeText(t, m, "/")
	m = keypress(t, m, "esc")
	if m.slash.isOpen() {
		t.Fatal("esc should close slash dropdown")
	}
	if got := m.composer.Value(); got != "/" {
		t.Fatalf("esc changed draft to %q", got)
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
	if strings.Contains(content, "/settings") || strings.Contains(content, "/review") {
		t.Errorf("completion list should be dismissed on empty line, got: %q", content)
	}
}

func TestModel_newCommandAsksConfirmation(t *testing.T) {
	t.Parallel()
	var prompted string
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")

	if prompted != "" {
		t.Fatalf("`/new` reached the engine seam before confirmation, got prompt %q", prompted)
	}
	if !m.prompting || !m.confirmNew {
		t.Fatalf("`/new` did not open the confirmation overlay (prompting=%v confirmNew=%v)", m.prompting, m.confirmNew)
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` mutated the session key before confirmation, got %q", live.Get())
	}
}

func TestModel_newCommandCancelLeavesIntact(t *testing.T) {
	t.Parallel()
	var prompted string
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)
	m = typeText(t, m, "/hello")
	m = submitAndWait(t, m)
	if prompted != "/hello" {
		t.Fatalf("setup turn prompt = %q, want /hello", prompted)
	}

	prompted = ""
	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")
	m = keypress(t, m, "n")

	if prompted != "" {
		t.Fatalf("`/new` ran a turn after cancel, got prompt %q", prompted)
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed after cancel, session key = %q, want old", live.Get())
	}
	if m.prompting || m.confirmNew {
		t.Fatal("confirmation overlay still open after cancel")
	}
	if len(m.tx.messages) == 0 {
		t.Fatal("cancel of `/new` cleared the transcript")
	}
}

func TestModel_newCommandConfirmClearsContextAndRekeys(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)
	m = typeText(t, m, "/hello")
	m = submitAndWait(t, m)
	m.history.Push("remember me")
	if len(m.tx.messages) == 0 {
		t.Fatal("setup did not populate the transcript")
	}

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")
	m = keypress(t, m, "y")

	if live.Get() != "fresh" {
		t.Fatalf("`/new` confirm rekeyed session to %q, want fresh", live.Get())
	}
	if len(m.tx.messages) != 0 {
		t.Fatalf("`/new` confirm did not clear the transcript, got %d messages", len(m.tx.messages))
	}
	if m.prompting || m.confirmNew {
		t.Fatal("confirmation overlay still open after confirm")
	}
	if got := m.history.Entries(); len(got) == 0 || got[len(got)-1] != "remember me" {
		t.Fatalf("`/new` cleared the prompt-history ring, got %q", got)
	}
}

func TestModel_newCommandBlockedWhileBusy(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)
	m.tx.busy = true

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")

	if m.prompting || m.confirmNew {
		t.Fatal("`/new` opened confirmation while a turn streams")
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed while busy, got %q", live.Get())
	}
}

func TestModel_newCommandBlockedWhileSettingsOpen(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)
	sm, _ := m.startSettings()
	m = asModel(t, sm)

	m = typeText(t, m, "/new")

	if m.prompting || m.confirmNew {
		t.Fatal("`/new` opened confirmation while the settings overlay is open")
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed while settings open, got %q", live.Get())
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed while settings open, got %q", live.Get())
	}
}

func TestModel_newCommandBlockedWhileSkillPending(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey: live,
		NewGUID: func() string { return "fresh" },
	})
	m = resize(t, m)
	m.skillPending = true

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")

	if m.prompting || m.confirmNew {
		t.Fatal("`/new` opened confirmation while a skill is pending")
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed while a skill is pending, got %q", live.Get())
	}
}
