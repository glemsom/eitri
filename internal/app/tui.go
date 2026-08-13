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
// blocks until the user quits. It renders through the alternate screen (T1
// pivot, issue #119): a full-terminal viewport where every frame is a clean
// repaint into the alt buffer, so the transcript re-flows cleanly on resize
// with no stale primary-buffer residue. The Settings surface (ctrl+s) is seeded
// from the loaded config and provider model discovery, and persists edits back
// through the config layer (eitri.md §2.7, T12). The right context rail
// (ctrl+b, issue #88) is seeded with the run's static provider/model/effort and
// session context (id + temp path). sessionTemp is the host-form ephemeral /tmp
// root for this run's session (ADR-0002).
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, p provider.Provider, cfgPath string, skills *tools.Catalog, workspace string, sessionTemp string) error {
	effort := cfg.ReasoningEffort
	if !cfg.ThinkingEnabled {
		effort = ""
	}
	te := tui.NewTelemetry(cfg.Model, effort, cfg.ThinkingEnabled, cfg.MaxTurns)
	stream := tui.NewStreamer()
	// The right context rail (issue #88): a fixed-width state pane beside the
	// transcript, seeded with the run's static provider/model/effort/session
	// context and fed live STATS from the telemetry surface below.
	rail := tui.NewRail(cfg.Provider, cfg.Model, effort, cfg.ThinkingEnabled, sessionKey, sessionTemp)
	// The live tool-call feed (issue #84): engine tool events render as compact,
	// collapsed `⊕ tool  args` one-liners in the transcript that expand on
	// demand to the full result.
	tools := tui.NewToolFeed()
	// Subscribe the live status strip, the streaming answer pane, and the tool
	// feed to the engine's per-turn usage/turn/compaction events, AnswerStream
	// deltas, and tool call/result events (issues #86, #83, #84). Read-only: it
	// only forwards telemetry, answer text, and tool events and never pauses the
	// running agent loop.
	feedEngineEvents(e, te, stream, tools)
	m := tui.NewModelCfg(tui.Dependencies{
		// On-demand provider model discovery for the Settings panel (issue #89
		// AC2): the provider's GET /v1/models list is fetched when the panel
		// opens, with loading/error states surfaced instead of failing silently.
		DiscoverModels: discoveredModels(p),
		// The workspace directory is surfaced as read-only project state (issue
		// #82 AC1): the run operates here, shown as a header above the transcript.
		WorkspacePath: workspace,
		Config:        cfg,
		Save:          func(c config.Config) error { return config.Save(c, cfgPath) },
		Telemetry:     te,
		Stream:        stream,
		Tools:         tools,
		Rail:          rail,
		// The skills panel and `/skillname` slash activation sit on the same
		// catalog the batch engine uses (T8): activation runs the `skill` tool
		// through the registry, so a slash activation behaves identically to a
		// model-invoked one (docs/spec.md §9, eitri.md §2.3, ticket #35).
		Skills: skillSurface(reg, skills),
		// The review panel's open_in_browser escape hatch (issue #90) reuses the
		// registry's host-side browser launch seam that backs the open_in_browser
		// tool, so a changed file's path opens in the host browser/editor.
		OpenInBrowser: reg.Browser().Open,
	})
	m.SetTurn(runEngineTurn(e, cfg, reg, sessionKey, m.ContinueHook()))
	return runProgram(m)
}

// feedEngineEvents wires the engine's live event stream into the TUI's status
// strip, streaming answer pane, and tool feed (issues #86, #83, and #84). It
// forwards per-turn usage, turn boundaries, and the compaction marker into the
// strip's buffered channel, each AnswerStream delta into the streaming pane's
// channel, and each tool call/result into the tool feed's channel — all
// delivered non-blocking so a busy run never stalls. The TUI stays decoupled
// from the engine: engine.Event is translated here into UI-facing updates.
func feedEngineEvents(e *engine.Engine, te *tui.Telemetry, stream *tui.Streamer, toolFeed *tui.ToolFeed) {
	teCh := te.UpdateChan()
	sCh := stream.UpdateChan()
	tCh := toolFeed.UpdateChan()
	e.SetListener(func(evt engine.Event) {
		switch ev := evt.(type) {
		case engine.StreamEvent:
			switch ev.Kind {
			case engine.AnswerStream:
				pushStream(sCh, tui.StreamUpdate{Kind: tui.AnswerStream, Delta: ev.Delta})
			case engine.ReasoningStream:
				// Reasoning rides the same stream seam as the answer but tagged as a
				// reasoning delta, so the TUI grows a distinct thinking block and
				// never merges it into the answer (issue #85, docs/spec.md §6).
				pushStream(sCh, tui.StreamUpdate{Kind: tui.ReasoningStream, Delta: ev.Delta})
			}
		case engine.UsageEvent:
			pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryUsage,
				Hit: ev.Usage.PromptCacheHitTokens, Miss: ev.Usage.PromptCacheMissTokens, Output: ev.Usage.CompletionTokens})
		case engine.TurnEvent:
			if ev.Start {
				pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryTurn})
			}
		case engine.CompactedEvent:
			pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryCompacted})
		case engine.ToolCallEvent:
			pushTool(tCh, tui.ToolUpdate{Start: &tui.ToolStart{Name: ev.Name, Args: ev.Arguments}})
		case engine.ToolResultEvent:
			pushTool(tCh, tui.ToolUpdate{Result: &tui.ToolResult{
				Name: ev.Name, Result: ev.Result, Lines: ev.Lines, Dropped: ev.Dropped,
				Compressed: ev.Compressed, Added: ev.Added, Removed: ev.Removed,
				Before: ev.Before, After: ev.After, Path: ev.Path,
			}})
		}
	})
}

// pushStream delivers an answer-text delta to the streaming pane's channel
// without blocking the engine's event-goroutine: if the buffered channel is
// full the delta is dropped, because rendering is best-effort and must never
// stall a live run.
func pushStream(ch chan<- tui.StreamUpdate, u tui.StreamUpdate) {
	select {
	case ch <- u:
	default:
	}
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

// pushTool delivers a tool-call observation to the tool feed's channel without
// blocking the engine's event-goroutine: if the buffered channel is full the
// observation is dropped, because the tool render is best-effort that must
// never stall a live run.
func pushTool(ch chan<- tui.ToolUpdate, u tui.ToolUpdate) {
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

// discoveredModels surfaces the provider's available model ids via the
// optional ModelLister capability, as an on-demand seam for the Settings panel
// (issue #89 AC2). A provider without the capability (or nil) returns a clean
// ErrNoDiscovery rather than failing the TUI boot; the panel renders it as the
// discovery error state.
func discoveredModels(p provider.Provider) func(ctx context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		if p == nil {
			return nil, provider.ErrNoDiscovery
		}
		lister, ok := p.(provider.ModelLister)
		if !ok {
			return nil, provider.ErrNoDiscovery
		}
		return lister.Models(ctx)
	}
}

// runProgram launches a Bubble Tea program. It is a package-level seam so tests
// can exercise the boot path without a real terminal; the production default
// runs the interactive TUI.
var runProgram = func(m tui.Model) error {
	// The TUI takes over the full terminal via the alternate screen (T1 pivot,
	// issue #119): every frame is a clean repaint into the alt buffer, so a
	// window resize re-flows the transcript cleanly with no primary-buffer
	// duplicate/scatter residue.
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
