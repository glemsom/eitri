package tui

import (
	"testing"
)

func TestTelemetryAggregatesUsage(t *testing.T) {
	t.Parallel()
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
	if pct := te.hitPercent(); pct < 50-1e-9 || pct > 50+1e-9 {
		t.Errorf("hitPercent = %.2f, want 50", pct)
	}
}

func TestTelemetryTurnCounting(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 10)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})

	if te.turns != 3 {
		t.Fatalf("turns = %d, want 3", te.turns)
	}
}

func TestTelemetryCompactionMarker(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryCompacted})

	if !te.compacted {
		t.Fatal("compacted flag not set after compaction event")
	}
}

func TestTelemetryLiveContextReplaces(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 1_000_000, Miss: 1_000_000, Output: 1_000_000, Ctx: 160_000})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 1_000_000, Miss: 1_000_000, Output: 1_000_000, Ctx: 50_000})

	if c := te.liveContextSize(); c != 50_000 {
		t.Fatalf("live context = %d, want 50000 (replaced, not accumulated)", c)
	}
	if te.cacheHit != 2_000_000 || te.cacheMiss != 2_000_000 || te.output != 2_000_000 {
		t.Fatalf("cumulative usage not accumulated: hit=%d miss=%d output=%d, want 2M each", te.cacheHit, te.cacheMiss, te.output)
	}
}
