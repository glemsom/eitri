package tui

import (
	"testing"
)

// TestTelemetryAggregatesUsage asserts the live telemetry accumulates per-turn
// token usage into cache hit/miss counters with known-good literals.
func TestTelemetryAggregatesUsage(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	for _, u := range []TelemetryUpdate{
		{Kind: TelemetryUsage, Hit: 1_000_000, Miss: 1_000_000, Output: 1_000_000},
		{Kind: TelemetryUsage, Hit: 1_000_000, Miss: 1_000_000, Output: 1_000_000},
	} {
		te.apply(u)
	}

	if te.cacheHit != 2_000_000 || te.cacheMiss != 2_000_000 || te.output != 2_000_000 {
		t.Fatalf("usage not accumulated: hit=%d miss=%d output=%d, want 2M each", te.cacheHit, te.cacheMiss, te.output)
	}
	// Cache hit ratio = hit/(hit+miss) = 50%.
	if pct := te.hitPercent(); pct < 50-1e-9 || pct > 50+1e-9 {
		t.Errorf("hitPercent = %.2f, want 50", pct)
	}
}

// TestTelemetryTurnCounting asserts the live telemetry counts one turn per turn
// Start event.
func TestTelemetryTurnCounting(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 10)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})

	if te.turns != 3 {
		t.Fatalf("turns = %d, want 3", te.turns)
	}
	// The turns/max readout now lives only in the right rail's STATS section;
	// the bottom status strip renders no telemetry numbers, so the
	// (now-removed) strip-level render is not asserted here — see
	// rail_test for the rail turn readout.
}

// TestTelemetryCompactionMarker asserts a compaction event sets the read-only
// compaction flag.
func TestTelemetryCompactionMarker(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryCompacted})

	if !te.compacted {
		t.Fatal("compacted flag not set after compaction event")
	}
	// The [compacted] marker now renders only in the right rail STATS section;
	// the bottom status strip no longer shows telemetry.
}

// TestTelemetryLiveContextReplaces asserts the live context-window size is
// REPLACED (not accumulated) from each usage event: it reflects the latest
// per-turn provider.Usage.PromptTokens, so it shrinks after a compaction.
// The cumulative cache hit/miss/output counters stay +=.
func TestTelemetryLiveContextReplaces(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 1_000_000, Miss: 1_000_000, Output: 1_000_000, Ctx: 160_000})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 1_000_000, Miss: 1_000_000, Output: 1_000_000, Ctx: 50_000})

	// Live ctx is replaced: the second turn's smaller value wins, never the sum.
	if c := te.liveContextSize(); c != 50_000 {
		t.Fatalf("live context = %d, want 50000 (replaced, not accumulated)", c)
	}
	// The cumulative hit/miss/output counters still accumulate across turns.
	if te.cacheHit != 2_000_000 || te.cacheMiss != 2_000_000 || te.output != 2_000_000 {
		t.Fatalf("cumulative usage not accumulated: hit=%d miss=%d output=%d, want 2M each", te.cacheHit, te.cacheMiss, te.output)
	}
}
