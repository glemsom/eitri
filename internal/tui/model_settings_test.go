package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestModel_OpenSettingsRendersSurface(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash", "grok-2"},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")

	content := view(m)
	if !strings.Contains(content, "Eitri Settings") {
		t.Fatalf("settings content %q missing title", content)
	}
	if !strings.Contains(content, "deepseek-v4-flash") && !strings.Contains(content, "grok-2") {
		t.Fatalf("settings content %q missing the model row", content)
	}
}

func TestModel_SettingsSavePersistsAndCloses(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if saved.Provider != "opencode-go" || saved.MaxTurns != 250 || saved.Model != "deepseek-v4-flash" {
		t.Fatalf("saved config = %+v, want the seeded draft values", saved)
	}
	if m.settings != nil {
		t.Fatal("settings surface did not close after Save")
	}
}

func TestModel_SettingsAdjustedValuePersists(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash", "grok-2"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	m = keypress(t, m, "tab")  // focus Model
	m = keypress(t, m, "down") // select grok-2
	for i := fieldModel; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	keypress(t, m, "enter")

	if saved.Model != "grok-2" {
		t.Fatalf("saved Model = %q, want grok-2 after down in Settings", saved.Model)
	}
}

func TestModel_SettingsEffortSelectingMediumPersists(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // reasoning_effort "high"
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldEffort; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "up")
	for i := fieldEffort; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	keypress(t, m, "enter")

	if saved.ReasoningEffort != "medium" {
		t.Fatalf("saved ReasoningEffort = %q, want medium", saved.ReasoningEffort)
	}
}

func TestModel_SettingsPathsBackspaceEdits(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldPaths; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "x")         // append
	m = keypress(t, m, "backspace") // delete it
	for i := fieldPaths; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	keypress(t, m, "enter")

	if len(saved.ExtraWritablePaths) != 1 || saved.ExtraWritablePaths[0] != "/srv" {
		t.Fatalf("saved paths = %v, want [/srv] after append+backspace", saved.ExtraWritablePaths)
	}
}

func TestModel_SettingsPathsSpaceTypesASpace(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldPaths; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, " ")
	m = keypress(t, m, "v2")
	for i := fieldPaths; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	keypress(t, m, "enter")

	if len(saved.ExtraWritablePaths) != 1 || saved.ExtraWritablePaths[0] != "/srv v2" {
		t.Fatalf("saved paths = %v, want [/srv v2] (space typed literally)", saved.ExtraWritablePaths)
	}
}

func TestModel_SettingsSaveAppliesThinkingStateToLiveSession(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // thinking on, high effort
		Save:   func(c config.Config) error { return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldThinking; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "down") // thinking off
	m = keypress(t, m, "tab")  // effort
	m = keypress(t, m, "up")   // high -> medium
	for i := fieldEffort; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if m.session.ThinkingEnabled() {
		t.Fatal("turn session ThinkingEnabled = true after Settings save, want false")
	}
	if m.tx.reasoningEffort != "medium" {
		t.Fatalf("transcript reasoningEffort = %q, want medium", m.tx.reasoningEffort)
	}
}

func TestModel_SettingsSaveFailureDoesNotApplyLiveConfig(t *testing.T) {
	t.Parallel()
	applied := false
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { return errors.New("disk full") },
		SaveBack: func(c config.Config) {
			applied = true
		},
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	m = keypress(t, m, "down") // provider opencode-go -> github-copilot
	for i := fieldProvider; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if applied {
		t.Fatal("SaveBack ran after Settings save failure")
	}
	if m.deps.Config.Provider != "opencode-go" {
		t.Fatalf("live config provider = %q after failed save, want opencode-go", m.deps.Config.Provider)
	}
	if m.savedMsg != "save failed: disk full" {
		t.Fatalf("savedMsg = %q, want save failure", m.savedMsg)
	}
}

func TestModel_SettingsThinkingTogglePersists(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // thinking on by default
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldThinking; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "down")
	for i := fieldThinking; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	keypress(t, m, "enter")

	if saved.ThinkingEnabled {
		t.Fatal("saved ThinkingEnabled = true, want false after toggling off in Settings")
	}
	if saved.ReasoningEffort != "high" {
		t.Fatalf("saved ReasoningEffort = %q, want retained \"high\"", saved.ReasoningEffort)
	}
	if saved.Provider != "opencode-go" || saved.MaxTurns != 250 {
		t.Fatalf("saved config = %+v, want untouched provider/maxTurns", saved)
	}
}

