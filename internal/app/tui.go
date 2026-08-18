package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/engine"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tools"
	"github.com/glemsom/eitri/internal/tui"
)

// runEngineTurn adapts the shared runAgent turn to the tui.Turn seam. It runs
// one agent turn over the same engine, session transcript, and tool registry as
// batch, so a TUI greeting round-trips through the engine exactly like batch
// canContinue enables interactive max-turns
// continuation (nil auto-denies, the batch default).
func runEngineTurn(e *engine.Engine, cfg func() config.Config, reg *tools.Registry, sessionKey string, canContinue func() bool) tui.Turn {
	return func(ctx context.Context, prompt string, payload string) (tui.TurnResult, error) {
		cur := cfg()
		// Thread a plain payload from the Turn seam into the engine as a *string:
		// an empty payload stays nil at the runAgent boundary (no injection).
		var skillInject *string
		if payload != "" {
			skillInject = &payload
		}
		res, err := runAgent(e, cur, reg, sessionKey, prompt, skillInject, canContinue)
		if err != nil {
			// The engine's stop sentinel means the user stopped the turn (esc in
			// the TUI): the run's partial answer/reasoning ride the result, and
			// the TUI keeps them on screen marked stopped instead of rendering
			// the cancellation as an error. Any other error fails the turn.
			if errors.Is(err, engine.ErrStopped) {
				return tui.TurnResult{Answer: res.Answer, Reasoning: res.Reasoning, Stopped: true}, nil
			}
			return tui.TurnResult{}, err
		}
		// Reasoning rides the same turn seam as the answer so the TUI can render
		// it as a collapsible block; batch still suppresses it on stdout unless -v.
		return tui.TurnResult{Answer: res.Answer, Reasoning: res.Reasoning}, nil
	}
}

