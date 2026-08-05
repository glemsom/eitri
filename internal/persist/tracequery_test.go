package persist

import (
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/session"
)

// mustWriteLiveSession writes a minimal session.json so the session counts as
// live (traces of deleted sessions — no session.json — must be excluded).
func mustWriteLiveSession(t *testing.T, p *Persister, sessionID string) {
	t.Helper()
	s := &session.UISession{ID: sessionID, Title: "test", Status: session.StatusIdle}
	if err := p.SnapshotSession(sessionID, s); err != nil {
		t.Fatalf("snapshot session %s: %v", sessionID, err)
	}
}

// mustWriteTrace persists a trace under the given session. The session must
// already be live on disk (mustWriteLiveSession).
func mustWriteTrace(t *testing.T, p *Persister, sessionID string, tr *debug.HTTPTrace) {
	t.Helper()
	if err := p.SaveTrace(sessionID, tr); err != nil {
		t.Fatalf("save trace %s: %v", tr.ID, err)
	}
}

func mustQueryTraces(t *testing.T, p *Persister, filter TraceFilter) *TracePage {
	t.Helper()
	page, err := p.QueryTraces(filter)
	if err != nil {
		t.Fatalf("QueryTraces(%+v): %v", filter, err)
	}
	return page
}

func mustAggregate(t *testing.T, p *Persister, filter TraceFilter) *TraceAggregate {
	t.Helper()
	agg, err := p.AggregateTraces(filter)
	if err != nil {
		t.Fatalf("AggregateTraces(%+v): %v", filter, err)
	}
	return agg
}

