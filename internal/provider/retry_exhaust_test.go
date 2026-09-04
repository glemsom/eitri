package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDoWithRetryExhausts5xxReturnsLastHTTPError locks in the incident behavior
// behind the "provider returned HTTP 500" report: an endpoint that keeps
// returning 5xx is attempted providerMaxRetries+1 times with the request body
// replayed every time, and the surfaced error is an HTTPError carrying the last
// response code and body.
func TestDoWithRetryExhausts5xxReturnsLastHTTPError(t *testing.T) {
	withFastRetry(t)
	body := `{"type":"error","error":{"type":"error","message":"Internal server error"}}`
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		got, err := io.ReadAll(r.Body)
		if err != nil || string(got) != "payload" {
			t.Errorf("attempt %d: body = %q, err = %v; want the full payload replayed", hits, got, err)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL, strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := doWithRetry(context.Background(), srv.Client(), req)
	if resp != nil {
		t.Fatalf("resp = %v, want nil after exhausted retries", resp)
	}
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("err = %v, want HTTPError", err)
	}
	if he.Code != http.StatusInternalServerError || he.Body != body {
		t.Fatalf("HTTPError = %d %q, want 500 %q", he.Code, he.Body, body)
	}
	if want := providerMaxRetries + 1; hits != want {
		t.Fatalf("server hits = %d, want %d (first attempt + %d retries)", hits, want, providerMaxRetries)
	}
}
