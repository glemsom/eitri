package debug

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorder_OnCompleteFiresOutsideLock(t *testing.T) {
	r := NewRecorder(10)
	done := make(chan struct{})

	r.OnComplete = func(trace *HTTPTrace) {
		// Re-entering the recorder from OnComplete must not deadlock: the
		// callback fires after the recorder mutex is released. With the old
		// (non-reentrant) locking these read calls would block forever.
		_ = r.Count()
		_ = r.List(0, "", "")
		_ = r.InFlight()
		_ = r.Get(trace.ID)
		close(done)
	}

	// Both completion paths — Record and the streaming traceBody path — must
	// fire OnComplete off the lock.
	r.Record("s1", "p1", "GET", "/record", nil, nil, 200, time.Second, "", nil)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record: OnComplete deadlocked while holding the recorder lock")
	}

	done = make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL + "/stream")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("completeTrace: OnComplete deadlocked while holding the recorder lock")
	}
}

func TestRecorder_InFlightCap_EvictsOldest(t *testing.T) {
	r := NewRecorder(10)
	r.maxInFlight = 2

	var ids []TraceID
	for i := 0; i < 3; i++ {
		ids = append(ids, r.startTrace("s1", "p1", "GET", "/", nil, 0, "", 0))
		time.Sleep(2 * time.Millisecond) // distinct start timestamps for oldest
	}

	// In-flight tracking stays bounded at the cap.
	inflight := r.InFlight()
	if len(inflight) != 2 {
		t.Fatalf("got %d in-flight traces, want 2 (cap)", len(inflight))
	}

	// The oldest in-flight trace was evicted into completed storage with an
	// eviction marker.
	var evicted *HTTPTrace
	for _, tr := range r.List(0, "", "") {
		if tr.ID == ids[0] {
			evicted = tr
		}
	}
	if evicted == nil {
		t.Fatal("expected the evicted (oldest) in-flight trace to be recorded as completed")
	}
	if !strings.Contains(evicted.Error, "evicted") {
		t.Fatalf("evicted trace Error = %q, want eviction marker", evicted.Error)
	}
	if evicted.Status != 0 {
		t.Fatalf("evicted trace Status = %d, want 0 (in-flight marker)", evicted.Status)
	}

	// The two newest traces remain tracked as in-flight.
	for _, id := range ids[1:] {
		if r.Get(id) == nil {
			t.Fatalf("in-flight trace %s should still be tracked", id)
		}
	}

	// Completing an already-evicted trace later must be a safe no-op.
	r.completeTrace(ids[0], []byte("late body"), 200, time.Second, "", nil, nil, "")
	if got := r.Get(ids[0]); got == nil {
		t.Fatal("evicted trace should remain in completed storage")
	} else if got.ResponseBody != "" {
		t.Fatalf("evicted trace was mutated by a late completion: ResponseBody = %q", got.ResponseBody)
	}
}

func TestRecorder_InFlightCap_EvictsOldestAndFiresOnComplete(t *testing.T) {
	r := NewRecorder(10)
	r.maxInFlight = 1

	var evictions []TraceID
	r.OnComplete = func(trace *HTTPTrace) {
		if strings.Contains(trace.Error, "evicted") {
			evictions = append(evictions, trace.ID)
		}
	}

	first := r.startTrace("s1", "p1", "GET", "/1", nil, 0, "", 0)
	time.Sleep(2 * time.Millisecond)
	second := r.startTrace("s1", "p1", "GET", "/2", nil, 0, "", 0)
	time.Sleep(2 * time.Millisecond)
	_ = r.startTrace("s1", "p1", "GET", "/3", nil, 0, "", 0)

	if len(evictions) != 2 {
		t.Fatalf("got %d eviction callbacks, want 2", len(evictions))
	}
	if evictions[0] != first {
		t.Fatalf("first eviction = %s, want %s (oldest)", evictions[0], first)
	}
	if evictions[1] != second {
		t.Fatalf("second eviction = %s, want %s", evictions[1], second)
	}

	// Evictions must not clobber the last failing trace.
	if ft := r.LastFailingTrace(); ft != nil {
		t.Fatalf("evictions must not set lastFailingTrace, got %s", ft.ID)
	}
}

