package persist

import (
	"encoding/json"
	"fmt"
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
// TraceFilter. The type and its fold live in the debug package — the single
// owner of trace aggregation (issue #1240) — and are aliased here so the
// persisted archive query surface keeps its original vocabulary.
type TraceAggregate = debug.TraceAggregate

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
// the filter (Limit and Offset are ignored). The aggregation itself is owned
// by the debug package — the single aggregation implementation shared by the
// trace endpoints (issue #1240).
func (p *Persister) AggregateTraces(filter TraceFilter) (*TraceAggregate, error) {
	matches, err := p.scanTraces(filter)
	if err != nil {
		return nil, err
	}
	return debug.AggregateTraces(matches), nil
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
		// Restore and SaveTrace). A stat error that is not "does not exist"
		// is surfaced, not treated as deletion (pre-#1237 behaviour).
		exists, err := p.sessionExistsOnDisk(sessionID)
		if err != nil {
			return nil, fmt.Errorf("cannot stat session file for %s: %w", sessionID, err)
		}
		if !exists {
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
