package tui

import (
	"fmt"
	"math"
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

	// history holds the closed per-turn usage samples the rail's sparklines
	// draw from (issue #182), oldest first, capped at maxHistorySamples;
	// current accumulates the in-progress turn's usage so the live edge of the
	// sparkline grows as usage lands.
	history []usageSample
	current usageSample

	// updates is the buffered feed the app's engine listener writes to; the
	// model drains it on the UI goroutine via apply.
	updates chan TelemetryUpdate
}

// NewTelemetry builds a status-strip collector seeded with the run's static
// session state (issue #86), returning the live Telemetry ready to be handed
// to a Model via Dependencies. The caller wires the engine event seam's
// per-turn updates into UpdateChan.
func NewTelemetry(model string, effort string, thinking bool, maxTurns int) *Telemetry {
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
		t.closeSample()
	case TelemetryUsage:
		t.cacheHit += u.Hit
		t.cacheMiss += u.Miss
		t.output += u.Output
		t.current.hit += u.Hit
		t.current.miss += u.Miss
		t.current.out += u.Output
	case TelemetryCompacted:
		t.compacted = true
	}
}

// usageSample is one turn's token usage — the unit the rail's sparkline
// history is drawn from (issue #182). Hit/miss/out split so a sample's cost
// derives at the same rates as the strip's running total.
type usageSample struct {
	hit, miss, out int
}

// maxHistorySamples caps the per-turn usage history the rail's sparklines draw
// from, so a long session keeps the recent shape and the history never grows
// unbounded.
const maxHistorySamples = 64

// closeSample folds the finished turn's usage into the history, dropping
// zero-usage turns so a run's boot boundary (and quiet turns) don't pad the
// sparkline with flat bars. It runs on the UI goroutine only.
func (t *Telemetry) closeSample() {
	if t.current.hit == 0 && t.current.miss == 0 && t.current.out == 0 {
		return
	}
	t.history = append(t.history, t.current)
	if len(t.history) > maxHistorySamples {
		t.history = t.history[len(t.history)-maxHistorySamples:]
	}
	t.current = usageSample{}
}

// samples returns the per-turn usage samples the rail's sparklines draw from:
// the closed turns plus the in-progress turn's accumulation, so the live edge
// of the sparkline grows as usage lands.
func (t *Telemetry) samples() []usageSample {
	if len(t.history) == 0 && t.current.hit == 0 && t.current.miss == 0 && t.current.out == 0 {
		return nil
	}
	out := make([]usageSample, 0, len(t.history)+1)
	out = append(out, t.history...)
	out = append(out, t.current)
	return out
}

// tokenSparkline renders the per-turn token usage (input + output) as a
// unicode-block sparkline (issue #182), so a session's usage history reads as
// a shape next to the running totals.
func (t *Telemetry) tokenSparkline(width int) string {
	samples := t.samples()
	vals := make([]float64, 0, len(samples))
	for _, s := range samples {
		vals = append(vals, float64(s.hit+s.miss+s.out))
	}
	return sparkline(vals, width)
}

// costSparkline renders the per-turn cost as a unicode-block sparkline (issue
// #182), derived from the same history as the token shape at the strip's
// deepseek-v4-flash rates.
func (t *Telemetry) costSparkline(width int) string {
	samples := t.samples()
	vals := make([]float64, 0, len(samples))
	for _, s := range samples {
		vals = append(vals, sampleCost(s))
	}
	return sparkline(vals, width)
}

// sampleCost returns one usage sample's dollar cost at the deepseek-v4-flash
// rates (ADR-0003): the same rates the strip's running total uses.
func sampleCost(s usageSample) float64 {
	return float64(s.miss)/1e6*costPerInputMiss +
		float64(s.hit)/1e6*costPerInputHit +
		float64(s.out)/1e6*costPerOutput
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
// Fixed-point with 8 decimals, never scientific (%.4g renders $1e-05 for
// sub-cent costs — unreadable at a glance); rounding below 8 decimals is
// fine for a glanceable readout.
func formatCost(c float64) string {
	s := fmt.Sprintf("%.8f", c)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return "$" + s
}

// sparkBlocks are the eight unicode block-element levels (U+2581..U+2588)
// that draw the rail's usage-history sparkline (issue #182). They are plain
// text glyphs — the shape reads even on a monochrome or non-truecolor
// terminal, color is only a layer on top.
const sparkBlocks = "▁▂▃▄▅▆▇█"

// sparkline renders a compact unicode-block sparkline of values (issue #182):
// each value maps to one of the eight block levels, scaled by the sample
// maximum, so usage history reads as a shape instead of a row of numbers. The
// newest samples land on the right — the last width values are kept, and
// shorter histories pad with the lowest block on the left — so a growing
// session reads left-to-right. All-zero data renders a flat low line, never an
// empty row.
func sparkline(vals []float64, width int) string {
	if width <= 0 {
		return ""
	}
	if len(vals) > width {
		vals = vals[len(vals)-width:]
	}
	max := 0.0
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	blocks := []rune(sparkBlocks)
	var b strings.Builder
	// Newest samples sit on the right: a shorter history pads with the lowest
	// block on the left, so the live edge is always the last character.
	for i := len(vals); i < width; i++ {
		b.WriteRune(blocks[0])
	}
	for _, v := range vals {
		lvl := 0
		if max > 0 {
			lvl = int(math.Round(v / max * float64(len(blocks)-1)))
		}
		b.WriteRune(blocks[lvl])
	}
	return b.String()
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