func TestRecorder_CapacityOverflow(t *testing.T) {
	r := NewRecorder(3)

	for i := 0; i < 5; i++ {
		r.Record("s1", "p1", "GET", "/v1/chat", []byte("req"), []byte("resp"), 200, time.Second, "", nil)
	}

	traces := r.List(0, "", "")
	if len(traces) != 3 {
		t.Fatalf("got %d traces, want 3", len(traces))
	}
	// The 3 remaining should be the last 3 written (indices 2,3,4)
	for _, tr := range traces {
		if tr.RequestBody != "req" {
			t.Fatalf("unexpected request body: %q", tr.RequestBody)
		}
	}
}

func TestRecorder_BodyTruncation(t *testing.T) {
	r := NewRecorder(5)

	// Create a body larger than MaxBodyBytes
	largeBody := make([]byte, MaxBodyBytes+10000)
	for i := range largeBody {
		largeBody[i] = 'A'
	}

	r.Record("s1", "p1", "POST", "/v1/chat", largeBody, largeBody, 200, time.Second, "", nil)

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}

	tr := traces[0]

	expectedSuffix := fmt.Sprintf("... [truncated %d bytes]", 10000)
	expectedLen := MaxBodyBytes + len(expectedSuffix)

	if len(tr.RequestBody) != expectedLen {
		t.Fatalf("request body length = %d, want %d (MaxBodyBytes + suffix)", len(tr.RequestBody), expectedLen)
	}
	if !strings.HasSuffix(tr.RequestBody, expectedSuffix) {
		t.Fatalf("request body missing truncation suffix")
	}

	if len(tr.ResponseBody) != expectedLen {
		t.Fatalf("response body length = %d, want %d (MaxBodyBytes + suffix)", len(tr.ResponseBody), expectedLen)
	}
	if !strings.HasSuffix(tr.ResponseBody, expectedSuffix) {
		t.Fatalf("response body missing truncation suffix")
	}

	// First bytes should be the original content
	if tr.RequestBody[:10] != "AAAAAAAAAA" {
		t.Fatalf("request body prefix should be original data")
	}

	if tr.RequestBytes != len(largeBody) {
		t.Fatalf("RequestBytes = %d, want %d", tr.RequestBytes, len(largeBody))
	}
}

func TestRecorder_SessionScoping(t *testing.T) {
	r := NewRecorder(10)

	r.Record("session-a", "p1", "GET", "/path", nil, nil, 200, 0, "", nil)
	r.Record("session-b", "p1", "GET", "/path", nil, nil, 200, 0, "", nil)
	r.Record("session-a", "p2", "GET", "/path", nil, nil, 200, 0, "", nil)

	// Filter by session-a
	traces := r.List(0, "session-a", "")
	if len(traces) != 2 {
		t.Fatalf("session-a: got %d traces, want 2", len(traces))
	}
	for _, tr := range traces {
		if tr.SessionID != "session-a" {
			t.Fatalf("unexpected session_id %q", tr.SessionID)
		}
	}

	// Filter by provider p1
	traces = r.List(0, "", "p1")
	if len(traces) != 2 {
		t.Fatalf("provider p1: got %d traces, want 2", len(traces))
	}
	for _, tr := range traces {
		if tr.ProviderID != "p1" {
			t.Fatalf("unexpected provider_id %q", tr.ProviderID)
		}
	}

	// Filter by both
	traces = r.List(0, "session-b", "p1")
	if len(traces) != 1 {
		t.Fatalf("session-b + p1: got %d traces, want 1", len(traces))
	}
}

func TestRecorder_ConcurrentWrites(t *testing.T) {
	r := NewRecorder(100)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Record("s1", "p1", "GET", "/path", []byte("req"), []byte("resp"), 200, time.Millisecond, "", nil)
		}(i)
	}
	wg.Wait()

	traces := r.List(0, "", "")
	if len(traces) != 50 {
		t.Fatalf("got %d traces, want 50", len(traces))
	}
}