// runTUI launches the interactive fullscreen TUI on the shared engine and
// blocks until the user quits. It renders through the alternate screen: a
// full-terminal viewport where every frame is a clean repaint into the alt
// buffer, so the transcript re-flows cleanly on resize with no stale
// primary-buffer residue. The Settings surface (ctrl+s) is seeded from the
// loaded config and provider model discovery, and persists edits back through
// the config layer. The right context rail is seeded with the run's static
// provider/model/effort and session context (id + temp path). sessionTemp is
// the host-form ephemeral /tmp root for this run's session.
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, p provider.Provider, cfgPath string, skills *tools.Catalog, workspace string, sessionTemp string) error {
	effort := cfg.ReasoningEffort
	if !cfg.ThinkingEnabled {
		effort = ""
	}
	te := tui.NewTelemetry(cfg.Model, effort, cfg.ThinkingEnabled, cfg.MaxTurns)
	stream := tui.NewStreamer()
	// The right context rail: a fixed-width state pane beside the
	// transcript, seeded with the run's static provider/model/effort/session
	// context and fed live STATS from the telemetry surface below.
	rail := tui.NewRail(cfg.Provider, cfg.Model, effort, cfg.ThinkingEnabled, sessionKey, sessionTemp)
	// The workspace's checked-out branch joins the CONTEXT section (statusline
	// telemetry, benchmark §4.1): a pure .git/HEAD read, no subprocess.
	rail.SetBranch(tui.GitBranch(workspace))
	// The live tool-call feed: engine tool events render as compact,
	// collapsed `⊕ tool  args` one-liners in the transcript that expand on
	// demand to the full result.
	tools := tui.NewToolFeed()
	// File line-delta + card-diff content: a TUI-side observer
	// fed by the engine's tool-call event stream snapshots each edit/write
	// target on tool-call start and diffs it on tool result, so the `⊕ edit
	// path [+N,-M]` tag and the expanded card's inline diff compute entirely
	// on the TUI side of the seam. The injected path-resolution seam wires the
	// registry's shared path translator + workspace root.
	observer := tui.NewDeltaObserver(fileDeltaResolver(reg))
	// Subscribe the live status strip, the streaming answer pane, and the tool
	// feed to the engine's per-turn usage/turn/compaction events, AnswerStream
	// deltas, and tool call/result events. Read-only: it only forwards
	// telemetry, answer text, and tool events and never pauses the running
	// agent loop.
	feedEngineEvents(e, te, stream, tools, observer)
	currentCfg := cfg
	m := tui.NewModelCfg(tui.Dependencies{
		// On-demand provider model discovery for the Settings panel:
		// the provider's GET /v1/models list is fetched when the panel opens,
		// with loading/error states surfaced instead of failing silently.
		DiscoverModels: discoveredModels(cfgPath),
		// The workspace directory is surfaced as read-only project state:
		// the run operates here, shown as a header above the transcript.
		WorkspacePath: workspace,
		Config:        cfg,
		Save:          func(c config.Config) error { return config.Save(c, cfgPath) },
		SaveBack: func(c config.Config) {
			currentCfg = c
			if np, err := buildProvider(c, cfgPath); err == nil {
				if hp, ok := p.(*hotProvider); ok {
					hp.Set(np)
				}
			}
		},
		Telemetry: te,
		Stream:    stream,
		Tools:     tools,
		Rail:      rail,
		// The run's provider thinking-suppression capability:
		// the Settings panel warns when thinking is off but the provider cannot
		// actually silence reasoning on the wire. The seam is derived here from
		// the concrete provider via the generation-control negotiation seam, so
		// the TUI stays decoupled from internal/provider. Nil p → supported
		// (view-only runs never warn).
		ThinkingSuppression: thinkingSuppression(p),
		// `/skillname` slash activation sits on the same catalog the batch
		// engine uses: activation runs the `skill` tool through the registry,
		// so a slash activation behaves identically to a model-invoked one.
		// The rail shows no skills panel; the surface only feeds slash
		// completion and activation.
		Skills: skillSurface(reg, skills),
		Login: func(ctx context.Context, onCode func(tui.LoginCode)) (config.Config, error) {
			if currentCfg.Provider != provider.ProviderCopilot {
				return config.Config{}, fmt.Errorf("login is only available for provider %q", provider.ProviderCopilot)
			}
			return CopilotConnect(ctx, cfgPath, http.DefaultClient, func(cd provider.DeviceCode) {
				if onCode != nil {
					onCode(tui.LoginCode{UserCode: cd.UserCode, VerificationURI: cd.VerificationURI})
				}
			})
		},
	})
	td := tui.NewTurnDispatch(runEngineTurn(e, func() config.Config { return currentCfg }, reg, sessionKey, m.ContinueHook()))
	td.SetThinkingEnabled(cfg.ThinkingEnabled)
	m.SetTurnDispatch(td)
	return runProgram(m)
}

