package tui

import (
	"strings"
	"testing"
)

// TestTelemetryAggregatesUsage asserts the live telemetry accumulates per-turn
// token usage into cache hit/miss and running cost with known-good literals
// from the deepseek-v4-flash price table (ADR-0003:
// $0.14/1M input miss, $0.28/1M output, $0.0028/1M cache hit).
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
	// 2M hit @0.0028 + 2M miss @0.14 + 2M output @0.28 = 0.0056 + 0.28 + 0.56.
	want := 0.0028*2 + 0.14*2 + 0.28*2 // = 0.8456
	if c := te.cost(); c < want-1e-9 || c > want+1e-9 {
		t.Errorf("cost = %.6f, want %.6f", c, want)
	}
	// Cache hit ratio = hit/(hit+miss) = 50%.
	if pct := te.hitPercent(); pct < 50-1e-9 || pct > 50+1e-9 {
		t.Errorf("hitPercent = %.2f, want 50", pct)
	}
}

// TestTelemetryFormatCostNoScientific asserts formatCost renders sub-cent
// costs in plain decimal (a $0.00001 session cost shows as "$0.00001", never
// the %.4g "$1e-05") while keeping mid-size costs and trimming trailing zeros.
func TestTelemetryFormatCostNoScientific(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.00658, "$0.00658"},
		{0.00001, "$0.00001"},
		{0.8456, "$0.8456"},
		{0.00112672, "$0.001127"}, // 4 sig figs: no eight-decimal wall
		{1.2345, "$1.234"},
		{0, "$0"},
	}
	for _, tc := range cases {
		if got := formatCost(tc.in); got != tc.want {
			t.Errorf("formatCost(%g) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if s := formatCost(0.00000028); strings.Contains(s, "e") {
		t.Errorf("formatCost must never use scientific notation, got %q", s)
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
	// The turns/max readout now lives only in the right rail's STATS section
	// (issue #228); the bottom status strip renders no telemetry numbers, so
	// the (now-removed) strip-level render is not asserted here — see
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
	// The [compacted] marker now renders only in the right rail STATS section
	// (issue #228); the bottom status strip no longer shows telemetry.
}