func TestRecorder_ActiveToCompletedLifecycle(t *testing.T) {
	r := NewRecorder(10)

	// Create recording round tripper
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"ok"}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "session-1", "provider-x")

	client := &http.Client{
		Transport: rt,
		Timeout:   5 * time.Second,
	}

	// Check no in-flight before request
	if inflight := r.InFlight(); len(inflight) != 0 {
		t.Fatalf("expected 0 in-flight, got %d", len(inflight))
	}

	// Make a request
	resp, err := client.Get(srv.URL + "/v1/chat")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// After request completes, check completed traces
	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d completed traces, want 1", len(traces))
	}

	tr := traces[0]
	if tr.SessionID != "session-1" {
		t.Fatalf("SessionID = %q, want %q", tr.SessionID, "session-1")
	}
	if tr.ProviderID != "provider-x" {
		t.Fatalf("ProviderID = %q, want %q", tr.ProviderID, "provider-x")
	}
	if tr.Method != "GET" {
		t.Fatalf("Method = %q, want %q", tr.Method, "GET")
	}
	if tr.Status != 200 {
		t.Fatalf("Status = %d, want 200", tr.Status)
	}
	if tr.DurationMs < 0 {
		t.Fatalf("DurationMs = %d, want >= 0", tr.DurationMs)
	}
}

func TestRecorder_GetUnknownID(t *testing.T) {
	r := NewRecorder(5)
	tr := r.Get("nonexistent")
	if tr != nil {
		t.Fatalf("expected nil, got %+v", tr)
	}
}

func TestRecorder_InFlightTracking(t *testing.T) {
	r := NewRecorder(5)

	// Use the RoundTripper to create an in-flight trace
	// Create a recorder that uses a slow handler to ensure in-flight state
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")

	// Start request in background
	errCh := make(chan error, 1)
	go func() {
		client := &http.Client{Transport: rt, Timeout: 5 * time.Second}
		resp, err := client.Get(srv.URL + "/test")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		errCh <- err
	}()

	// Give time for request to start
	time.Sleep(10 * time.Millisecond)

	// Check in-flight traces
	inflight := r.InFlight()
	if len(inflight) == 0 {
		t.Fatal("expected at least 1 in-flight trace")
	}
	if inflight[0].Status != 0 {
		t.Fatalf("in-flight Status = %d, want 0", inflight[0].Status)
	}
	if inflight[0].DurationMs <= 0 {
		t.Fatalf("in-flight DurationMs = %d, want > 0", inflight[0].DurationMs)
	}

	// Wait for request to complete
	<-errCh

	// Now it should be in completed, not in-flight
	if inflight := r.InFlight(); len(inflight) != 0 {
		t.Fatalf("expected 0 in-flight after completion, got %d", len(inflight))
	}
}

func TestRecorder_RequestError(t *testing.T) {
	r := NewRecorder(5)

	// Point to a non-existent server
	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: time.Second}

	_, err := client.Get("http://127.0.0.1:1/nonexistent")
	if err == nil {
		t.Fatal("expected error connecting to non-existent server")
	}

	// Trace should still be recorded with error
	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}

	tr := traces[0]
	if tr.Error == "" {
		t.Fatal("expected non-empty error string")
	}
}

func TestRecorder_Count(t *testing.T) {
	r := NewRecorder(10)
	if c := r.Count(); c != 0 {
		t.Fatalf("Count = %d, want 0", c)
	}

	r.Record("s1", "p1", "GET", "/", nil, nil, 200, 0, "", nil)
	if c := r.Count(); c != 1 {
		t.Fatalf("Count = %d, want 1", c)
	}
}

func TestRecorder_ListLimit(t *testing.T) {
	r := NewRecorder(20)
	for i := 0; i < 20; i++ {
		r.Record("s1", "p1", "GET", "/", nil, nil, 200, 0, "", nil)
	}

	// List with limit
	traces := r.List(5, "", "")
	if len(traces) != 5 {
		t.Fatalf("got %d traces, want 5", len(traces))
	}
}