func TestModel_SettingsCollapseTogglesPersistAndFlipDefaults(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // both collapsed-by-default on
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldCoTCollapsed; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "down") // CoT collapsed -> off
	m = keypress(t, m, "tab")  // focus Tool results collapsed
	m = keypress(t, m, "down") // tool results collapsed -> off
	for i := fieldToolResultsCollapsed; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if saved.CoTCollapsedByDefault {
		t.Fatal("saved CoTCollapsedByDefault = true, want false after toggling off")
	}
	if saved.ToolResultsCollapsedByDefault {
		t.Fatal("saved ToolResultsCollapsedByDefault = true, want false after toggling off")
	}
	if m.tx.cotExpanded == false {
		t.Fatal("transcript cotExpanded = false after save, want true (setting flipped the default)")
	}
	if m.tx.toolResultsExpanded == false {
		t.Fatal("transcript toolResultsExpanded = false after save, want true")
	}
}

func TestModel_SettingsThemeSelectingPersists(t *testing.T) {
	t.Parallel()
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // theme "dark"
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldTheme; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "down")
	for i := fieldTheme; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	keypress(t, m, "enter")

	if saved.Theme != "light" {
		t.Fatalf("saved Theme = %q, want light", saved.Theme)
	}
}

func TestSettingsView_ThinkingSuppressionWarning(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.ThinkingEnabled = false
	unsupported := func() bool { return false }
	supported := func() bool { return true }

	cases := []struct {
		name                string
		cfg                 config.Config
		thinkingSuppression func() bool
		wantWarning         bool
	}{
		{"off+unsupported: warn", cfg, unsupported, true},
		{"off+supported: no warn", cfg, supported, false},
		{"off+nil seam: assume supported", cfg, nil, false},
		{"on+unsupported: no warn", cfgFixture(), unsupported, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSettingsForm(tc.cfg, []string{})
			f.thinkingSuppression = tc.thinkingSuppression
			view := settingsView(f)
			if tc.wantWarning && !strings.Contains(view, "reasoning cannot be disabled on this provider") {
				t.Fatalf("settings view %q missing the thinking-suppression warning", view)
			}
			if !tc.wantWarning && strings.Contains(view, "reasoning cannot be disabled on this provider") {
				t.Fatalf("settings view %q rendered a warning, want none", view)
			}
		})
	}
}

func TestModel_SettingsWiringSurfacesThinkingSuppression(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.ThinkingEnabled = false
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models:              []string{"deepseek-v4-flash"},
		Config:              cfg,
		ThinkingSuppression: func() bool { return false },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")

	if m.settings == nil || m.settings.thinkingSuppression == nil {
		t.Fatal("settings form not seeded with the thinking-suppression seam")
	}
	if m.settings.thinkingSuppression() {
		t.Fatal("seeded thinkingSuppression() = true, want false (unsupported provider)")
	}
	if !strings.Contains(view(m), "reasoning cannot be disabled on this provider") {
		t.Fatalf("settings view %q missing the thinking-suppression warning", view(m))
	}
}

func TestModel_ContinuationPromptAnswersYes(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{})
	m = resize(t, m)

	m.continueReq <- struct{}{}
	m = keypress(t, m, "y")

	select {
	case got := <-m.continueResp:
		if !got {
			t.Fatal("continuation answered false, want true (y)")
		}
	default:
		t.Fatal("engine hook never received a decision")
	}
}

func TestModel_ContinuationPromptAnswersNo(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{})
	m = resize(t, m)

	m.continueReq <- struct{}{}
	m = keypress(t, m, "n")

	select {
	case got := <-m.continueResp:
		if got {
			t.Fatal("continuation answered true, want false (n)")
		}
	default:
		t.Fatal("engine hook never received a decision")
	}
}

func keypress(t *testing.T, m Model, key string) Model {
	t.Helper()
	nm, _ := m.Update(namedKey(key))
	return asModel(t, nm)
}

func namedKey(name string) tea.Msg {
	switch name {
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "y":
		return tea.KeyPressMsg{Code: 'y', Text: "y"}
	case "n":
		return tea.KeyPressMsg{Code: 'n', Text: "n"}
	default:
		return tea.KeyPressMsg{Code: tea.KeyExtended, Text: name}
	}
}
