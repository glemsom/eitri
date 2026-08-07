package persist

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/debug"
)

// TraceFilter is the query surface over the persisted HTTP trace archive.
// Every field is optional: zero values mean "no constraint". From is an
// inclusive lower bound on the trace timestamp, To an exclusive upper bound.
type TraceFilter struct {
	SessionID  string
	ProviderID string
	Model      string
	From       time.Time
	To         time.Time
	Limit      int // max traces to return; 0 = no limit
	Offset     int // most-recent matches to skip (pagination)
}

// TracePage is a page of persisted-trace query results. Total counts all
// matching traces ignoring Limit/Offset, so callers can paginate.
type TracePage struct {
	Total  int                `json:"total"`
	Offset int                `json:"offset"`
	Limit  int                `json:"limit"`
	Traces []*debug.HTTPTrace `json:"traces"`
}

// TraceAggregate is a window aggregate over persisted traces matching a
// TraceFilter. ErrorRate is the fraction of calls that failed (non-2xx status
// or transport error), in [0,1]. P50LatencyMs and P95LatencyMs are computed
// over all matching call durations. Tokens sum provider-reported usage. From
// and To bound the actual matching window (oldest/newest trace timestamps).
type TraceAggregate struct {
	Count        int               `json:"count"`
	ErrorCount   int               `json:"error_count"`
	ErrorRate    float64           `json:"error_rate"`
	P50LatencyMs int64             `json:"p50_latency_ms"`
	P95LatencyMs int64             `json:"p95_latency_ms"`
	Tokens       debug.UsageTotals `json:"tokens"`
	From         time.Time         `json:"from,omitempty"`
	To           time.Time         `json:"to,omitempty"`
}

// Matches reports whether the trace satisfies the filter's constraints.
func (f TraceFilter) Matches(t *debug.HTTPTrace) bool {
	if f.SessionID != "" && t.SessionID != f.SessionID {
		return false
	}
	if f.ProviderID != "" && t.ProviderID != f.ProviderID {
		return false
	}
	if f.Model != "" && t.Model != f.Model {
		return false
	}
	if !f.From.IsZero() && t.Timestamp.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && !t.Timestamp.Before(f.To) {
		return false
	}
	return true
}

// QueryTraces scans the persisted trace archive and returns the matching
// traces sorted most-recent-first. Offset and Limit implement pagination over
// the sorted, filtered result set. Traces of permanently deleted sessions (no
// session.json on disk) are excluded.
func (p *Persister) QueryTraces(filter TraceFilter) (*TracePage, error) {
	matches, err := p.scanTraces(filter)
	if err != nil {
		return nil, err
	}

	page := &TracePage{Total: len(matches), Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset > 0 {
		if filter.Offset >= len(matches) {
			page.Traces = []*debug.HTTPTrace{}
			return page, nil
		}
		matches = matches[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(matches) {
		matches = matches[:filter.Limit]
	}
	if len(matches) == 0 {
		page.Traces = []*debug.HTTPTrace{}
		return page, nil
	}
	page.Traces = matches
	return page, nil
}

// AggregateTraces returns window aggregates over the persisted traces matching
// the filter (Limit and Offset are ignored).
func (p *Persister) AggregateTraces(filter TraceFilter) (*TraceAggregate, error) {
	matches, err := p.scanTraces(filter)
	if err != nil {
		return nil, err
	}
	return aggregateTraces(matches), nil
}

// scanTraces walks the on-disk trace archive and returns every trace matching
// the filter, sorted most-recent-first. Sessions without a session.json are
// treated as permanently deleted and their traces are excluded.
func (p *Persister) scanTraces(filter TraceFilter) ([]*debug.HTTPTrace, error) {
	sessionsDir := filepath.Join(p.rootDir, "sessions")

	var sessionDirs []string
	if filter.SessionID != "" {
		// Fast path: only one session can match.
		sessionDirs = []string{filter.SessionID}
	} else {
		entries, err := os.ReadDir(sessionsDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("cannot list sessions dir %s: %w", sessionsDir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				sessionDirs = append(sessionDirs, e.Name())
			}
		}
	}

	var matches []*debug.HTTPTrace
	for _, sessionID := range sessionDirs {
		sessionDir := filepath.Join(sessionsDir, sessionID)
		// Deleted sessions have no session.json — their traces must not be
		// resurrected by queries (single owner: sessionExistsOnDisk, mirrors
		// Restore and SaveTrace).
		if !p.sessionExistsOnDisk(sessionID) {
			continue
		}

		tracesDir := filepath.Join(sessionDir, "traces")
		entries, err := os.ReadDir(tracesDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("cannot list traces dir for %s: %w", sessionID, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(tracesDir, entry.Name()))
			if err != nil {
				return nil, fmt.Errorf("cannot read trace file %s: %w", entry.Name(), err)
			}
			var trace debug.HTTPTrace
			if err := json.Unmarshal(data, &trace); err != nil {
				return nil, fmt.Errorf("cannot unmarshal trace %s: %w", entry.Name(), err)
			}
			if !filter.Matches(&trace) {
				continue
			}
			matches = append(matches, &trace)
		}
	}

	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Timestamp.After(matches[j].Timestamp)
	})
	return matches, nil
}

// aggregateTraces folds a set of traces (already matching a filter, sorted
// most-recent-first) into a window aggregate.
func aggregateTraces(traces []*debug.HTTPTrace) *TraceAggregate {
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
	// TotalTokens mirrors the recorder's metrics aggregation: the sum of the
	// four components, not each trace's stored total (which may be zero on
	// traces recorded before provider-usage enrichment).
	agg.Tokens.TotalTokens = agg.Tokens.PromptTokens + agg.Tokens.CompletionTokens +
		agg.Tokens.CacheReadTokens + agg.Tokens.CacheWriteTokens
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
