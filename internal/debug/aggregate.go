// Trace aggregation — the single owner of folding per-call HTTP traces into
// window aggregates (issue #1240). Before this file existed the same fold
// lived twice: the persisted trace archive re-folded the same trace data the
// in-memory recorder accumulates (its aggregateTraces explicitly "mirrored the
// recorder's metrics"). The window aggregate now lives here once, and every
// surface — the persisted archive query (persist.Persister.AggregateTraces)
// and, through it, GET /api/debug/traces/aggregate — routes through it. The
// richer per-provider-per-model metrics snapshot (Retries, error classes,
// latency histogram, cache hit/miss) remains the recorder's own surface; it
// carries data the window aggregate does not.

package debug

import (
	"math"
	"sort"
	"time"
)

// TraceAggregate is a window aggregate over HTTP traces matching a filter.
// ErrorRate is the fraction of calls that failed (non-2xx status or transport
// error), in [0,1]. P50LatencyMs and P95LatencyMs are computed over all
// matching call durations. Tokens sum provider-reported usage. From and To
// bound the actual matching window (oldest/newest trace timestamps).
type TraceAggregate struct {
	Count        int         `json:"count"`
	ErrorCount   int         `json:"error_count"`
	ErrorRate    float64     `json:"error_rate"`
	P50LatencyMs int64       `json:"p50_latency_ms"`
	P95LatencyMs int64       `json:"p95_latency_ms"`
	Tokens       UsageTotals `json:"tokens"`
	From         time.Time   `json:"from,omitempty"`
	To           time.Time   `json:"to,omitempty"`
}

// AggregateTraces folds a set of traces (already matching a filter, sorted
// most-recent-first) into a window aggregate. It is the single aggregation
// implementation behind the persisted archive aggregate endpoint (issue
// #1240); the in-memory recorder keeps its own richer per-provider-per-model
// metrics surface.
func AggregateTraces(traces []*HTTPTrace) *TraceAggregate {
	agg := &TraceAggregate{Count: len(traces)}
	if len(traces) == 0 {
		return agg
	}

	// traces are sorted most-recent-first, so the last is the oldest.
	agg.From = traces[len(traces)-1].Timestamp
	agg.To = traces[0].Timestamp

	durations := make([]int64, 0, len(traces))
	for _, tr := range traces {
		if tr.Status < 200 || tr.Status >= 300 || tr.Error != "" {
			agg.ErrorCount++
		}
		durations = append(durations, tr.DurationMs)
		if u := tr.Usage; u != nil {
			agg.Tokens.PromptTokens += u.PromptTokens
			agg.Tokens.CompletionTokens += u.CompletionTokens
			agg.Tokens.CacheReadTokens += u.CacheReadTokens
			agg.Tokens.CacheWriteTokens += u.CacheWriteTokens
			agg.Tokens.ReasoningTokens += u.ReasoningTokens
		}
	}
	// TotalTokens derives through the shared UsageTotals.TokenTotal (issue
	// #1240) — the sum of the four components, not each trace's stored total
	// (which may be zero on traces recorded before provider-usage enrichment) —
	// the same derivation the recorder's metrics snapshot uses.
	agg.Tokens.TotalTokens = agg.Tokens.TokenTotal()
	agg.ErrorRate = float64(agg.ErrorCount) / float64(len(traces))

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	agg.P50LatencyMs = percentile(durations, 0.50)
	agg.P95LatencyMs = percentile(durations, 0.95)
	return agg
}

// percentile returns the p-th percentile (0 < p < 1) of sorted values using
// linear interpolation (the R-7 method used by numpy/Excel).
func percentile(sorted []int64, p float64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	h := p * float64(n-1)
	lower := int(math.Floor(h))
	if lower >= n-1 {
		return sorted[n-1]
	}
	weight := h - float64(lower)
	return sorted[lower] + int64(math.Round(weight*float64(sorted[lower+1]-sorted[lower])))
}
