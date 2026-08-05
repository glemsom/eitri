package debug

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func openAIResponse(model string, usage string) string {
	return `{"id":"x","model":"` + model + `","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":` + usage + `}`
}

// TestRecorder_TraceEnrichment verifies that a trace recorded through the
// RoundTripper gains the model name (from the request body), provider usage,
// and finish reason (from the response body).
func TestRecorder_TraceEnrichment(t *testing.T) {
	r := NewRecorder(5)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(openAIResponse("gpt-4o-mini",
			`{"prompt_tokens":12,"completion_tokens":5,"total_tokens":17,"prompt_tokens_details":{"cached_tokens":4}}`)))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	resp, err := client.Post(srv.URL+"/v1/chat", "application/json",
		bytes.NewReader([]byte(`{"model":"gpt-4o-mini","messages":[]}`)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]
	if tr.Model != "gpt-4o-mini" {
		t.Fatalf("Model = %q, want gpt-4o-mini", tr.Model)
	}
	if tr.Usage == nil {
		t.Fatal("expected Usage on trace")
	}
	if tr.Usage.PromptTokens != 12 || tr.Usage.CompletionTokens != 5 {
		t.Fatalf("unexpected usage: %+v", tr.Usage)
	}
	if tr.Usage.CacheReadTokens != 4 {
		t.Fatalf("cache read = %d, want 4", tr.Usage.CacheReadTokens)
	}
	if tr.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want stop", tr.FinishReason)
	}
	if tr.ErrorClass != "" {
		t.Fatalf("ErrorClass = %q, want empty for success", tr.ErrorClass)
	}
}

// TestRecorder_AttemptFromContext verifies the retry attempt number is read
// from the request context and stamped onto the trace at capture time.
func TestRecorder_AttemptFromContext(t *testing.T) {
	r := NewRecorder(5)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat",
		bytes.NewReader([]byte(`{"model":"m1"}`)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req = req.WithContext(WithAttempt(req.Context(), 3))

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].Attempt != 3 {
		t.Fatalf("Attempt = %d, want 3", traces[0].Attempt)
	}

	// A request without the context value defaults to attempt 0.
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/chat",
		bytes.NewReader([]byte(`{"model":"m1"}`)))
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	traces = r.List(0, "", "")
	if traces[1].Attempt != 0 {
		t.Fatalf("Attempt without context = %d, want 0", traces[1].Attempt)
	}
}

// TestRecorder_StreamTailUsageBeyondCap verifies that a streaming response
// larger than MaxBodyBytes still yields usage and finish_reason from the
// stream tail.
func TestRecorder_StreamTailUsageBeyondCap(t *testing.T) {
	r := NewRecorder(5)

	var body strings.Builder
	for i := 0; i < 300; i++ {
		body.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"m1","choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", 1024) + `"},"finish_reason":null}]}`)
		body.WriteString("\n\n")
	}
	body.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"m1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	body.WriteString("\n\n")
	body.WriteString(`data: {"id":"x","object":"chat.completion.chunk","model":"m1","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`)
	body.WriteString("\n\n")
	body.WriteString("data: [DONE]\n\n")

	if body.Len() < MaxBodyBytes {
		t.Fatalf("fixture must exceed MaxBodyBytes, got %d", body.Len())
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(body.String()))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 10 * time.Second}

	resp, err := client.Post(srv.URL+"/v1/chat", "application/json",
		bytes.NewReader([]byte(`{"model":"m1","stream":true}`)))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]
	if tr.Usage == nil || !tr.Usage.HasTokens() {
		t.Fatal("expected usage captured from truncated stream tail")
	}
	if tr.Usage.PromptTokens != 100 || tr.Usage.CompletionTokens != 50 {
		t.Fatalf("unexpected usage: %+v", tr.Usage)
	}
	if tr.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want stop", tr.FinishReason)
	}
}