// feedEngineEvents wires the engine's live event stream into the TUI's status
// strip, streaming answer pane, and tool feed. It forwards per-turn usage, turn
// boundaries, and the compaction marker into the strip's buffered channel, each
// AnswerStream delta into the streaming pane's channel, and each tool
// call/result into the tool feed's channel — all delivered non-blocking so a
// busy run never stalls. The delta observer is fed the paired tool
// start/result events so the feed's entries carry the file line-delta and
// before/after content computed entirely on the TUI side of the engine seam.
// The TUI stays decoupled from the engine: engine.Event is translated here
// into UI-facing updates.
func feedEngineEvents(e *engine.Engine, te *tui.Telemetry, stream *tui.Streamer, toolFeed *tui.ToolFeed, obs *tui.DeltaObserver) {
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
				// never merges it into the answer.
				pushStream(sCh, tui.StreamUpdate{Kind: tui.ReasoningStream, Delta: ev.Delta})
			}
		case engine.UsageEvent:
			pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryUsage,
				Hit: ev.Usage.PromptCacheHitTokens, Miss: ev.Usage.PromptCacheMissTokens, Output: ev.Usage.CompletionTokens,
				// Live per-turn context-window size: the provider's PromptTokens,
				// recomputed each request, so it shrinks after a compaction.
				Ctx: ev.Usage.PromptTokens})
		case engine.TurnEvent:
			if ev.Start {
				pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryTurn})
			}
		case engine.CompactedEvent:
			pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryCompacted})
		case engine.ToolCallEvent:
			// Snapshot the target file's pre-edit state before the tool runs; the
			// paired result diffs it.
			obs.Start(ev.ID, ev.Name, ev.Arguments)
			pushTool(tCh, tui.ToolUpdate{Start: &tui.ToolStart{Name: ev.Name, Args: ev.Arguments}})
		case engine.ToolResultEvent:
			// The line delta, before/after content, and host path come from the
			// TUI-side observer's diff, not from the engine seam.
			added, removed, before, after, path := obs.Result(ev.ID, ev.Name)
			pushTool(tCh, tui.ToolUpdate{Result: &tui.ToolResult{
				Name: ev.Name, Result: ev.Result, BytesDropped: ev.BytesDropped,
				Lines: ev.Lines, Dropped: ev.Dropped,
				Compressed: ev.Compressed, Added: added, Removed: removed,
				Before: before, After: after, Path: path,
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

// skillSurface adapts the run's skill catalog to the TUI's slash-command
// surface: Items lists the detected skill names for `/` completion and
// Activate runs the `skill` tool through the registry (a no-op registry when
// the tool is unregistered, i.e. no skills).
func skillSurface(reg *tools.Registry, c *tools.Catalog) *tui.SkillsSurface {
	if c == nil || len(c.Names()) == 0 {
		return nil
	}
	items := make([]tui.SkillItem, 0, len(c.Names()))
	for _, name := range c.Names() {
		items = append(items, tui.SkillItem{Name: name})
	}
	return &tui.SkillsSurface{
		Items: items,
		Activate: func(ctx context.Context, name string) (string, error) {
			res, err := reg.Run(ctx, "skill", map[string]any{"name": name})
			if err != nil {
				return "", err
			}
			return res.Text, nil
		},
	}
}

// discoveredModels surfaces the draft config's provider model ids via the
// optional ModelLister capability, as an on-demand seam for the Settings panel.
// It builds the draft's provider so switching provider inside Settings
// re-discovers against that provider before Save. A provider without the
// capability returns a clean ErrNoDiscovery rather than failing TUI boot;
// the panel renders it as discovery error state.
func discoveredModels(cfgPath string) func(ctx context.Context, cfg config.Config) ([]string, error) {
	return func(ctx context.Context, cfg config.Config) ([]string, error) {
		p, err := buildProvider(cfg, cfgPath)
		if err != nil {
			return nil, err
		}
		lister, ok := p.(provider.ModelLister)
		if !ok {
			return nil, provider.ErrNoDiscovery
		}
		models, err := lister.Models(ctx)
		if err != nil {
			return nil, err
		}
		return provider.ModelIDs(models), nil
	}
}

// thinkingSuppression reports whether the run's provider can actually suppress
// reasoning on the wire when thinking is off. It consults the provider's
// declared generation controls through NegotiateGenerationControls and returns
// whether thinking_suppression is in the honored set. A nil provider (or one
// without the capability surface) assumes support so a view-only run never
// spurs the settings warning; negotiation failure also degrades to supported
// (the toggle stays unfettered rather than falsely warning).
func thinkingSuppression(p provider.Provider) func() bool {
	return func() bool {
		if p == nil {
			return true
		}
		honored, err := provider.NegotiateGenerationControls(context.Background(), p, []provider.ControlRequirement{
			{Control: provider.GenerationControlThinkingSuppression, Required: false},
		})
		if err != nil {
			return true // negotiation failure: assume supported, never false-warn
		}
		for _, c := range honored {
			if c == provider.GenerationControlThinkingSuppression {
				return true
			}
		}
		return false
	}
}

// runProgram launches a Bubble Tea program. It is a package-level seam so tests
// can exercise the boot path without a real terminal; the production default
// runs the interactive TUI.
var runProgram = func(m tui.Model) error {
	// The TUI takes over the full terminal via the alternate screen and
	// mouse-cell-motion mode — declared declaratively on the model's tea.View
	// (View sets v.AltScreen and v.MouseMode) per bubbletea v2. The transcript
	// scrolls with the wheel over the history viewport and click-drags select.
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
