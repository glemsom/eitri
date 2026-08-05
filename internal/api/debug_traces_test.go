package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/session"
)

// seedPersistedTraces writes session snapshots and traces into a persister on
// disk (the same layout a running server would produce), so the debug API can
// query them.
func seedPersistedTraces(t *testing.T, p *persist.Persister) {
	t.Helper()
	writeSnapshot := func(id string) {
		if err := p.SnapshotSession(id, &session.UISession{ID: id, Title: id, Status: session.StatusIdle}); err != nil {
			t.Fatalf("snapshot %s: %v", id, err)
		}
	}
	save := func(sessionID string, tr *debug.HTTPTrace) {
		if err := p.SaveTrace(sessionID, tr); err != nil {
			t.Fatalf("save trace %s: %v", tr.ID, err)
		}
	}

	writeSnapshot("s1")
	writeSnapshot("s2")
	writeSnapshot("s3")

	save("s1", &debug.HTTPTrace{
		ID: "a1", SessionID: "s1", ProviderID: "opencode_go", Model: "deepseek-v4", Status: 200,
		DurationMs: 100, Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Usage: &debug.UsageTotals{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
	})
	save("s1", &debug.HTTPTrace{
		ID: "a2", SessionID: "s1", ProviderID: "opencode_go", Model: "deepseek-v4", Status: 200,
		DurationMs: 250, Timestamp: time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC),
	})
	save("s1", &debug.HTTPTrace{
		ID: "a3", SessionID: "s1", ProviderID: "opencode_go", Model: "deepseek-v4", Status: 500,
		DurationMs: 400, Timestamp: time.Date(2026, 1, 3, 10, 0, 0, 0, time.UTC),
	})
	save("s2", &debug.HTTPTrace{
		ID: "b1", SessionID: "s2", ProviderID: "github_copilot", Model: "gpt-x", Status: 200,
		DurationMs: 800, Timestamp: time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC),
	})
	save("s3", &debug.HTTPTrace{
		ID: "c1", SessionID: "s3", ProviderID: "opencode_go", Model: "qwen-x", Status: 200,
		DurationMs: 50, Timestamp: time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC),
	})
}

// getJSON performs a GET and decodes the JSON body, failing the test on error.
func getJSON(t *testing.T, url string) (map[string]any, int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return body, resp.StatusCode
}

func traceIDs(body map[string]any) []string {
	raw, _ := body["traces"].([]any)
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		if tr, ok := item.(map[string]any); ok {
			if id, ok := tr["id"].(string); ok {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// TestDebugTraces_QueriesPersistedArchive exercises GET /api/debug/traces:
// filtering, ordering, pagination, and the JSON shape.
func TestDebugTraces_QueriesPersistedArchive(t *testing.T) {
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedPersistedTraces(t, p)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p})
	defer server.Close()

	// No filters: all 5 traces, most-recent-first (default limit 20).
	body, status := getJSON(t, server.URL+"/api/debug/traces")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if total := body["total"].(float64); total != 5 {
		t.Errorf("total = %v, want 5", total)
	}
	wantAll := []string{"c1", "b1", "a3", "a2", "a1"}
	if got := traceIDs(body); !equalStrings(got, wantAll) {
		t.Errorf("traces = %v, want %v", got, wantAll)
	}

	// Filter by session + model.
	body, _ = getJSON(t, server.URL+"/api/debug/traces?session_id=s1&model=deepseek-v4")
	if total := body["total"].(float64); total != 3 {
		t.Errorf("s1+model total = %v, want 3", total)
	}
	if got := traceIDs(body); !equalStrings(got, []string{"a3", "a2", "a1"}) {
		t.Errorf("s1+model traces = %v", got)
	}

	// Provider filter.
	body, _ = getJSON(t, server.URL+"/api/debug/traces?provider_id=github_copilot")
	if total := body["total"].(float64); total != 1 {
		t.Errorf("provider total = %v, want 1", total)
	}

	// Time-range filter: 2026-01-02 00:00 (inclusive) → 2026-01-03 00:00 (exclusive).
	from := url.QueryEscape("2026-01-02T00:00:00Z")
	to := url.QueryEscape("2026-01-03T00:00:00Z")
	body, _ = getJSON(t, server.URL+"/api/debug/traces?from="+from+"&to="+to)
	if total := body["total"].(float64); total != 1 {
		t.Errorf("time-range total = %v, want 1", total)
	}
	if got := traceIDs(body); !equalStrings(got, []string{"a2"}) {
		t.Errorf("time-range traces = %v, want [a2]", got)
	}

	// Invalid timestamp → 400.
	_, status = getJSON(t, server.URL+"/api/debug/traces?from=not-a-time")
	if status != http.StatusBadRequest {
		t.Errorf("bad from status = %d, want 400", status)
	}
}

// TestDebugTraces_InvalidParams rejects malformed query parameters with 400.
func TestDebugTraces_InvalidParams(t *testing.T) {
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedPersistedTraces(t, p)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p})
	defer server.Close()

	for _, q := range []string{
		"?from=not-a-time",
		"?to=2026-13-99T00:00:00Z",
		"?limit=abc",
		"?limit=-5",
		"?offset=-1",
		"?offset=1.5",
	} {
		_, status := getJSON(t, server.URL+"/api/debug/traces"+q)
		if status != http.StatusBadRequest {
			t.Errorf("GET /api/debug/traces%s status = %d, want 400", q, status)
		}
	}
	_, status := getJSON(t, server.URL+"/api/debug/traces/aggregate?from=oops")
	if status != http.StatusBadRequest {
		t.Errorf("aggregate bad from status = %d, want 400", status)
	}
}

