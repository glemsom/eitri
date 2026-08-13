package tui

import (
	"strings"
	"testing"
)

// TestTelemetryAggregatesUsage asserts the live telemetry accumulates per-turn
// token usage into cache hit/miss and running cost with known-good literals
// from the deepseek-v4-flash price table (docs/spec.md §4 / ADR-0003:
// $0.14/1M input miss, $0.28/1M output, $0.0028/1M cache hit).
func TestTelemetryAggregatesUsage(t *testing.T) {
	te := newTelemetry("deepseek-v4-flash", "low", true, 250)
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

// TestTelemetryTurnCounting asserts a status strip counts one turn per turn
// Start event, reflected in the turns/N rendered text.
func TestTelemetryTurnCounting(t *testing.T) {
	te := newTelemetry("deepseek-v4-flash", "low", true, 10)
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})

	if te.turns != 3 {
		t.Fatalf("turns = %d, want 3", te.turns)
	}
	if !strings.Contains(te.render(120), "3/10") {
		t.Errorf("status strip should show turns/max \"3/10\", got: %q", te.render(120))
	}
}

// TestTelemetryCompactionMarker asserts a compaction event surfaces a
// read-only [compacted] marker in the strip.
func TestTelemetryCompactionMarker(t *testing.T) {
	te := newTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryCompacted})

	if !te.compacted {
		t.Fatal("compacted flag not set after compaction event")
	}
	if !strings.Contains(te.render(120), "[compacted]") {
		t.Errorf("status strip missing [compacted] marker, got: %q", te.render(120))
	}
}

// TestTelemetryStripShowsStaticSession asserts the strip renders model, effort,
// and thinking on/off from the run's static config at boot, before any events
// arrive.
func TestTelemetryStripShowsStaticSession(t *testing.T) {
	te := newTelemetry("deepseek-v4-flash", "low", true, 250)
	s := te.render(140)

	for _, want := range []string{"deepseek-v4-flash", "effort", "low", "thinking", "on", "0/250", "$0"} {
		if !strings.Contains(s, want) {
			t.Errorf("status strip missing %q, got: %q", want, s)
		}
	}
}

// TestTelemetryStripCollapsesBelowWidth asserts the status strip collapses to a
// compact one-line form on narrow windows so it never overlaps the composer.
func TestTelemetryStripCollapsesBelowWidth(t *testing.T) {
	te := newTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	full := te.render(140)
	collapsed := te.render(60)

	if len(full) <= len(collapsed) {
		t.Errorf("narrow strip should be more compact: full len=%d coll len=%d", len(full), len(collapsed))
	}
	// Both forms keep the cache gauge and cost so telemetry is never lost.
	if !strings.Contains(collapsed, "%") || !strings.Contains(collapsed, "$") {
		t.Errorf("narrow strip must keep gauge+cost, got: %q", collapsed)
	}
}
