package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