// TestDebugTraces_Pagination walks the archive page by page.
func TestDebugTraces_Pagination(t *testing.T) {
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedPersistedTraces(t, p)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p})
	defer server.Close()

	body, status := getJSON(t, server.URL+"/api/debug/traces?limit=2&offset=0")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if total := body["total"].(float64); total != 5 {
		t.Errorf("page total = %v, want 5", total)
	}
	if limit := body["limit"].(float64); limit != 2 {
		t.Errorf("limit = %v, want 2", limit)
	}
	if got := traceIDs(body); !equalStrings(got, []string{"c1", "b1"}) {
		t.Errorf("page1 = %v, want [c1 b1]", got)
	}

	body, _ = getJSON(t, server.URL+"/api/debug/traces?limit=2&offset=2")
	if got := traceIDs(body); !equalStrings(got, []string{"a3", "a2"}) {
		t.Errorf("page2 = %v, want [a3 a2]", got)
	}

	body, _ = getJSON(t, server.URL+"/api/debug/traces?limit=2&offset=4")
	if got := traceIDs(body); !equalStrings(got, []string{"a1"}) {
		t.Errorf("page3 = %v, want [a1]", got)
	}
}

// TestDebugTracesAggregate checks the window aggregation endpoint's math and
// filter combination.
func TestDebugTracesAggregate(t *testing.T) {
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedPersistedTraces(t, p)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p})
	defer server.Close()

	body, status := getJSON(t, server.URL+"/api/debug/traces/aggregate")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["count"].(float64) != 5 {
		t.Errorf("count = %v, want 5", body["count"])
	}
	if body["error_count"].(float64) != 1 {
		t.Errorf("error_count = %v, want 1", body["error_count"])
	}
	if body["error_rate"].(float64) != 0.2 {
		t.Errorf("error_rate = %v, want 0.2", body["error_rate"])
	}
	if body["p50_latency_ms"].(float64) != 250 {
		t.Errorf("p50 = %v, want 250", body["p50_latency_ms"])
	}
	if body["p95_latency_ms"].(float64) != 720 {
		t.Errorf("p95 = %v, want 720", body["p95_latency_ms"])
	}
	tokens := body["tokens"].(map[string]any)
	if tokens["prompt_tokens"].(float64) != 10 || tokens["total_tokens"].(float64) != 30 {
		t.Errorf("tokens = %v, want prompt=10 total=30", tokens)
	}
	if _, ok := body["generated_at"].(string); !ok {
		t.Error("generated_at missing")
	}

	// Filter by session: only s1's traces (count 3, one error).
	body, _ = getJSON(t, server.URL+"/api/debug/traces/aggregate?session_id=s1")
	if body["count"].(float64) != 3 || body["error_count"].(float64) != 1 {
		t.Errorf("s1 aggregate = count %v errors %v, want 3/1", body["count"], body["error_count"])
	}

	// Filter that matches nothing.
	body, _ = getJSON(t, server.URL+"/api/debug/traces/aggregate?provider_id=nope")
	if body["count"].(float64) != 0 || body["error_rate"].(float64) != 0 {
		t.Errorf("empty aggregate = %v, want count=0 rate=0", body)
	}
}