func TestRecorder_EmptyList(t *testing.T) {
	r := NewRecorder(10)
	traces := r.List(0, "", "")
	if len(traces) != 0 {
		t.Fatalf("got %d traces, want 0", len(traces))
	}
}

func TestRecorder_LastN(t *testing.T) {
	r := NewRecorder(10)

	// Record 5 traces: 3 for session-a, 2 for session-b
	r.Record("session-a", "p1", "GET", "/1", nil, nil, 200, 0, "", nil)
	r.Record("session-a", "p1", "GET", "/2", nil, nil, 200, 0, "", nil)
	r.Record("session-b", "p1", "GET", "/3", nil, nil, 200, 0, "", nil)
	r.Record("session-a", "p1", "GET", "/4", nil, nil, 200, 0, "", nil)
	r.Record("session-b", "p1", "GET", "/5", nil, nil, 200, 0, "", nil)

	// LastN for session-a should return the 2 most recent (chronological)
	traces := r.LastN("session-a", 2)
	if len(traces) != 2 {
		t.Fatalf("session-a LastN(2): got %d traces, want 2", len(traces))
	}
	// Should be traces 2 and 4 (0-indexed: 1 and 3), in chronological order by URL
	if traces[0].URL != "/2" {
		t.Errorf("session-a[0].URL = %q, want /2", traces[0].URL)
	}
	if traces[1].URL != "/4" {
		t.Errorf("session-a[1].URL = %q, want /4", traces[1].URL)
	}

	// LastN for session-b should return the 2 most recent
	traces = r.LastN("session-b", 2)
	if len(traces) != 2 {
		t.Fatalf("session-b LastN(2): got %d traces, want 2", len(traces))
	}
	if traces[0].URL != "/3" {
		t.Errorf("session-b[0].URL = %q, want /3", traces[0].URL)
	}
	if traces[1].URL != "/5" {
		t.Errorf("session-b[1].URL = %q, want /5", traces[1].URL)
	}

	// LastN for unknown session returns empty
	traces = r.LastN("nonexistent", 3)
	if len(traces) != 0 {
		t.Errorf("unknown session: got %d traces, want 0", len(traces))
	}

	// LastN with n larger than available returns all
	traces = r.LastN("session-a", 100)
	if len(traces) != 3 {
		t.Errorf("session-a LastN(100): got %d traces, want 3", len(traces))
	}
}

func TestRecorder_RoundTripperBodyPreservation(t *testing.T) {
	r := NewRecorder(5)

	// Server that verifies request body
	var gotBody []byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"response":"ok"}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	body := bytes.NewReader([]byte(`{"model":"test"}`))
	resp, err := client.Post(srv.URL+"/v1/chat", "application/json", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Verify downstream got the body
	if string(gotBody) != `{"model":"test"}` {
		t.Fatalf("downstream got body %q, want %q", string(gotBody), `{"model":"test"}`)
	}

	// Verify recorded trace has the request body
	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	if traces[0].RequestBody != `{"model":"test"}` {
		t.Fatalf("recorded request body = %q, want %q", traces[0].RequestBody, `{"model":"test"}`)
	}
}