// TestRecorder_ErrorClassCapture verifies error classification happens at
// capture time and is stored on the trace.
func TestRecorder_ErrorClassCapture(t *testing.T) {
	r := NewRecorder(10)

	// 429 → rate_limit
	r.Record("s1", "p1", "POST", "/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"error":{"message":"rate limit exceeded"}}`), 429, time.Second, "", nil)
	// 401 → auth
	r.Record("s1", "p1", "POST", "/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"error":{"message":"invalid api key"}}`), 401, time.Second, "", nil)
	// transport error → network
	r.Record("s1", "p1", "POST", "/chat", []byte(`{"model":"m1"}`),
		nil, 0, time.Second, "dial tcp: connect: connection refused", nil)
	// 400 with context-length body → context_length
	r.Record("s1", "p1", "POST", "/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"error":{"message":"This model's maximum context length is 128000 tokens"}}`), 400, time.Second, "", nil)
	// 500 → other
	r.Record("s1", "p1", "POST", "/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"error":"boom"}`), 500, time.Second, "", nil)

	traces := r.List(0, "", "")
	want := []ErrorClass{ErrorClassRateLimit, ErrorClassAuth, ErrorClassNetwork, ErrorClassContextLength, ErrorClassOther}
	for i, w := range want {
		if traces[i].ErrorClass != w {
			t.Fatalf("trace %d ErrorClass = %q, want %q", i, traces[i].ErrorClass, w)
		}
	}
}

// TestRecorder_MetricsAccumulation drives a mixed success/error/retry workload
// through the recorder and verifies the aggregated per-provider-per-model
// counters.
func TestRecorder_MetricsAccumulation(t *testing.T) {
	r := NewRecorder(50)

	usageBody := func(model, usage string) []byte {
		return []byte(openAIResponse(model, usage))
	}
	reqBody := func(model string) []byte {
		return []byte(`{"model":"` + model + `","messages":[]}`)
	}

	// m1: 3 successes with usage (1 cache hit, 2 misses)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		usageBody("m1", `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}`), 200, 120*time.Millisecond, "", nil)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		usageBody("m1", `{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}`), 200, 400*time.Millisecond, "", nil)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		usageBody("m1", `{"prompt_tokens":6,"completion_tokens":2,"total_tokens":8}`), 200, 900*time.Millisecond, "", nil)
	// m1: success without usage
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"), []byte(`{"response":"ok"}`), 200, 50*time.Millisecond, "", nil)
	// m1: two retried calls (attempts 1 and 2), both success
	r.RecordWithMeta("s1", "p1", "POST", "/chat", reqBody("m1"),
		usageBody("m1", `{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}`), 200, 2500*time.Millisecond, "", nil,
		RecordMeta{Attempt: 1})
	r.RecordWithMeta("s1", "p1", "POST", "/chat", reqBody("m1"),
		usageBody("m1", `{"prompt_tokens":5,"completion_tokens":1,"total_tokens":6}`), 200, 3000*time.Millisecond, "", nil,
		RecordMeta{Attempt: 2})
	// m1: errors — 429, 401, network, context-length, 500
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		[]byte(`{"error":{"message":"rate limited"}}`), 429, 10*time.Millisecond, "", nil)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		[]byte(`{"error":{"message":"unauthorized"}}`), 401, 10*time.Millisecond, "", nil)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"), nil, 0, 7000*time.Millisecond,
		"dial tcp: connect: connection refused", nil)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		[]byte(`{"error":{"message":"maximum context length exceeded"}}`), 400, 10*time.Millisecond, "", nil)
	r.Record("s1", "p1", "POST", "/chat", reqBody("m1"),
		[]byte(`{"error":"internal"}`), 500, 60000*time.Millisecond, "", nil)
	// a different model to prove per-model keys
	r.Record("s1", "p2", "POST", "/chat", reqBody("m2"),
		usageBody("m2", `{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`), 200, 30*time.Millisecond, "", nil)

	snap := r.Metrics()

	if snap.TotalCalls != 12 {
		t.Fatalf("TotalCalls = %d, want 12", snap.TotalCalls)
	}
	if snap.TotalErrors != 5 {
		t.Fatalf("TotalErrors = %d, want 5", snap.TotalErrors)
	}
	if len(snap.Providers) != 2 {
		t.Fatalf("Providers = %d, want 2", len(snap.Providers))
	}

	var m1 *ModelMetrics
	var m2 *ModelMetrics
	for i := range snap.Providers {
		for j := range snap.Providers[i].Models {
			switch snap.Providers[i].Models[j].Model {
			case "m1":
				m1 = &snap.Providers[i].Models[j]
			case "m2":
				m2 = &snap.Providers[i].Models[j]
			}
		}
	}
	if m1 == nil || m2 == nil {
		t.Fatalf("missing model aggregates: m1=%v m2=%v", m1, m2)
	}

	if m1.Calls != 11 {
		t.Fatalf("m1 calls = %d, want 11", m1.Calls)
	}
	if m1.Retries != 2 {
		t.Fatalf("m1 retries = %d, want 2", m1.Retries)
	}
	if m1.Errors[ErrorClassRateLimit] != 1 || m1.Errors[ErrorClassAuth] != 1 ||
		m1.Errors[ErrorClassNetwork] != 1 || m1.Errors[ErrorClassContextLength] != 1 ||
		m1.Errors[ErrorClassOther] != 1 {
		t.Fatalf("m1 errors = %v", m1.Errors)
	}
	if m1.Cache.Hits != 1 || m1.Cache.Misses != 4 {
		t.Fatalf("m1 cache = %+v, want hits=1 misses=4", m1.Cache)
	}
	if m1.Tokens.PromptTokens != 34 { // 10+8+6+5+5
		t.Fatalf("m1 prompt tokens = %d, want 34", m1.Tokens.PromptTokens)
	}
	if m1.Tokens.CompletionTokens != 13 { // 5+4+2+1+1
		t.Fatalf("m1 completion tokens = %d, want 13", m1.Tokens.CompletionTokens)
	}
	if m1.Tokens.CacheReadTokens != 3 {
		t.Fatalf("m1 cache read tokens = %d, want 3", m1.Tokens.CacheReadTokens)
	}

	// Latency buckets: 120→le_250, 400→le_500, 900→le_1000, 50→le_100,
	// 2500→le_2500, 3000→le_5000, 10→le_100 (x3), 7000→le_10000,
	// 60000→inf
	if m1.Latency["le_100"] != 4 { // 50, 10, 10, 10
		t.Fatalf("le_100 = %d, want 4", m1.Latency["le_100"])
	}
	if m1.Latency["le_250"] != 1 {
		t.Fatalf("le_250 = %d, want 1", m1.Latency["le_250"])
	}
	if m1.Latency["le_500"] != 1 {
		t.Fatalf("le_500 = %d, want 1", m1.Latency["le_500"])
	}
	if m1.Latency["le_1000"] != 1 {
		t.Fatalf("le_1000 = %d, want 1", m1.Latency["le_1000"])
	}
	if m1.Latency["le_2500"] != 1 {
		t.Fatalf("le_2500 = %d, want 1", m1.Latency["le_2500"])
	}
	if m1.Latency["le_5000"] != 1 {
		t.Fatalf("le_5000 = %d, want 1", m1.Latency["le_5000"])
	}
	if m1.Latency["le_10000"] != 1 {
		t.Fatalf("le_10000 = %d, want 1", m1.Latency["le_10000"])
	}
	if m1.Latency["inf"] != 1 {
		t.Fatalf("inf = %d, want 1", m1.Latency["inf"])
	}
	if m1.LastError != ErrorClassOther {
		t.Fatalf("m1 last error = %q, want other", m1.LastError)
	}

	if m2.Calls != 1 || m2.Cache.Hits != 0 || m2.Cache.Misses != 1 {
		t.Fatalf("m2 aggregate = calls:%d cache:%+v", m2.Calls, m2.Cache)
	}
	if m2.Tokens.TotalTokens != 3 {
		t.Fatalf("m2 total tokens = %d, want 3", m2.Tokens.TotalTokens)
	}
}