func TestQueryTraces_FiltersCombine(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	mustWriteLiveSession(t, p, "s1")
	mustWriteLiveSession(t, p, "s2")

	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{ID: "t1", SessionID: "s1", ProviderID: "p1", Model: "m1", Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)})
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{ID: "t2", SessionID: "s1", ProviderID: "p1", Model: "m2", Timestamp: time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)})
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{ID: "t3", SessionID: "s1", ProviderID: "p2", Model: "m1", Timestamp: time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC)})
	mustWriteTrace(t, p, "s2", &debug.HTTPTrace{ID: "t4", SessionID: "s2", ProviderID: "p1", Model: "m1", Timestamp: time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC)})
	mustWriteTrace(t, p, "s2", &debug.HTTPTrace{ID: "t5", SessionID: "s2", ProviderID: "p2", Model: "m2", Timestamp: time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)})

	// IDs sorted most-recent-first.
	ids := func(page *TracePage) []string {
		out := make([]string, 0, len(page.Traces))
		for _, tr := range page.Traces {
			out = append(out, string(tr.ID))
		}
		return out
	}

	tests := []struct {
		name   string
		filter TraceFilter
		want   []string
		total  int
	}{
		{
			name:   "no filter returns all",
			filter: TraceFilter{},
			want:   []string{"t5", "t4", "t3", "t2", "t1"},
			total:  5,
		},
		{
			name:   "by session",
			filter: TraceFilter{SessionID: "s1"},
			want:   []string{"t3", "t2", "t1"},
			total:  3,
		},
		{
			name:   "by provider",
			filter: TraceFilter{ProviderID: "p1"},
			want:   []string{"t4", "t2", "t1"},
			total:  3,
		},
		{
			name:   "by model",
			filter: TraceFilter{Model: "m1"},
			want:   []string{"t4", "t3", "t1"},
			total:  3,
		},
		{
			name:   "session and provider combine",
			filter: TraceFilter{SessionID: "s1", ProviderID: "p1"},
			want:   []string{"t2", "t1"},
			total:  2,
		},
		{
			name:   "all three combine",
			filter: TraceFilter{SessionID: "s1", ProviderID: "p1", Model: "m1"},
			want:   []string{"t1"},
			total:  1,
		},
		{
			name: "time range inclusive from, exclusive to",
			filter: TraceFilter{
				From: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
				To:   time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC),
			},
			want:  []string{"t3", "t2"},
			total: 2,
		},
		{
			name:   "no match",
			filter: TraceFilter{ProviderID: "nope"},
			want:   []string{},
			total:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := mustQueryTraces(t, p, tt.filter)
			if page.Total != tt.total {
				t.Errorf("total = %d, want %d", page.Total, tt.total)
			}
			got := ids(page)
			if len(got) != len(tt.want) {
				t.Fatalf("traces = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("traces[%d] = %s, want %s (full: %v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestQueryTraces_Pagination(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteLiveSession(t, p, "s1")

	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id := debug.TraceID("t" + string(rune('a'+i)))
		mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
			ID: id, SessionID: "s1", ProviderID: "p1", Model: "m1",
			Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}

	// Page 1: first two most-recent (te, td), total 5.
	page := mustQueryTraces(t, p, TraceFilter{Limit: 2, Offset: 0})
	if page.Total != 5 || len(page.Traces) != 2 {
		t.Fatalf("page1 total=%d len=%d, want total=5 len=2", page.Total, len(page.Traces))
	}
	if page.Traces[0].ID != "te" || page.Traces[1].ID != "td" {
		t.Errorf("page1 = [%s %s], want [te td]", page.Traces[0].ID, page.Traces[1].ID)
	}

	// Page 2: next two (tc, tb).
	page = mustQueryTraces(t, p, TraceFilter{Limit: 2, Offset: 2})
	if page.Total != 5 || len(page.Traces) != 2 {
		t.Fatalf("page2 total=%d len=%d, want total=5 len=2", page.Total, len(page.Traces))
	}
	if page.Traces[0].ID != "tc" || page.Traces[1].ID != "tb" {
		t.Errorf("page2 = [%s %s], want [tc tb]", page.Traces[0].ID, page.Traces[1].ID)
	}

	// Offset beyond the result set yields an empty page but a correct total.
	page = mustQueryTraces(t, p, TraceFilter{Limit: 2, Offset: 99})
	if page.Total != 5 || len(page.Traces) != 0 {
		t.Errorf("past-end total=%d len=%d, want total=5 len=0", page.Total, len(page.Traces))
	}
}

func TestQueryTraces_DeletedSessionsExcluded(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	mustWriteLiveSession(t, p, "live")
	mustWriteTrace(t, p, "live", &debug.HTTPTrace{
		ID: "live1", SessionID: "live", ProviderID: "p1", Model: "m1",
		Timestamp: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
	})

	// A session with traces on disk but no session.json — as if permanently
	// deleted. Write the trace files directly to simulate the leftover state.
	if err := p.SnapshotSession("deleted", &session.UISession{ID: "deleted"}); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveTrace("deleted", &debug.HTTPTrace{
		ID: "gone1", SessionID: "deleted", ProviderID: "p2", Model: "m2",
		Timestamp: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	// Now permanently delete the session.
	if err := p.DeleteSession("deleted"); err != nil {
		t.Fatal(err)
	}

	page := mustQueryTraces(t, p, TraceFilter{})
	if page.Total != 1 || len(page.Traces) != 1 {
		t.Fatalf("total=%d len=%d, want total=1 len=1 (deleted-session traces excluded)", page.Total, len(page.Traces))
	}
	if page.Traces[0].ID != "live1" {
		t.Errorf("trace = %s, want live1", page.Traces[0].ID)
	}

	// Session filter for the deleted session also returns nothing.
	page = mustQueryTraces(t, p, TraceFilter{SessionID: "deleted"})
	if page.Total != 0 || len(page.Traces) != 0 {
		t.Errorf("deleted-session query total=%d len=%d, want 0", page.Total, len(page.Traces))
	}
}

func TestQueryTraces_SurvivesRestart(t *testing.T) {
	rootDir := t.TempDir()

	p1, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteLiveSession(t, p1, "s1")
	for i, dur := range []int64{100, 250, 700} {
		tr := &debug.HTTPTrace{
			ID: debug.TraceID(string(rune('r'+i)) + "trace"), SessionID: "s1",
			ProviderID: "opencode_go", Model: "deepseek-v4",
			Timestamp:    time.Date(2026, 4, 1, 0, 0, int(i), 0, time.UTC),
			Status:       200,
			DurationMs:   dur,
			RequestBody:  "req",
			ResponseBody: "resp",
			Usage:        &debug.UsageTotals{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
		}
		mustWriteTrace(t, p1, "s1", tr)
	}

	// Simulate a server restart: a fresh persister (and recorder) over the
	// same data directory. The recorded traces must round-trip untouched.
	p2, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	page := mustQueryTraces(t, p2, TraceFilter{ProviderID: "opencode_go", Model: "deepseek-v4"})
	if page.Total != 3 {
		t.Fatalf("total = %d, want 3", page.Total)
	}
	// Most recent first: r2trace (700ms), r1trace (250ms), r0trace (100ms).
	wantOrder := []int64{700, 250, 100}
	for i, want := range wantOrder {
		if page.Traces[i].DurationMs != want {
			t.Errorf("traces[%d].duration_ms = %d, want %d", i, page.Traces[i].DurationMs, want)
		}
	}
	// Body and usage enrichment survive the disk round-trip.
	first := page.Traces[0]
	if first.RequestBody != "req" || first.ResponseBody != "resp" {
		t.Errorf("bodies not preserved: %q / %q", first.RequestBody, first.ResponseBody)
	}
	if first.Usage == nil || first.Usage.TotalTokens != 30 {
		t.Errorf("usage not preserved: %+v", first.Usage)
	}
}

func TestAggregateTraces_Math(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteLiveSession(t, p, "s1")

	// Durations sorted: 100, 200, 300, 400, 500. One failure (status 500).
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
		ID: "a", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 200, DurationMs: 100,
		Timestamp: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		Usage:     &debug.UsageTotals{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	})
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
		ID: "b", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 200, DurationMs: 200,
		Timestamp: time.Date(2026, 5, 1, 0, 0, 1, 0, time.UTC),
	})
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
		ID: "c", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 500, DurationMs: 300,
		Timestamp: time.Date(2026, 5, 1, 0, 0, 2, 0, time.UTC),
		Usage:     &debug.UsageTotals{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
	})
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
		ID: "d", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 200, DurationMs: 400,
		Timestamp: time.Date(2026, 5, 1, 0, 0, 3, 0, time.UTC),
	})
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
		ID: "e", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 200, DurationMs: 500,
		Timestamp: time.Date(2026, 5, 1, 0, 0, 4, 0, time.UTC),
	})

	agg := mustAggregate(t, p, TraceFilter{})
	if agg.Count != 5 {
		t.Errorf("count = %d, want 5", agg.Count)
	}
	if agg.ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", agg.ErrorCount)
	}
	if agg.ErrorRate != 0.2 {
		t.Errorf("error_rate = %v, want 0.2", agg.ErrorRate)
	}
	if agg.P50LatencyMs != 300 {
		t.Errorf("p50 = %d, want 300", agg.P50LatencyMs)
	}
	if agg.P95LatencyMs != 480 {
		t.Errorf("p95 = %d, want 480", agg.P95LatencyMs)
	}
	if agg.Tokens.PromptTokens != 15 || agg.Tokens.CompletionTokens != 25 || agg.Tokens.TotalTokens != 40 {
		t.Errorf("tokens = %+v, want prompt=15 completion=25 total=40", agg.Tokens)
	}
	if agg.From.IsZero() || agg.To.IsZero() {
		t.Errorf("window from/to not set: %v → %v", agg.From, agg.To)
	}
}

