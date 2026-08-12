package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

// runEngineTurn adapts the shared runAgent turn to the tui.Turn seam. It runs
// one agent turn over the same engine, session transcript, and tool registry as
// batch, so a TUI greeting round-trips through the engine exactly like batch
// (docs/spec.md §9, eitri.md §2.6). canContinue enables interactive max-turns
// continuation (nil auto-denies, the batch default).
func runEngineTurn(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, canContinue func() bool) tui.Turn {
	return func(ctx context.Context, prompt string) (string, error) {
		res, err := runAgent(e, cfg, reg, sessionKey, prompt, canContinue)
		if err != nil {
			return "", err
		}
		return res.Answer, nil
	}
}

// runTUI launches the interactive fullscreen TUI on the shared engine and
// blocks until the user quits. It renders into the primary (normal) buffer:
// Bubble Tea's default renderer does not enter the alt screen, so native
// scrollback, selection, and search survive a session (docs/spec.md §9). The
// Settings surface (ctrl+s) is seeded from the loaded config and provider model
// discovery, and persists edits back through the config layer (eitri.md §2.7,
// T12).
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, p provider.Provider, cfgPath string) error {
	m := tui.NewModelCfg(tui.Dependencies{
		Models: discoveredModels(context.Background(), p),
		Config: cfg,
		Save:   func(c config.Config) error { return config.Save(c, cfgPath) },
	})
	m.SetTurn(runEngineTurn(e, cfg, reg, sessionKey, m.ContinueHook()))
	return runProgram(m)
}

// discoveredModels surfaces the provider's available model ids via the optional
// ModelLister capability; a provider without it (or an error) yields nil (no
// discovery) rather than failing the TUI boot.
func discoveredModels(ctx context.Context, p provider.Provider) []string {
	if p == nil {
		return nil
	}
	lister, ok := p.(provider.ModelLister)
	if !ok {
		return nil
	}
	models, err := lister.Models(ctx)
	if err != nil {
		return nil
	}
	return models
}

// runProgram launches a Bubble Tea program. It is a package-level seam so tests
// can exercise the boot path without a real terminal; the production default
// runs the interactive TUI.
var runProgram = func(m tui.Model) error {
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
