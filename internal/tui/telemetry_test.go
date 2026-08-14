package tui

import (
	"strings"
	"testing"
)

// TestSparklineBlocks asserts the unicode-block sparkline maps values to the
// eight block levels (issue #182): a linear ramp renders as the full ▁..█
// scale, flat data renders a single level, and zero data renders the lowest
// block (a flat line, never an empty row).
func TestSparklineBlocks(t *testing.T) {
	cases := []struct {
		name  string
		vals  []float64
		width int
		want  string
	}{
		{"ramp", []float64{0, 1, 2, 3, 4, 5, 6, 7}, 8, "▁▂▃▄▅▆▇█"},
		{"all-zero", []float64{0, 0, 0, 0}, 4, "▁▁▁▁"},
		{"flat", []float64{100, 100, 100}, 3, "███"},
		{"pads-left", []float64{5}, 4, "▁▁▁█"},
		{"truncates-left", []float64{0, 1, 2, 3, 4, 5}, 3, "▅▇█"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sparkline(tc.vals, tc.width); got != tc.want {
				t.Errorf("sparkline(%v, %d) = %q, want %q", tc.vals, tc.width, got, tc.want)
			}
		})
	}
}

// TestTelemetryAggregatesUsage asserts the live telemetry accumulates per-turn

// TestTelemetrySparklineHistory asserts the rail's usage sparkline draws from
// per-turn usage history (issue #182): each closed turn is one sample, the
// in-progress turn rides as the live last sample, and the token and cost
// shapes derive from the same history at the strip's rates.
func TestTelemetrySparklineHistory(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	// Turn 1: 100 miss in + 100 out = 200 tokens.
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 100, Output: 100})
	// Turn 2: 300 miss in + 300 out = 600 tokens.
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 300, Output: 300})
	// Turn 3 started; no usage yet — the live edge stays low.
	te.apply(TelemetryUpdate{Kind: TelemetryTurn})

	// Samples: [200, 600, 0] -> max 600 -> ▁▃█ with the low live edge, padded
	// to width 4 on the left.
	if got, want := te.tokenSparkline(4), "▁▃█▁"; got != want {
		t.Errorf("token sparkline = %q, want %q", got, want)
	}
	// Cost per sample scales identically (miss+out at $0.14/$0.28 per 1M), so
	// the cost shape matches the token shape for this history.
	if got, want := te.costSparkline(4), "▁▃█▁"; got != want {
		t.Errorf("cost sparkline = %q, want %q", got, want)
	}
}

// TestTelemetrySparklineCaps asserts the per-turn history is capped so a long
// session's sparkline keeps only the recent shape: the history never grows
// unbounded, and the sparkline renders the newest samples on the right.
func TestTelemetrySparklineCaps(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	for i := 0; i < 70; i++ {
		te.apply(TelemetryUpdate{Kind: TelemetryTurn})
		te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 0, Miss: 10, Output: 10})
	}

	if n := len(te.history); n > maxHistorySamples {
		t.Errorf("history length = %d, want capped at %d", n, maxHistorySamples)
	}
	// All samples are 20 tokens: the capped history renders a flat full-height
	// shape of the requested width.
	if got, want := te.tokenSparkline(6), "██████"; got != want {
		t.Errorf("token sparkline after cap = %q, want %q", got, want)
	}
}

// token usage into cache hit/miss and running cost with known-good literals
// from the deepseek-v4-flash price table (docs/spec.md §4 / ADR-0003:
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

// TestTelemetryTurnCounting asserts a status strip counts one turn per turn
// Start event, reflected in the turns/N rendered text.
func TestTelemetryTurnCounting(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 10)
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
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
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
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
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
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
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
