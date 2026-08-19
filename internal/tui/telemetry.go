package tui

import (
	"time"
)

// TelemetryKind discriminates a live status-strip update: a turn boundary,
// per-turn token usage, or the one-shot compaction marker.
type TelemetryKind int

const (
	// TelemetryTurn counts one agent-loop turn that started.
	TelemetryTurn TelemetryKind = iota
	// TelemetryUsage accumulates cached/cold input and output tokens.
	TelemetryUsage
	// TelemetryCompacted marks that the session was compacted.
	TelemetryCompacted
)

// TelemetryUpdate is one additive update for the status strip. It is fed from
// the app's engine-listener goroutine (via a buffered channel) and applied on
// the UI goroutine, so the strip aggregates deterministically without racing
// the running agent loop. It mirrors the engine seam's UsageEvent/TurnEvent/
// CompactedEvent while keeping the TUI decoupled from the engine.
type TelemetryUpdate struct {
	Kind   TelemetryKind
	Hit    int
	Miss   int
	Output int
	// Ctx is the live per-turn context-window size (provider.Usage.PromptTokens)
	// for the active turn. It REPLACES the tracked live value on each usage
	// event, never accumulates, so it shrinks after a compaction.
	Ctx int
}

// Telemetry is the live session telemetry surface consumed by the right rail's
// STATS section and the settings readout. It holds the run's static config
// (model, effort, thinking, max turns) plus live counters updated from the
// engine seam. It is read-only against the agent loop: nothing here ever
// pauses or blocks a run.
type Telemetry struct {
	model    string
	effort   string
	thinking bool
	maxTurns int

	turns     int
	cacheHit  int
	cacheMiss int
	output    int
	compacted bool
	// liveCtx is the live context-window size for the active turn, replaced from
	// each usage event's provider.Usage.PromptTokens rather than accumulated.
	// 0 until the first usage event lands.
	liveCtx int

	// startedAt is when the session began (NewTelemetry), backing the live
	// session-elapsed readout in the right rail STATS (benchmark §4.1
	// statusline telemetry: elapsed time). It is set once and never mutated.
	startedAt time.Time

	// updates is the buffered feed the app's engine listener writes to; the
	// model drains it on the UI goroutine via apply.
	updates chan TelemetryUpdate
}

// NewTelemetry builds the live session telemetry surface seeded with the run's
// static session state, returning the Telemetry ready to be handed to a Model
// via Dependencies. The caller wires the engine event seam's per-turn updates
// into UpdateChan.
func NewTelemetry(model string, effort string, thinking bool, maxTurns int) *Telemetry {
	return &Telemetry{
		model:     model,
		effort:    effort,
		thinking:  thinking,
		maxTurns:  maxTurns,
		startedAt: time.Now(),
		updates:   make(chan TelemetryUpdate, 64),
	}
}

// UpdateChan exposes the live-update feed the app wires the engine listener to.
func (t *Telemetry) UpdateChan() chan<- TelemetryUpdate { return t.updates }

// Updates exposes the same feed for reading (tests/observation).
func (t *Telemetry) Updates() <-chan TelemetryUpdate { return t.updates }

// apply folds one engine-derived update into the live counters. It runs on the
// UI goroutine only.
func (t *Telemetry) apply(u TelemetryUpdate) {
	switch u.Kind {
	case TelemetryTurn:
		t.turns++
	case TelemetryUsage:
		t.cacheHit += u.Hit
		t.cacheMiss += u.Miss
		t.output += u.Output
		// Live ctx is replaced, not added: it reflects the current turn's
		// context-window size and shrinks after a compaction.
		t.liveCtx = u.Ctx
	case TelemetryCompacted:
		t.compacted = true
	}
}

// liveContextSize returns the live per-turn context-window size in tokens (0
// before the first usage event). Unlike the cumulative counters it is REPLACED,
// not accumulated, so it collapses back down after a compaction shrinks the
// real context.
func (t *Telemetry) liveContextSize() int { return t.liveCtx }

// hitPercent returns the prompt-cache hit ratio as a percentage, 0 when no
// input tokens have been billed yet.
func (t *Telemetry) hitPercent() float64 {
	in := t.cacheHit + t.cacheMiss
	if in == 0 {
		return 0
	}
	return float64(t.cacheHit) / float64(in) * 100
}
