package tui

import (
	"fmt"
	"strings"
)

// deepseek-v4-flash pricing, per 1M tokens (docs/spec.md §4 / ADR-0003). These
// are the canonical rates that turn the engine seam's cached/cold token
// telemetry into the running cost readout on the status strip.
const (
	costPerInputMiss = 0.14   // $/1M uncached input tokens
	costPerOutput    = 0.28   // $/1M output tokens
	costPerInputHit  = 0.0028 // $/1M cache-hit input tokens
)

// TelemetryKind discriminates a live status-strip update (issue #86): a turn
// boundary, per-turn token usage, or the one-shot compaction marker.
type TelemetryKind int

const (
	// TelemetryTurn counts one agent-loop turn that started.
	TelemetryTurn TelemetryKind = iota
	// TelemetryUsage accumulates cached/cold input and output tokens.
	TelemetryUsage
	// TelemetryCompacted marks that the session was compacted (docs/spec.md §7).
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
}

// Telemetry is the live session telemetry rendered in the bottom status strip
// (issue #86). It holds the run's static config (model, effort, thinking,
// max turns) plus live counters updated from the engine seam. It is read-only
// against the agent loop: nothing here ever pauses or blocks a run.
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

	// updates is the buffered feed the app's engine listener writes to; the
	// model drains it on the UI goroutine via apply.
	updates chan TelemetryUpdate
}

// NewTelemetry builds a status-strip collector seeded with the run's static
// session state (issue #86), returning the live Telemetry ready to be handed
// to a Model via Dependencies. The caller wires the engine event seam's
// per-turn updates into UpdateChan.
func NewTelemetry(model string, effort string, thinking bool, maxTurns int) *Telemetry {
	return newTelemetry(model, effort, thinking, maxTurns)
}

// newTelemetry builds a status-strip collector seeded with the run's static
// session state. All updates arrive through the returned channel.
func newTelemetry(model string, effort string, thinking bool, maxTurns int) *Telemetry {
	return &Telemetry{
		model:    model,
		effort:   effort,
		thinking: thinking,
		maxTurns: maxTurns,
		updates:  make(chan TelemetryUpdate, 64),
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
	case TelemetryCompacted:
		t.compacted = true
	}
}

// cost returns the running session cost in dollars from accumulated token
// telemetry at the deepseek-v4-flash rates.
func (t *Telemetry) cost() float64 {
	return float64(t.cacheMiss)/1e6*costPerInputMiss +
		float64(t.cacheHit)/1e6*costPerInputHit +
		float64(t.output)/1e6*costPerOutput
}

// hitPercent returns the prompt-cache hit ratio as a percentage, 0 when no
// input tokens have been billed yet.
func (t *Telemetry) hitPercent() float64 {
	in := t.cacheHit + t.cacheMiss
	if in == 0 {
		return 0
	}
	return float64(t.cacheHit) / float64(in) * 100
}

// formatCost renders the running cost in dollars, dropping trailing zeros.
func formatCost(c float64) string {
	return fmt.Sprintf("$%.4g", c)
}

// collapseWidth is the terminal width below which the status strip drops the
// static session details (model/effort/thinking) and keeps only the live
// telemetry, so it stays glanceable and never crowds the composer.
const collapseWidth = 100

// render returns the status-strip line. It is compact and glanceable at normal
// widths, and collapses to the live telemetry on narrow windows.
func (t *Telemetry) render(width int) string {
	thinking := "on"
	if !t.thinking {
		thinking = "off"
	}
	turns := fmt.Sprintf("%d/%d", t.turns, t.maxTurns)
	gauge := fmt.Sprintf("cache:%.0f%%", t.hitPercent())
	cost := "cost:" + formatCost(t.cost())
	compacted := ""
	if t.compacted {
		compacted = " [compacted]"
	}

	// Static session details only on wide-enough terminals.
	if width >= collapseWidth {
		return strings.Join([]string{
			t.model,
			"effort:" + t.effort,
			"thinking:" + thinking,
			turns,
			gauge,
			cost,
		}, " · ") + compacted
	}
	return strings.Join([]string{turns, gauge, cost}, " · ") + compacted
}