func TestRecorder_LastFailingTrace_Non2xx(t *testing.T) {
	r := NewRecorder(3)

	// Record a 200 (success) — should NOT set last failing trace
	r.Record("s1", "p1", "GET", "/ok", nil, nil, 200, time.Second, "", nil)
	if ft := r.LastFailingTrace(); ft != nil {
		t.Fatalf("expected nil lastFailingTrace after 200, got trace id=%s", ft.ID)
	}

	// Record a 400 (failing) — should set last failing trace
	r.Record("s1", "p1", "POST", "/bad-request", nil, []byte(`{"error":"bad"}`), 400, time.Second, "", nil)

	ft := r.LastFailingTrace()
	if ft == nil {
		t.Fatal("expected non-nil lastFailingTrace after 400")
	}
	if ft.Status != 400 {
		t.Fatalf("lastFailingTrace.Status = %d, want 400", ft.Status)
	}
	if ft.URL != "/bad-request" {
		t.Fatalf("lastFailingTrace.URL = %q, want /bad-request", ft.URL)
	}
	if ft.ResponseBody != `{"error":"bad"}` {
		t.Fatalf("lastFailingTrace.ResponseBody = %q, want `{\"error\":\"bad\"}`", ft.ResponseBody)
	}

	// Record a 500 (another failure) — should overwrite to most recent
	r.Record("s1", "p1", "POST", "/server-error", nil, nil, 500, time.Second, "internal error", nil)
	ft = r.LastFailingTrace()
	if ft == nil {
		t.Fatal("expected non-nil lastFailingTrace after 500")
	}
	if ft.Status != 500 {
		t.Fatalf("lastFailingTrace.Status = %d, want 500", ft.Status)
	}
	if ft.Error != "internal error" {
		t.Fatalf("lastFailingTrace.Error = %q, want 'internal error'", ft.Error)
	}
}

func TestRecorder_LastFailingTrace_CapacityPressure(t *testing.T) {
	r := NewRecorder(20)

	// Write 30 traces: 29 successes (200) and 1 failure (400) at position 15
	for i := 0; i < 30; i++ {
		if i == 15 {
			r.Record("s1", "p1", "POST", "/fail", nil, []byte(`{"error":"bad"}`), 400, time.Second, "", nil)
		} else {
			r.Record("s1", "p1", "GET", "/ok", nil, nil, 200, 0, "", nil)
		}
	}

	// Ring buffer should only have 20 traces
	traces := r.List(0, "", "")
	if len(traces) != 20 {
		t.Fatalf("ring buffer has %d traces, want 20", len(traces))
	}

	// But the failing trace should still be accessible via LastFailingTrace
	ft := r.LastFailingTrace()
	if ft == nil {
		t.Fatal("expected non-nil lastFailingTrace after capacity pressure")
	}
	if ft.Status != 400 {
		t.Fatalf("lastFailingTrace.Status = %d, want 400", ft.Status)
	}
	if ft.ResponseBody != `{"error":"bad"}` {
		t.Fatalf("lastFailingTrace.ResponseBody = %q, want `{\"error\":\"bad\"}`", ft.ResponseBody)
	}
}

func TestRecorder_LastFailingTrace_ErroredRequest(t *testing.T) {
	r := NewRecorder(5)

	// Record a trace with an error message (simulating RoundTrip error)
	r.Record("s1", "p1", "POST", "/fail", []byte("req"), nil, 0, time.Second, "connection refused", nil)

	ft := r.LastFailingTrace()
	if ft == nil {
		t.Fatal("expected non-nil lastFailingTrace for errored request")
	}
	if ft.Status != 0 {
		t.Fatalf("lastFailingTrace.Status = %d, want 0", ft.Status)
	}
	if ft.Error != "connection refused" {
		t.Fatalf("lastFailingTrace.Error = %q, want 'connection refused'", ft.Error)
	}
}

func TestRecorder_ResponseHeaders(t *testing.T) {
	r := NewRecorder(5)

	// Create a test server that returns specific headers
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "req-abc-123")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	resp, err := client.Get(srv.URL + "/v1/chat")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Check the failing trace has response headers
	ft := r.LastFailingTrace()
	if ft == nil {
		t.Fatal("expected non-nil lastFailingTrace for 429 response")
	}
	if ft.Status != http.StatusTooManyRequests {
		t.Fatalf("Status = %d, want 429", ft.Status)
	}
	if ft.ResponseHeaders == nil {
		t.Fatal("expected non-nil ResponseHeaders")
	}
	if ft.ResponseHeaders["X-Request-Id"][0] != "req-abc-123" {
		t.Fatalf("X-Request-Id = %q, want 'req-abc-123'", ft.ResponseHeaders["X-Request-Id"])
	}
	if ft.ResponseHeaders["Retry-After"][0] != "30" {
		t.Fatalf("Retry-After = %q, want '30'", ft.ResponseHeaders["Retry-After"])
	}

	// Also check via regular List
	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]
	if tr.ResponseHeaders["X-Request-Id"][0] != "req-abc-123" {
		t.Fatalf("List: X-Request-Id = %q, want 'req-abc-123'", tr.ResponseHeaders["X-Request-Id"])
	}
}

