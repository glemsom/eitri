package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/glemsom/eitri/internal/config"
)

// TestModel_OpenSettingsRendersSurface verifies ctrl+s opens the Settings
// surface, which renders the provider/model and knob rows (T12).
func TestModel_OpenSettingsRendersSurface(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Models: []string{"deepseek-v4-flash", "grok-2"},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")

	view := m.View()
	if !strings.Contains(view, "Eitri Settings") {
		t.Fatalf("settings view %q missing title", view)
	}
	if !strings.Contains(view, "deepseek-v4-flash") && !strings.Contains(view, "grok-2") {
		t.Fatalf("settings view %q missing the model row", view)
	}
}

// TestModel_SettingsSavePersistsAndCloses verifies navigating to Save and
// pressing Enter invokes the Save seam with the edited config and closes the
// Settings surface (eitri.md §2.7).
func TestModel_SettingsSavePersistsAndCloses(t *testing.T) {
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	// Advance focus from Provider(0) to the Save field (last).
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

// TestModel_SettingsAdjustedValuePersists verifies a value changed in the
// panel (cycling the model) reaches the persisted config.
func TestModel_SettingsAdjustedValuePersists(t *testing.T) {
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
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
	m = keypress(t, m, "enter")

	if saved.Model != "grok-2" {
		t.Fatalf("saved Model = %q, want grok-2 after down in Settings", saved.Model)
	}
}

// TestModel_SettingsEffortSelectingMediumPersists verifies a reasoning-effort
// tier selected in the panel (medium) persists to config through the Save seam
// (issue #74 acceptance criteria).
func TestModel_SettingsEffortSelectingMediumPersists(t *testing.T) {
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // reasoning_effort "high"
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	// Advance focus from Provider(0) to the Reasoning effort field (3).
	for i := fieldProvider; i < fieldEffort; i++ {
		m = keypress(t, m, "tab")
	}
	// From "high", one up selects "medium".
	m = keypress(t, m, "up")
	for i := fieldEffort; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if saved.ReasoningEffort != "medium" {
		t.Fatalf("saved ReasoningEffort = %q, want medium", saved.ReasoningEffort)
	}
}

// TestModel_SettingsPathsBackspaceEdits verifies the free-form writable-paths
// field supports backspace to delete the trailing char before Save.
func TestModel_SettingsPathsBackspaceEdits(t *testing.T) {
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	// Advance focus to the paths field (index 5).
	for i := fieldProvider; i < fieldPaths; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "x")         // append
	m = keypress(t, m, "backspace") // delete it
	for i := fieldPaths; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if len(saved.ExtraWritablePaths) != 1 || saved.ExtraWritablePaths[0] != "/srv" {
		t.Fatalf("saved paths = %v, want [/srv] after append+backspace", saved.ExtraWritablePaths)
	}
}

// TestModel_SettingsThinkingTogglePersists verifies flipping the reasoning
// mode off in the panel persists ThinkingEnabled=false through the Save seam
// while retaining the effort dial (issue #56).
func TestModel_SettingsThinkingTogglePersists(t *testing.T) {
	var saved config.Config
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(), // thinking on by default
		Save:   func(c config.Config) error { saved = c; return nil },
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	// Advance focus from Provider(0) to the Thinking mode field (2).
	for i := fieldProvider; i < fieldThinking; i++ {
		m = keypress(t, m, "tab")
	}
	// Toggle thinking off with an arrow.
	m = keypress(t, m, "down")
	// Advance to Save and persist.
	for i := fieldThinking; i < fieldSave; i++ {
		m = keypress(t, m, "tab")
	}
	m = keypress(t, m, "enter")

	if saved.ThinkingEnabled {
		t.Fatal("saved ThinkingEnabled = true, want false after toggling off in Settings")
	}
	// The effort dial is retained so re-enabling later restores it.
	if saved.ReasoningEffort != "high" {
		t.Fatalf("saved ReasoningEffort = %q, want retained \"high\"", saved.ReasoningEffort)
	}
	// Other seeded knobs are untouched.
	if saved.Provider != "opencode-go" || saved.MaxTurns != 250 {
		t.Fatalf("saved config = %+v, want untouched provider/maxTurns", saved)
	}
}

