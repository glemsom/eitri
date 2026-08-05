package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
)

// TestDebugMetrics returns the aggregated per-provider-per-model counters and
// documents the JSON shape of GET /api/debug/metrics.
func TestDebugMetrics(t *testing.T) {
	rec := debug.NewRecorder(50)

	// p1/m1: success with cache-hit usage, retried call, and a 429 error.
	rec.Record("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"model":"m1","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}},"choices":[{"finish_reason":"stop"}]}`),
		200, 120*time.Millisecond, "", nil)
	rec.RecordWithMeta("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"model":"m1","usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`),
		200, 400*time.Millisecond, "", nil, debug.RecordMeta{Attempt: 1})
	rec.Record("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"error":{"message":"rate limited"}}`), 429, 10*time.Millisecond, "", nil)
	// p1/m2: one call.
	rec.Record("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"m2"}`),
		[]byte(`{"model":"m2","usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`),
		200, 30*time.Millisecond, "", nil)

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{debugRecorder: rec})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q, want application/json", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if got := body["total_calls"].(float64); got != 4 {
		t.Fatalf("total_calls = %v, want 4", got)
	}
	if got := body["total_errors"].(float64); got != 1 {
		t.Fatalf("total_errors = %v, want 1", got)
	}
	if _, ok := body["generated_at"].(string); !ok {
		t.Fatal("generated_at missing or not a string")
	}

	providers, ok := body["providers"].([]any)
	if !ok {
		t.Fatalf("providers missing or not an array: %#v", body["providers"])
	}
	if len(providers) != 1 {
		t.Fatalf("providers = %d, want 1", len(providers))
	}
	provider := providers[0].(map[string]any)
	if provider["provider_id"] != "p1" {
		t.Fatalf("provider_id = %v, want p1", provider["provider_id"])
	}
	if got := provider["total_calls"].(float64); got != 4 {
		t.Fatalf("provider total_calls = %v, want 4", got)
	}

	models := provider["models"].([]any)
	if len(models) != 2 {
		t.Fatalf("models = %d, want 2", len(models))
	}
	first := models[0].(map[string]any)
	if first["model"] != "m1" {
		t.Fatalf("first model = %v, want m1 (sorted)", first["model"])
	}
	if got := first["calls"].(float64); got != 3 {
		t.Fatalf("m1 calls = %v, want 3", got)
	}
	if got := first["retries"].(float64); got != 1 {
		t.Fatalf("m1 retries = %v, want 1", got)
	}
	errors := first["errors"].(map[string]any)
	if errors["rate_limit"] != float64(1) {
		t.Fatalf("m1 rate_limit errors = %v, want 1", errors["rate_limit"])
	}
	if errors["network"] != float64(0) {
		t.Fatalf("m1 network errors = %v, want 0", errors["network"])
	}
	latency := first["latency_ms"].(map[string]any)
	if latency["le_250"] != float64(1) {
		t.Fatalf("m1 latency le_250 = %v, want 1", latency["le_250"])
	}
	tokens := first["tokens"].(map[string]any)
	if tokens["prompt_tokens"] != float64(18) || tokens["completion_tokens"] != float64(9) {
		t.Fatalf("m1 tokens = %v, want prompt=18 completion=9", tokens)
	}
	if tokens["cache_read_tokens"] != float64(3) {
		t.Fatalf("m1 cache_read_tokens = %v, want 3", tokens["cache_read_tokens"])
	}
	cache := first["cache"].(map[string]any)
	if cache["hits"] != float64(1) || cache["misses"] != float64(1) {
		t.Fatalf("m1 cache = %v, want hits=1 misses=1", cache)
	}
	if first["last_error"] != "rate_limit" {
		t.Fatalf("m1 last_error = %v, want rate_limit", first["last_error"])
	}

	second := models[1].(map[string]any)
	if second["model"] != "m2" {
		t.Fatalf("second model = %v, want m2", second["model"])
	}
}

// TestDebugMetrics_NoRecorder verifies the endpoint reports 404 when no
// recorder is configured.
func TestDebugMetrics_NoRecorder(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestDebugMetrics_Empty verifies an empty recorder yields an empty aggregate
// (not null).
func TestDebugMetrics_Empty(t *testing.T) {
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{debugRecorder: debug.NewRecorder(5)})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["total_calls"] != float64(0) {
		t.Fatalf("total_calls = %v, want 0", body["total_calls"])
	}
	providers, ok := body["providers"].([]any)
	if !ok || len(providers) != 0 {
		t.Fatalf("providers = %#v, want empty array", body["providers"])
	}
}
