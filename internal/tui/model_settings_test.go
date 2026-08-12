package tui

import (
	"context"
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
