package telemetry

import (
	"time"
)

type TelemetryKind int

const (
	TelemetryTurn TelemetryKind = iota
	TelemetryUsage
	TelemetryCompacted
)

type TelemetryUpdate struct {
	Kind   TelemetryKind
	Hit    int
	Miss   int
	Output int
	Ctx    int
}

// Telemetry is the live session telemetry surface consumed by the right rail's STATS section.
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
	liveCtx   int

	startedAt time.Time

	updates chan TelemetryUpdate
}

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

func (t *Telemetry) UpdateChan() chan<- TelemetryUpdate { return t.updates }

func (t *Telemetry) Updates() <-chan TelemetryUpdate { return t.updates }

func (t *Telemetry) Apply(u TelemetryUpdate) {
	switch u.Kind {
	case TelemetryTurn:
		t.turns++
	case TelemetryUsage:
		t.cacheHit += u.Hit
		t.cacheMiss += u.Miss
		t.output += u.Output
		t.liveCtx = u.Ctx
	case TelemetryCompacted:
		t.compacted = true
	}
}

// Reset zeroes the live session counters and restarts the elapsed clock, giving
// a fresh `/new` session an empty STATS picture. The static model, effort,
// thinking, and maxTurns seeding is preserved across the reset.
func (t *Telemetry) Reset() {
	t.turns = 0
	t.cacheHit = 0
	t.cacheMiss = 0
	t.output = 0
	t.compacted = false
	t.liveCtx = 0
	t.startedAt = time.Now()
}

// LiveContextSize returns the live per-turn context-window size in tokens (0 before the first usage event).
func (t *Telemetry) LiveContextSize() int { return t.liveCtx }

// HitPercent returns the prompt-cache hit ratio as a percentage, 0 when no input tokens have been billed yet.
func (t *Telemetry) HitPercent() float64 {
	in := t.cacheHit + t.cacheMiss
	if in == 0 {
		return 0
	}
	return float64(t.cacheHit) / float64(in) * 100
}

// Stats is an immutable snapshot of the accumulated live session counters,
// consumed by the right-rail renderer and by tests. It replaces direct reads
// of unexported fields from outside the package, keeping the counters owned
// by Telemetry alone.
type Stats struct {
	Turns     int
	CacheHit  int
	CacheMiss int
	Output    int
	Compacted bool
	Elapsed   time.Duration
	MaxTurns  int
}

func (t *Telemetry) Stats() Stats {
	return Stats{
		Turns:     t.turns,
		CacheHit:  t.cacheHit,
		CacheMiss: t.cacheMiss,
		Output:    t.output,
		Compacted: t.compacted,
		Elapsed:   time.Since(t.startedAt),
		MaxTurns:  t.maxTurns,
	}
}