// TestModel_SettingsDiscoveryLoadsAsync verifies the settings panel reports a
// loading state then folds in on-demand provider model discovery (issue #89
// AC2): opening settings with no pre-seeded list and a DiscoverModels seam
// starts discovery, which delives the model list back through the model loop.
func TestModel_SettingsDiscoveryLoadsAsync(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Config: cfgFixture(), // no Models seeded
		DiscoverModels: func(ctx context.Context) ([]string, error) {
			return []string{"deepseek-v4-flash", "grok-2"}, nil
			return []string{"deepseek-v4-flash", "grok-2"}, nil
		},
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	// While discovery is in flight the panel shows a loading state and renders
	// it, so the user knows a fetch is underway rather than seeing an empty list.
	if m.settings.discoverState != discoverLoading {
		t.Fatalf("settings discoverState after open = %v, want discoverLoading", m.settings.discoverState)
	}
	if !strings.Contains(m.View(), "discovering models") {
		t.Fatalf("settings view %q missing loading state", m.View())
	}

	// The discovery command's delivery folds the model list into the panel.
	nm, _ := m.Update(discoverDoneMsg{models: []string{"deepseek-v4-flash", "grok-2"}})
	m = asModel(t, nm)
	if m.settings.discoverState != discoverIdle {
		t.Fatalf("settings discoverState after delivery = %v, want discoverIdle", m.settings.discoverState)
	}
	if got := m.settings.Model(); got != "deepseek-v4-flash" {
		t.Fatalf("settings Model after discovery = %q, want deepseek-v4-flash", got)
	}
}

// TestModel_SettingsDiscoveryErrorState verifies model discovery that fails
// returns an error state in the panel rather than failing silently (issue #89
// AC2), while the configured model still stays usable.
func TestModel_SettingsDiscoveryErrorState(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Config: cfgFixture(), // no Models seeded
		DiscoverModels: func(ctx context.Context) ([]string, error) {
			return nil, errors.New("connection refused")
		},
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	// Fold in the discovery result (the async command's delivery).
	nm, _ := m.Update(discoverDoneMsg{err: errors.New("connection refused")})
	m = asModel(t, nm)

	if m.settings.discoverState != discoverError {
		t.Fatalf("settings discoverState after failing discovery = %v, want discoverError", m.settings.discoverState)
	}
	if m.settings.discoverErr == "" {
		t.Fatal("settings discovery error message not recorded")
	}
	view := m.View()
	// The error surfaces to the panel, and the configured model remains selectable.
	if !strings.Contains(view, "connection refused") {
		t.Fatalf("settings view %q missing discovery error", view)
	}
	if !strings.Contains(view, cfgFixture().Model) {
		t.Fatalf("settings view %q missing configured model after failed discovery", view)
	}
}

// TestModel_ContinuationPromptAnswersYes verifies the interactive max-turns
// path: an engine that hits the cap signals a prompt, the Model renders it, and
// a "y" answer grants continuation (eitri.md §2.1). The engine-side hook
// blocks on the Model's channels, which the main loop services; this test
// drives the Model side deterministically.
func TestModel_ContinuationPromptAnswersYes(t *testing.T) {
	m := NewModelCfg(Dependencies{})
	m = resize(t, m)

	// The running engine reached the cap and signalled for a decision.
	m.continueReq <- struct{}{}
	m = keypress(t, m, "y")

	// The engine's blocked hook must receive the grant.
	select {
	case got := <-m.continueResp:
		if !got {
			t.Fatal("continuation answered false, want true (y)")
		}
	default:
		t.Fatal("engine hook never received a decision")
	}
}

// TestModel_ContinuationPromptAnswersNo verifies the interactive max-turns
// path refuses continuation on an "n" answer.
func TestModel_ContinuationPromptAnswersNo(t *testing.T) {
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

// keypress sends a named keypress (ctrl+s, tab, down, up, enter, esc, y, n…)
// and returns the updated model.
func keypress(t *testing.T, m Model, key string) Model {
	t.Helper()
	nm, _ := m.Update(namedKey(key))
	return asModel(t, nm)
}

// namedKey maps a textual key name to its tea.KeyMsg. Rune names ('y', 'n',
// letters) become a keyrunes message so the composer/settings accumulate them.
func namedKey(name string) tea.Msg {
	switch name {
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "y":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
	case "n":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}
