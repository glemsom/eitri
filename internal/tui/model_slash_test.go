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

func TestModel_newCommandMintsFreshSession(t *testing.T) {
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
	m.history.Push("remember me")
	if len(m.tx.messages) == 0 {
		t.Fatal("setup did not populate the transcript")
	}

	prompted = ""
	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")

	if prompted != "" {
		t.Fatalf("`/new` ran a turn, got prompt %q", prompted)
	}
	if m.prompting {
		t.Fatal("`/new` must not open a confirmation overlay")
	}
	if live.Get() != "fresh" {
		t.Fatalf("`/new` rekeyed session to %q, want fresh", live.Get())
	}
	if len(m.tx.messages) != 0 {
		t.Fatalf("`/new` did not clear the transcript, got %d messages", len(m.tx.messages))
	}
	if got := m.history.Entries(); len(got) == 0 || got[len(got)-1] != "remember me" {
		t.Fatalf("`/new` cleared the prompt-history ring, got %q", got)
	}
}

func TestModel_newCommandResetsLiveStats(t *testing.T) {
	t.Parallel()
	live := NewLiveSessionKey("old")
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		LiveKey:   live,
		NewGUID:   func() string { return "fresh" },
		Telemetry: te,
	})
	m = resize(t, m)
	if te.turns == 0 || te.cacheHit == 0 || te.output == 0 {
		t.Fatalf("setup did not seed live stats (turns=%d hit=%d out=%d)", te.turns, te.cacheHit, te.output)
	}

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")

	if m.telemetry == nil {
		t.Fatal("model telemetry unset")
	}
	if m.telemetry.turns != 0 || m.telemetry.cacheHit != 0 || m.telemetry.cacheMiss != 0 || m.telemetry.output != 0 {
		t.Fatalf("`/new` did not reset live stats (turns=%d hit=%d miss=%d out=%d)",
			m.telemetry.turns, m.telemetry.cacheHit, m.telemetry.cacheMiss, m.telemetry.output)
	}
	if m.telemetry.compacted {
		t.Fatal("`/new` left the compaction marker set")
	}
}

func TestModel_newCommandBlockedWhileBusy(t *testing.T) {
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
	m.tx.busy = true

	m = typeText(t, m, "/new")
	m = keypress(t, m, "enter")

	if m.prompting {
		t.Fatal("`/new` opened a prompt while a turn streams")
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed while busy, got %q", live.Get())
	}
}

func TestModel_newCommandBlockedWhileSettingsOpen(t *testing.T) {
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
	sm, _ := m.startSettings()
	m = asModel(t, sm)

	m = typeText(t, m, "/new")

	if m.prompting {
		t.Fatal("`/new` opened a prompt while the settings overlay is open")
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

	if m.prompting {
		t.Fatal("`/new` opened a prompt while a skill is pending")
	}
	if live.Get() != "old" {
		t.Fatalf("`/new` rekeyed while a skill is pending, got %q", live.Get())
	}
}

func TestModel_slashCompletionRendersCommandsPopover(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{Skills: &SkillsSurface{Items: []SkillItem{{Name: "review"}}}})
	m = resize(t, m)
	m = typeText(t, m, "/")

	content := ansiStrip(view(m))
	commands := strings.Index(content, "Commands")
	ask := strings.Index(content, "Ask Eitri")
	hints := strings.Index(content, "navigate")
	if commands == -1 {
		t.Fatalf("slash completion missing Commands popover, got:\n%s", content)
	}
	if ask == -1 || commands > ask {
		t.Fatalf("Commands popover must render above Ask Eitri panel, got:\n%s", content)
	}
	if hints == -1 || hints < ask {
		t.Fatalf("completion hints must render below composer panel, got:\n%s", content)
	}
	if !strings.Contains(content, "▸ /settings") || !strings.Contains(content, "  /copy") {
		t.Fatalf("slash candidates missing selected/non-selected rows, got:\n%s", content)
	}
}
