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

// runEngineTurn adapts the shared runAgent turn to the tui.Turn seam.
func runEngineTurn(e *engine.Engine, cfg func() config.Config, reg *tools.Registry, sessionKey string, canContinue func() bool) tui.Turn {
	return func(ctx context.Context, prompt string, payload string) (tui.TurnResult, error) {
		cur := cfg()
		// Thread a plain payload from the Turn seam into the engine as a *string: an empty payload stays nil at the runAgent boundary (no injection).
		var skillInject *string
		if payload != "" {
			skillInject = &payload
		}
		res, err := runAgent(e, cur, reg, sessionKey, prompt, skillInject, canContinue)
		if err != nil {
			if errors.Is(err, engine.ErrStopped) {
				return tui.TurnResult{Answer: res.Answer, Reasoning: res.Reasoning, Stopped: true}, nil
			}
			return tui.TurnResult{}, err
		}
		return tui.TurnResult{Answer: res.Answer, Reasoning: res.Reasoning}, nil
	}
}

// runTUI launches the interactive fullscreen TUI on the shared engine and blocks until the user quits.
func runTUI(e *engine.Engine, cfg config.Config, reg *tools.Registry, sessionKey string, p provider.Provider, cfgPath string, skills *tools.Catalog, workspace string, sessionTemp string) error {
	effort := cfg.ReasoningEffort
	if !cfg.ThinkingEnabled {
		effort = ""
	}
	te := tui.NewTelemetry(cfg.Model, effort, cfg.ThinkingEnabled, cfg.MaxTurns)
	stream := tui.NewStreamer()
	rail := tui.NewRail(cfg.Provider, cfg.Model, effort, cfg.ThinkingEnabled, sessionKey, sessionTemp)
	rail.SetBranch(tui.GitBranch(workspace))
	tools := tui.NewToolFeed()
	observer := tui.NewDeltaObserver(fileDeltaResolver(reg))
	feedEngineEvents(e, te, stream, tools, observer)
	currentCfg := cfg
	m := tui.NewModelCfg(tui.Dependencies{
		DiscoverModels: discoveredModels(cfgPath),
		WorkspacePath:  workspace,
		Config:         cfg,
		Save:           func(c config.Config) error { return config.Save(c, cfgPath) },
		SaveBack: func(c config.Config) {
			currentCfg = c
			if np, err := buildProvider(c, cfgPath); err == nil {
				if hp, ok := p.(*hotProvider); ok {
					hp.Set(np)
				}
			}
		},
		Telemetry:           te,
		Stream:              stream,
		Tools:               tools,
		Rail:                rail,
		ThinkingSuppression: thinkingSuppression(p),
		Skills:              skillSurface(reg, skills),
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

// feedEngineEvents wires the engine's live event stream into the TUI's status strip, streaming answer pane, and tool feed.
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
				pushStream(sCh, tui.StreamUpdate{Kind: tui.ReasoningStream, Delta: ev.Delta})
			}
		case engine.UsageEvent:
			pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryUsage,
				Hit: ev.Usage.PromptCacheHitTokens, Miss: ev.Usage.PromptCacheMissTokens, Output: ev.Usage.CompletionTokens,
				Ctx: ev.Usage.PromptTokens})
		case engine.TurnEvent:
			if ev.Start {
				pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryTurn})
			}
		case engine.CompactedEvent:
			pushTelemetry(teCh, tui.TelemetryUpdate{Kind: tui.TelemetryCompacted})
		case engine.ToolCallEvent:
			obs.Start(ev.ID, ev.Name, ev.Arguments)
			pushTool(tCh, tui.ToolUpdate{Start: &tui.ToolStart{Name: ev.Name, Args: ev.Arguments}})
		case engine.ToolResultEvent:
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

// pushStream delivers an answer-text delta to the streaming pane's channel without blocking the engine's event-goroutine: if the buffered channel is full the delta is dropped, because rendering is best-effort and must never stall a live run.
func pushStream(ch chan<- tui.StreamUpdate, u tui.StreamUpdate) {
	select {
	case ch <- u:
	default:
	}
}

// pushTelemetry delivers an update to the strip's channel without blocking the engine's event-goroutine: if the buffered channel is full the update is dropped, because the strip is best-effort telemetry that must never stall a live run.
func pushTelemetry(ch chan<- tui.TelemetryUpdate, u tui.TelemetryUpdate) {
	select {
	case ch <- u:
	default:
	}
}

// pushTool delivers a tool-call observation to the tool feed's channel without blocking the engine's event-goroutine: if the buffered channel is full the observation is dropped, because the tool render is best-effort that must never stall a live run.
func pushTool(ch chan<- tui.ToolUpdate, u tui.ToolUpdate) {
	select {
	case ch <- u:
	default:
	}
}

// skillSurface adapts the run's skill catalog to the TUI's slash-command surface: Items lists the detected skill names for `/` completion and Activate runs the `skill` tool through the registry (a no-op registry when the tool is unregistered, i.e. no skills).
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

// discoveredModels surfaces the draft config's provider model ids via the optional ModelLister capability, as an on-demand seam for the Settings panel.
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

// thinkingSuppression reports whether the run's provider can actually suppress reasoning on the wire when thinking is off.
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

// runProgram launches a Bubble Tea program.
var runProgram = func(m tui.Model) error {
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}