func TestAggregateTraces_TotalTokensRecomputedFromComponents(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteLiveSession(t, p, "s1")

	// Stored usage has prompt/completion set but a zero TotalTokens field (a
	// trace recorded before provider-usage enrichment). The aggregate must
	// derive the total from the components instead of trusting the field.
	mustWriteTrace(t, p, "s1", &debug.HTTPTrace{
		ID: "a", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 200, DurationMs: 100,
		Timestamp: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		Usage:     &debug.UsageTotals{PromptTokens: 10, CompletionTokens: 20},
	})

	agg := mustAggregate(t, p, TraceFilter{})
	if agg.Tokens.TotalTokens != 30 {
		t.Errorf("total_tokens = %d, want 30 (recomputed from prompt+completion)", agg.Tokens.TotalTokens)
	}
	if agg.Tokens.PromptTokens != 10 || agg.Tokens.CompletionTokens != 20 {
		t.Errorf("tokens = %+v, want prompt=10 completion=20", agg.Tokens)
	}
}

func TestAggregateTraces_EmptyWindow(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteLiveSession(t, p, "s1")

	agg := mustAggregate(t, p, TraceFilter{})
	if agg.Count != 0 || agg.ErrorCount != 0 || agg.ErrorRate != 0 {
		t.Errorf("empty aggregate = %+v, want zeros", agg)
	}
	if agg.P50LatencyMs != 0 || agg.P95LatencyMs != 0 {
		t.Errorf("empty percentiles = %d/%d, want 0", agg.P50LatencyMs, agg.P95LatencyMs)
	}
}

func TestAggregateTraces_RespectsFilters(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteLiveSession(t, p, "s1")

	ok := &debug.HTTPTrace{
		ID: "ok", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 200, DurationMs: 100,
		Timestamp: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	fail := &debug.HTTPTrace{
		ID: "fail", SessionID: "s1", ProviderID: "p1", Model: "m1", Status: 500, DurationMs: 400,
		Timestamp: time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC),
	}
	mustWriteTrace(t, p, "s1", ok)
	mustWriteTrace(t, p, "s1", fail)

	// Filter to just the successful trace (From inclusive, To exclusive).
	agg := mustAggregate(t, p, TraceFilter{
		Model: "m1",
		From:  time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		To:    time.Date(2026, 6, 1, 0, 30, 0, 0, time.UTC),
	})
	if agg.Count != 1 || agg.ErrorCount != 0 {
		t.Errorf("aggregate over window = count %d errors %d, want 1/0", agg.Count, agg.ErrorCount)
	}
	if agg.P50LatencyMs != 100 {
		t.Errorf("p50 = %d, want 100", agg.P50LatencyMs)
	}
}