// TestDebugTraces_RestartConsistency is the acceptance test for issue #989:
// a query spanning a server restart returns consistent data. The first server
// records traces through its persister; after a simulated restart (fresh
// recorder + persister + server over the same data dir) the query returns the
// same traces and aggregates.
func TestDebugTraces_RestartConsistency(t *testing.T) {
	rootDir := t.TempDir()

	// First "server run": persist some traces via the normal async path.
	p1, err := persist.New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	seedPersistedTraces(t, p1)
	// The recorder is the in-memory view; traces land on disk via OnComplete
	// (here we call SaveTraceAsync to mirror the production wiring).
	rec := debug.NewRecorder(0)
	rec.OnComplete = func(tr *debug.HTTPTrace) { p1.SaveTraceAsync(tr.SessionID, tr) }
	rec.Record("s1", "opencode_go", "POST", "/v1/chat", []byte(`{"model":"deepseek-v4"}`),
		[]byte(`{"model":"deepseek-v4","usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`),
		200, 120*time.Millisecond, "", nil)
	p1.Flush(nil, nil) // drain the async queue before "restarting"

	server1 := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p1, debugRecorder: rec})
	defer server1.Close()
	before, status := getJSON(t, server1.URL+"/api/debug/traces?session_id=s1")
	if status != http.StatusOK {
		t.Fatalf("pre-restart status = %d, want 200", status)
	}

	// Simulated restart: a fresh recorder hydrated from disk and a fresh
	// server over the same data directory.
	restored, err := p1.Restore()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := persist.New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := debug.NewRecorder(0)
	rec2.LoadAll(restored.Traces)
	server2 := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p2, debugRecorder: rec2})
	defer server2.Close()

	after, status := getJSON(t, server2.URL+"/api/debug/traces?session_id=s1")
	if status != http.StatusOK {
		t.Fatalf("post-restart status = %d, want 200", status)
	}
	if before["total"].(float64) != after["total"].(float64) {
		t.Errorf("total before=%v after=%v, want equal", before["total"], after["total"])
	}
	if got, want := traceIDs(after), traceIDs(before); !equalStrings(got, want) {
		t.Errorf("traces after restart = %v, before = %v — inconsistent", got, want)
	}

	aggBefore, _ := getJSON(t, server1.URL+"/api/debug/traces/aggregate?session_id=s1")
	aggAfter, _ := getJSON(t, server2.URL+"/api/debug/traces/aggregate?session_id=s1")
	if aggBefore["count"].(float64) != aggAfter["count"].(float64) {
		t.Errorf("aggregate count before=%v after=%v, want equal", aggBefore["count"], aggAfter["count"])
	}
}

// TestDebugTraces_DeletedSessionsExcluded is the acceptance test for issue
// #989: traces of permanently deleted sessions are excluded from queries and
// aggregates.
func TestDebugTraces_DeletedSessionsExcluded(t *testing.T) {
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedPersistedTraces(t, p)
	// Permanently delete session s2 (removes its directory, including traces).
	if err := p.DeleteSession("s2"); err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{persister: p})
	defer server.Close()

	body, _ := getJSON(t, server.URL+"/api/debug/traces")
	if total := body["total"].(float64); total != 4 {
		t.Errorf("total = %v, want 4 (s2 traces excluded)", total)
	}
	for _, id := range traceIDs(body) {
		if id == "b1" {
			t.Error("deleted-session trace b1 still queryable")
		}
	}

	agg, _ := getJSON(t, server.URL+"/api/debug/traces/aggregate")
	if agg["count"].(float64) != 4 {
		t.Errorf("aggregate count = %v, want 4", agg["count"])
	}
}

// TestDebugTraces_NoPersister returns 404 when no archive is available.
func TestDebugTraces_NoPersister(t *testing.T) {
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{debugRecorder: debug.NewRecorder(5)})
	defer server.Close()

	for _, path := range []string{"/api/debug/traces", "/api/debug/traces/aggregate"} {
		_, status := getJSON(t, server.URL+path)
		if status != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, status)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