// TestRecorder_MetricsEmpty verifies an empty recorder returns an empty (not
// nil) metrics snapshot.
func TestRecorder_MetricsEmpty(t *testing.T) {
	r := NewRecorder(5)
	snap := r.Metrics()
	if snap.TotalCalls != 0 || snap.TotalErrors != 0 {
		t.Fatalf("empty snapshot totals: %+v", snap)
	}
	if len(snap.Providers) != 0 {
		t.Fatalf("empty snapshot providers = %d, want 0", len(snap.Providers))
	}
	if snap.GeneratedAt.IsZero() {
		t.Fatal("GeneratedAt should be set")
	}
}

// TestRecorder_MetricsLoadedTraces verifies restored traces feed the same
// counters.
func TestRecorder_MetricsLoadedTraces(t *testing.T) {
	r := NewRecorder(5)
	r.Record("s1", "p1", "POST", "/chat", []byte(`{"model":"m1"}`),
		[]byte(`{"model":"m1","usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`), 200, time.Second, "", nil)

	r2 := NewRecorder(5)
	r2.LoadAll(r.List(0, "", ""))
	snap := r2.Metrics()
	if snap.TotalCalls != 1 {
		t.Fatalf("TotalCalls after LoadAll = %d, want 1", snap.TotalCalls)
	}
	if snap.Providers[0].Models[0].Tokens.PromptTokens != 4 {
		t.Fatalf("prompt tokens after LoadAll = %d, want 4", snap.Providers[0].Models[0].Tokens.PromptTokens)
	}
}

func TestAttemptContext(t *testing.T) {
	ctx := WithAttempt(context.Background(), 5)
	if got := AttemptFromContext(ctx); got != 5 {
		t.Fatalf("AttemptFromContext = %d, want 5", got)
	}
	if got := AttemptFromContext(context.Background()); got != 0 {
		t.Fatalf("AttemptFromContext(empty) = %d, want 0", got)
	}
}
