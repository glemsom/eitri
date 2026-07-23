package debug

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorder_CapacityOverflow(t *testing.T) {
	r := NewRecorder(3)

	for i := 0; i < 5; i++ {
		r.Record("s1", "p1", "GET", "/v1/chat", []byte("req"), []byte("resp"), 200, time.Second, "")
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

	r.Record("s1", "p1", "POST", "/v1/chat", largeBody, largeBody, 200, time.Second, "")

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

	r.Record("session-a", "p1", "GET", "/path", nil, nil, 200, 0, "")
	r.Record("session-b", "p1", "GET", "/path", nil, nil, 200, 0, "")
	r.Record("session-a", "p2", "GET", "/path", nil, nil, 200, 0, "")

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
			r.Record("s1", "p1", "GET", "/path", []byte("req"), []byte("resp"), 200, time.Millisecond, "")
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

	r.Record("s1", "p1", "GET", "/", nil, nil, 200, 0, "")
	if c := r.Count(); c != 1 {
		t.Fatalf("Count = %d, want 1", c)
	}
}

func TestRecorder_ListLimit(t *testing.T) {
	r := NewRecorder(20)
	for i := 0; i < 20; i++ {
		r.Record("s1", "p1", "GET", "/", nil, nil, 200, 0, "")
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
	r.Record("session-a", "p1", "GET", "/1", nil, nil, 200, 0, "")
	r.Record("session-a", "p1", "GET", "/2", nil, nil, 200, 0, "")
	r.Record("session-b", "p1", "GET", "/3", nil, nil, 200, 0, "")
	r.Record("session-a", "p1", "GET", "/4", nil, nil, 200, 0, "")
	r.Record("session-b", "p1", "GET", "/5", nil, nil, 200, 0, "")

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
