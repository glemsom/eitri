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
	return func(ctx context.Context, prompt string) (tui.TurnResult, error) {
		res, err := runAgent(e, cfg, reg, sessionKey, prompt, canContinue)
		if err != nil {
			return tui.TurnResult{}, err
		}
		// Reasoning rides the same turn seam as the answer so the TUI can render
		// it as a collapsible block (T9b); batch still suppresses it on stdout
		// unless -v (docs/spec.md §6).
		return tui.TurnResult{Answer: res.Answer, Reasoning: res.Reasoning}, nil
	}
}

// runTUI launches the interactive fullscreen TUI on the shared engine and
// blocks until the user quits. It renders into the primary (normal) buffer:
// Bubble Tea's default renderer does not enter the alt screen, so native
// scrollback, selection, and search survive a session (docs/spec.md §9). The
// Settings surface (ctrl+s) is seeded from the loaded config and provider model
// discovery, and persists edits back through the config layer (eitri.md §2.7,
// T12).
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, p provider.Provider, cfgPath string, skills *tools.Catalog) error {
	m := tui.NewModelCfg(tui.Dependencies{
		Models: discoveredModels(context.Background(), p),
		Config: cfg,
		Save:   func(c config.Config) error { return config.Save(c, cfgPath) },
		// The skills panel and `/skillname` slash activation sit on the same
		// catalog the batch engine uses (T8): activation runs the `skill` tool
		// through the registry, so a slash activation behaves identically to a
		// model-invoked one (docs/spec.md §9, eitri.md §2.3, ticket #35).
		Skills: skillSurface(reg, skills),
	})
	m.SetTurn(runEngineTurn(e, cfg, reg, sessionKey, m.ContinueHook()))
	return runProgram(m)
}

// skillSurface adapts the run's skill catalog to the TUI's SkillsSurface seam.
// Items reflects detected + per-session active state; Activate runs the `skill`
// tool through the registry (a no-op registry when the tool is unregistered,
// i.e. no skills).
func skillSurface(reg *tools.Registry, c *tools.Catalog) *tui.SkillsSurface {
	if c == nil || len(c.Names()) == 0 {
		return nil
	}
	items := make([]tui.SkillItem, 0, len(c.Names()))
	for _, it := range c.Items() {
		items = append(items, tui.SkillItem{
			Name:        it.Name,
			Description: it.Description,
			Scope:       it.Scope,
			Active:      it.Active,
		})
	}
	return &tui.SkillsSurface{
		Items: items,
		Activate: func(ctx context.Context, name string) (string, error) {
			return reg.Run(ctx, "skill", map[string]any{"name": name})
		},
	}
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