func TestRecorder_StreamingTruncationPreservesEnrichedFields(t *testing.T) {
	r := NewRecorder(5)

	// A streaming response whose tail — the chunk carrying usage and
	// finish_reason — sits far beyond the MaxBodyBytes cap. The first chunk
	// alone pushes the body over the cap, so the raw-body capture keeps only
	// the head; the enriched fields must survive via the TraceMeta bridge.
	pad := strings.Repeat("x", MaxBodyBytes)
	tail := "data: {\"id\":\"c1\",\"model\":\"gpt-stream\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":101,\"completion_tokens\":42,\"total_tokens\":143}}\n\ndata: [DONE]\n"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Delay before the first byte so time-to-first-byte is measurable
		// (sub-millisecond responses would otherwise report 0ms).
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write([]byte(pad))
		_, _ = w.Write([]byte(tail))
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")

	meta := &TraceMeta{}
	meta.SetAttempt(2)
	meta.SetModel("gpt-stream")
	req, err := http.NewRequestWithContext(WithTraceMeta(context.Background(), meta), http.MethodPost, srv.URL+"/v1/chat/completions", bytes.NewReader([]byte(`{"model":"gpt-stream"}`)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	resp, err := (&http.Client{Transport: rt, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	// Simulate the run loop: it parses the SSE stream and records usage and
	// finish reason on the meta before the body/stream is closed.
	meta.SetUsage(&UsageTotals{PromptTokens: 101, CompletionTokens: 42, TotalTokens: 143})
	meta.SetFinishReason("stop")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]

	// The raw body capture is capped at MaxBodyBytes and the tail (with the
	// usage chunk) is gone — exactly the truncation bug this ticket fixes.
	if len(tr.ResponseBody) != MaxBodyBytes {
		t.Fatalf("captured body length = %d, want %d (capped, tail dropped)", len(tr.ResponseBody), MaxBodyBytes)
	}
	if strings.Contains(tr.ResponseBody, "finish_reason") || strings.Contains(tr.ResponseBody, "usage") {
		t.Fatal("captured body unexpectedly contains the stream tail")
	}
	if tr.ResponseBytes != MaxBodyBytes {
		t.Fatalf("ResponseBytes = %d, want %d (capped capture)", tr.ResponseBytes, MaxBodyBytes)
	}

	// Yet the enriched fields survive because they came from the parsed stream.
	if tr.Usage == nil || tr.Usage.PromptTokens != 101 || tr.Usage.CompletionTokens != 42 || tr.Usage.TotalTokens != 143 {
		t.Fatalf("trace Usage = %+v, want prompt=101 completion=42 total=143", tr.Usage)
	}
	if tr.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", tr.FinishReason, "stop")
	}
	if tr.Model != "gpt-stream" {
		t.Fatalf("Model = %q, want %q", tr.Model, "gpt-stream")
	}
	if tr.Attempt != 2 {
		t.Fatalf("Attempt = %d, want 2", tr.Attempt)
	}
	if tr.TTFBMs <= 0 {
		t.Fatalf("TTFBMs = %d, want > 0 (measured on first body read)", tr.TTFBMs)
	}
}

func TestRecord_EnrichesFromOpenAIBody(t *testing.T) {
	r := NewRecorder(5)

	body := `{"id":"chatcmpl-1","model":"gpt-4o","choices":[{"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":3}}}`
	r.Record("s1", "p1", "POST", "/chat/completions", nil, []byte(body), 200, time.Second, "", nil)

	tr := r.List(0, "", "")[0]
	if tr.Usage == nil {
		t.Fatal("expected parsed usage from OpenAI body")
	}
	if tr.Usage.PromptTokens != 10 || tr.Usage.CompletionTokens != 5 || tr.Usage.TotalTokens != 15 {
		t.Fatalf("Usage = %+v, want prompt=10 completion=5 total=15", tr.Usage)
	}
	if tr.Usage.CacheReadTokens != 3 {
		t.Fatalf("CacheReadTokens = %d, want 3", tr.Usage.CacheReadTokens)
	}
	if tr.FinishReason != "stop" {
		t.Fatalf("FinishReason = %q, want %q", tr.FinishReason, "stop")
	}
	if tr.Model != "gpt-4o" {
		t.Fatalf("Model = %q, want %q", tr.Model, "gpt-4o")
	}
}

func TestRecord_EnrichesFromAnthropicBody(t *testing.T) {
	r := NewRecorder(5)

	body := `{"id":"msg_1","model":"claude-3-7","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":3,"cache_creation_input_tokens":2}}`
	r.Record("s1", "p1", "POST", "/v1/messages", nil, []byte(body), 200, time.Second, "", nil)

	tr := r.List(0, "", "")[0]
	if tr.Usage == nil {
		t.Fatal("expected parsed usage from Anthropic body")
	}
	if tr.Usage.PromptTokens != 10 || tr.Usage.CompletionTokens != 5 {
		t.Fatalf("Usage = %+v, want prompt=10 completion=5", tr.Usage)
	}
	if tr.Usage.CacheReadTokens != 3 || tr.Usage.CacheWriteTokens != 2 {
		t.Fatalf("cache usage = read:%d write:%d, want read:3 write:2", tr.Usage.CacheReadTokens, tr.Usage.CacheWriteTokens)
	}
	if tr.Usage.TotalTokens != 15 {
		t.Fatalf("TotalTokens = %d, want 15 (computed from prompt+completion)", tr.Usage.TotalTokens)
	}
	if tr.FinishReason != "end_turn" {
		t.Fatalf("FinishReason = %q, want %q", tr.FinishReason, "end_turn")
	}
	if tr.Model != "claude-3-7" {
		t.Fatalf("Model = %q, want %q", tr.Model, "claude-3-7")
	}
}

func TestRecord_IgnoresTruncatedStreamBody(t *testing.T) {
	r := NewRecorder(5)

	// An SSE stream body (not a single JSON document) must not crash or
	// produce bogus usage — parsing fails safely and leaves fields empty.
	body := `data: {"id":"c1","choices":[{"delta":{"content":"hi"},"finish_reason":null}],"usage":null}` + "\n\ndata: [DONE]\n"
	r.Record("s1", "p1", "POST", "/chat/completions", nil, []byte(body), 200, time.Second, "", nil)

	tr := r.List(0, "", "")[0]
	if tr.Usage != nil {
		t.Fatalf("expected nil usage for SSE body, got %+v", tr.Usage)
	}
	if tr.FinishReason != "" {
		t.Fatalf("expected empty finish reason for SSE body, got %q", tr.FinishReason)
	}
}

func TestTraceMeta_ContextRoundTrip(t *testing.T) {
	meta := &TraceMeta{}
	ctx := WithTraceMeta(context.Background(), meta)
	if got := TraceMetaFromContext(ctx); got != meta {
		t.Fatal("TraceMetaFromContext did not return the attached meta")
	}
	if got := TraceMetaFromContext(context.Background()); got != nil {
		t.Fatal("TraceMetaFromContext should return nil without a meta")
	}
	if got := TraceMetaFromContext(nil); got != nil {
		t.Fatal("TraceMetaFromContext should return nil for a nil context")
	}

	meta.SetAttempt(3)
	meta.SetTTFBMs(17)
	meta.SetModel("m-1")
	meta.SetFinishReason("length")
	meta.SetUsage(&UsageTotals{PromptTokens: 7, CompletionTokens: 8})

	if meta.Attempt() != 3 || meta.TTFBMs() != 17 || meta.Model() != "m-1" || meta.FinishReason() != "length" {
		t.Fatalf("meta getters did not round-trip setters: %+v", meta)
	}
	if u := meta.Usage(); u == nil || u.PromptTokens != 7 || u.CompletionTokens != 8 {
		t.Fatalf("meta Usage did not round-trip: %+v", u)
	}
}

func TestTraceMeta_RunTurnAndTTFTRoundTrip(t *testing.T) {
	meta := &TraceMeta{}
	meta.SetRunID("run-42")
	meta.SetTurn(3)
	meta.SetAttempt(1)

	start := time.Now().Add(-200 * time.Millisecond)
	meta.SetRequestStart(start)
	meta.SetFirstTokenTime(start.Add(50 * time.Millisecond))

	if meta.RunID() != "run-42" {
		t.Errorf("RunID = %q, want run-42", meta.RunID())
	}
	if meta.Turn() != 3 {
		t.Errorf("Turn = %d, want 3", meta.Turn())
	}
	// 50ms from request start to first token.
	if ttft := meta.TTFTMs(); ttft < 40 || ttft > 60 {
		t.Errorf("TTFTMs = %d, want ~50", ttft)
	}

	// ResetFirstToken clears the anchors so a retry starts a fresh
	// measurement (issue #988).
	meta.ResetFirstToken()
	if meta.TTFTMs() != 0 {
		t.Errorf("TTFTMs after reset = %d, want 0", meta.TTFTMs())
	}
	newStart := time.Now().Add(-100 * time.Millisecond)
	meta.SetRequestStart(newStart)
	meta.SetFirstTokenTime(newStart.Add(25 * time.Millisecond))
	if ttft := meta.TTFTMs(); ttft < 15 || ttft > 35 {
		t.Errorf("TTFTMs after retry = %d, want ~25", ttft)
	}
}

func TestRecorder_RoundTripperStampsRunTurnAndTTFT(t *testing.T) {
	r := NewRecorder(5)
	meta := &TraceMeta{}
	meta.SetRunID("run-1")
	meta.SetTurn(2)
	meta.SetAttempt(0)

	// Serve a small body so the round-tripper's traceBody measures TTFB and
	// the meta's first-token anchor produces a TTFT at finalize time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices":[{"delta":{"content":"hi"}}]}`))
	}))
	defer srv.Close()

	rt := NewRecordingRoundTripper(nil, r, "s1", "p1")
	client := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	ctx := WithTraceMeta(context.Background(), meta)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/v1/chat", bytes.NewReader([]byte(`{"model":"test"}`)))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	// First token arrives as soon as the body is read.
	io.Copy(io.Discard, resp.Body)
	meta.SetFirstTokenTime(meta.RequestStart().Add(7 * time.Millisecond))
	resp.Body.Close()

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]
	if tr.RunID != "run-1" {
		t.Errorf("trace RunID = %q, want run-1", tr.RunID)
	}
	if tr.Turn != 2 {
		t.Errorf("trace Turn = %d, want 2", tr.Turn)
	}
	if tr.TTFTMs != 7 {
		t.Errorf("trace TTFTMs = %d, want 7", tr.TTFTMs)
	}
}

func TestRecordWithMeta_CarriesRunTurnCorrelation(t *testing.T) {
	r := NewRecorder(5)

	r.RecordWithMeta("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"m"}`), []byte(`{"model":"m"}`), 200, time.Second, "", nil, RecordMeta{
		Attempt: 3,
		RunID:   "run-9",
		Turn:    4,
	})

	traces := r.List(0, "", "")
	if len(traces) != 1 {
		t.Fatalf("got %d traces, want 1", len(traces))
	}
	tr := traces[0]
	if tr.RunID != "run-9" {
		t.Errorf("trace RunID = %q, want run-9", tr.RunID)
	}
	if tr.Turn != 4 {
		t.Errorf("trace Turn = %d, want 4", tr.Turn)
	}
	if tr.Attempt != 3 {
		t.Errorf("trace Attempt = %d, want 3", tr.Attempt)
	}
}
