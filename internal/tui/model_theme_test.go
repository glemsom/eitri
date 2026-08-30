package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestModel_unknownThemeStartupWarning(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.Theme = "bogus"
	m := NewModelCfg(Dependencies{Config: cfg})

	first := view(m)
	if !strings.Contains(first, "unknown theme") {
		t.Errorf("first view missing unknown-theme warning, got: %q", first)
	}
	if !strings.Contains(first, `"bogus"`) {
		t.Errorf("first view missing the offending theme value, got: %q", first)
	}
	if !strings.Contains(first, config.DefaultTheme) {
		t.Errorf("first view missing the fallback theme name, got: %q", first)
	}

	m = resize(t, m)
	if second := view(m); strings.Contains(second, "unknown theme") {
		t.Errorf("warning repeated on second frame, got: %q", second)
	}
}

func TestModel_validThemeNoWarning(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.Theme = "dracula"
	m := NewModelCfg(Dependencies{Config: cfg})
	m = resize(t, m)

	if view := view(m); strings.Contains(view, "unknown theme") {
		t.Errorf("valid theme must not warn, got: %q", view)
	}
}

func TestModel_configThemeSkinsChromeAtStartup(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.Theme = "dracula"
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Config: cfg,
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	if pane := lineContaining(view(m), "plain"); !strings.Contains(pane, "\x1b[38;2;189;147;249m") {
		t.Errorf("dracula config must skin the chrome at startup, got: %q", pane)
	}
}

func TestModel_settingsThemeSaveReskinsChrome(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		Config: cfgFixture(), // theme "dark"
		Save:   func(config.Config) error { return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	if pane := lineContaining(view(m), "plain"); !strings.Contains(pane, "\x1b[38;2;122;162;247m") {
		t.Fatalf("default chrome expected before save, got: %q", pane)
	}

	m = keypress(t, m, "ctrl+s")
	for i := fieldProvider; i < fieldTheme; i++ {
		m = keypress(t, m, "enter")
	}
	m = keypress(t, m, "tab") // dark -> light
	m = keypress(t, m, "tab") // light -> dracula
	for i := fieldTheme; i < fieldSave; i++ {
		m = keypress(t, m, "enter")
	}
	m = keypress(t, m, "enter")

	if m.deps.Config.Theme != "dracula" {
		t.Fatalf("config theme after save = %q, want dracula", m.deps.Config.Theme)
	}
	if got := m.tx.theme.accent; got != lipgloss.Color("#BD93F9") {
		t.Fatalf("model theme accent after save = %v, want dracula accent", got)
	}
	if pane := lineContaining(view(m), "plain"); !strings.Contains(pane, "\x1b[38;2;189;147;249m") {
		t.Errorf("chrome must re-skin to dracula after save, got: %q", pane)
	}
}

func TestModel_statusNoteIsOneShot(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(string) error { return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m) // sets the "copied" note

	if view := view(m); !strings.Contains(view, "copied") {
		t.Fatalf("expected copy note on the frame after Ctrl+O, got: %q", view)
	}
	m = resize(t, m)
	if view := view(m); strings.Contains(view, "copied") {
		t.Errorf("copy note repeated after a later update, got: %q", view)
	}
}
