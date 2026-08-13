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
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, p provider.Provider, cfgPath string, skills *tools.Catalog, workspace string) error {
	effort := cfg.ReasoningEffort
	if !cfg.ThinkingEnabled {
		effort = ""
	}
	te := tui.NewTelemetry(cfg.Model, effort, cfg.ThinkingEnabled, cfg.MaxTurns)
	// Subscribe the live status strip to the engine's per-turn usage/turn/
	// compaction events (issue #86). Read-only: it only forwards telemetry and
	// never pauses the running agent loop.
	feedTelemetry(e, te)
	m := tui.NewModelCfg(tui.Dependencies{
		Models: discoveredModels(context.Background(), p),
		// The workspace directory is surfaced as read-only project state (issue
		// #82 AC1): the run operates here, shown as a header above the transcript.
		WorkspacePath: workspace,
		Config:        cfg,
		Save:          func(c config.Config) error { return config.Save(c, cfgPath) },
		Telemetry:     te,
		// The skills panel and `/skillname` slash activation sit on the same
		// catalog the batch engine uses (T8): activation runs the `skill` tool
		// through the registry, so a slash activation behaves identically to a
		// model-invoked one (docs/spec.md §9, eitri.md §2.3, ticket #35).
		Skills: skillSurface(reg, skills),
	})
	m.SetTurn(runEngineTurn(e, cfg, reg, sessionKey, m.ContinueHook()))
	return runProgram(m)
}

// feedTelemetry wires the engine's live event stream into a TUI status-strip
// collector (issue #86). It forwards only per-turn usage, turn boundaries, and
// the compaction marker into the strip's buffered channel, delivered
// non-blocking so a busy run never stalls on the strip. The TUI stays decoupled
// from the engine: engine.Event is translated here into UI-facing updates.
func feedTelemetry(e *engine.Engine, te *tui.Telemetry) {
	ch := te.UpdateChan()
	e.SetListener(func(evt engine.Event) {
		switch ev := evt.(type) {
		case engine.UsageEvent:
			pushTelemetry(ch, tui.TelemetryUpdate{Kind: tui.TelemetryUsage,
				Hit: ev.Usage.PromptCacheHitTokens, Miss: ev.Usage.PromptCacheMissTokens, Output: ev.Usage.CompletionTokens})
		case engine.TurnEvent:
			if ev.Start {
				pushTelemetry(ch, tui.TelemetryUpdate{Kind: tui.TelemetryTurn})
			}
		case engine.CompactedEvent:
			pushTelemetry(ch, tui.TelemetryUpdate{Kind: tui.TelemetryCompacted})
		}
	})
}

// pushTelemetry delivers an update to the strip's channel without blocking the
// engine's event-goroutine: if the buffered channel is full the update is
// dropped, because the strip is best-effort telemetry that must never stall a
// live run.
func pushTelemetry(ch chan<- tui.TelemetryUpdate, u tui.TelemetryUpdate) {
	select {
	case ch <- u:
	default:
	}
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
