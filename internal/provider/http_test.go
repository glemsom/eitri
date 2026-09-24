package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// withFastRetry shrinks the retry budget for the duration of a test so retry
// loops stay fast; it restores the defaults on cleanup.
func withFastRetry(t *testing.T) {
	t.Helper()
	oldDelay, oldRetries := providerRetryDelay, providerMaxRetries
	providerRetryDelay = time.Millisecond
	providerMaxRetries = 3
	t.Cleanup(func() { providerRetryDelay, providerMaxRetries = oldDelay, oldRetries })
}

type traceRecorder struct {
	requests  [][]byte
	responses [][]byte
}

func (r *traceRecorder) TraceRequest(body []byte) {
	r.requests = append(r.requests, append([]byte(nil), body...))
}

func (r *traceRecorder) TraceResponse(body []byte) {
	r.responses = append(r.responses, append([]byte(nil), body...))
}

func TestTraceTransportRecordsRequestAndResponseBodies(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if string(body) != `{"prompt":"sensitive"}` {
			t.Fatalf("request body = %q, want payload preserved", body)
		}
		_, _ = io.WriteString(w, "data: response\n\n")
	}))
	defer srv.Close()

	trace := &traceRecorder{}
	client := &http.Client{Transport: NewTraceTransport(nil, trace)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader(`{"prompt":"sensitive"}`))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response: %v", err)
	}
	if got := string(trace.requests[0]); got != `{"prompt":"sensitive"}` {
		t.Fatalf("traced request = %q, want raw body", got)
	}
	if got := string(trace.responses[0]); got != "data: response\n\n" {
		t.Fatalf("traced response = %q, want raw body", got)
	}
}

func TestDoWithRetrySucceedsAfterTransient5xx(t *testing.T) {
	withFastRetry(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("body"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := doWithRetry(context.Background(), srv.Client(), req)
	if err != nil {
		t.Fatalf("doWithRetry() error = %v, want nil", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 3 {
		t.Fatalf("server hits = %d, want 3 (two 5xx then success)", hits)
	}
}

func TestDoWithRetryDoesNotRetry4xx(t *testing.T) {
	withFastRetry(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "nope")
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("body"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := doWithRetry(context.Background(), srv.Client(), req)
	if err != nil {
		t.Fatalf("doWithRetry() error = %v, want nil (4xx returned for the caller to handle)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (4xx must not retry)", resp.StatusCode)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1 (4xx must not retry)", hits)
	}
}

func TestDoWithRetryStopsOnCanceledContext(t *testing.T) {
	withFastRetry(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before any attempt
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unused.invalid/", strings.NewReader("body"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	start := time.Now()
	resp, err := doWithRetry(ctx, http.DefaultClient, req)
	if err == nil {
		t.Fatalf("doWithRetry() error = nil, want context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if resp != nil {
		t.Fatalf("resp = %v, want nil", resp)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("doWithRetry blocked %s; must not sleep the retry delay when the context is cancelled", time.Since(start))
	}
}

type closeUnblocksReadBody struct {
	readStarted chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
}

func (b *closeUnblocksReadBody) Read(p []byte) (int, error) {
	close(b.readStarted)
	<-b.closed
	copy(p, "final response")
	return len("final response"), io.EOF
}

func (b *closeUnblocksReadBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

type responseRoundTripper struct{ body io.ReadCloser }

func (t responseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: t.body, Header: make(http.Header)}, nil
}

func TestTraceTransportCapturesFinalResponseWhenCloseUnblocksRead(t *testing.T) {
	body := &closeUnblocksReadBody{readStarted: make(chan struct{}), closed: make(chan struct{})}
	trace := &traceRecorder{}
	client := &http.Client{Transport: NewTraceTransport(responseRoundTripper{body: body}, trace)}

	resp, err := client.Get("http://example.test/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		readDone <- err
	}()
	select {
	case <-body.readStarted:
	case <-time.After(time.Second):
		t.Fatal("Read did not reach the blocking response body")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- resp.Body.Close() }()

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadAll deadlocked with Close")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked with ReadAll")
	}
	if len(trace.responses) != 1 {
		t.Fatalf("traced responses = %d, want 1", len(trace.responses))
	}
	if got := string(trace.responses[0]); got != "final response" {
		t.Fatalf("traced response = %q, want complete final response", got)
	}
}
