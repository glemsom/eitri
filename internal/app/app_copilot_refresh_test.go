package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// withCopilotTokenURL points the copilotRefresh seam at an httptest server and
// restores the production endpoint when the test finishes.
func withCopilotTokenURL(t *testing.T, tokenURL string) {
	t.Helper()
	orig := copilotTokenURL
	copilotTokenURL = tokenURL
	t.Cleanup(func() { copilotTokenURL = orig })
}

// TestCopilotRefreshRenewsTokens drives the happy path: the token endpoint
// returns a fresh token set and the renewal round-trips it into the config,
// deriving ExpiresAt from expires_in. It also pins the wire shape the endpoint
// must receive (POST form on the access-token path with the Copilot client id).
func TestCopilotRefreshRenewsTokens(t *testing.T) {
	var method, path, contentType, accept string
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		path = r.URL.Path
		contentType = r.Header.Get("Content-Type")
		accept = r.Header.Get("Accept")
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse refresh request form: %v", err)
		}
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc-renewed","refresh_token":"ref-renewed","expires_in":3600}`))
	}))
	defer srv.Close()
	withCopilotTokenURL(t, srv.URL+"/login/oauth/access_token")

	before := time.Now().Unix()
	refresh := copilotRefresh(srv.Client())
	cfg, err := refresh(context.Background(), "old-refresh-token")
	if err != nil {
		t.Fatalf("refresh() error = %v, want nil", err)
	}
	after := time.Now().Unix()

	if method != http.MethodPost {
		t.Fatalf("refresh request method = %q, want POST", method)
	}
	if path != "/login/oauth/access_token" {
		t.Fatalf("refresh request path = %q, want /login/oauth/access_token", path)
	}
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		t.Fatalf("refresh request content-type = %q, want form-urlencoded", contentType)
	}
	if accept != "application/json" {
		t.Fatalf("refresh request accept = %q, want application/json", accept)
	}
	if form.Get("grant_type") != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", form.Get("grant_type"))
	}
	if form.Get("refresh_token") != "old-refresh-token" {
		t.Fatalf("refresh_token = %q, want old-refresh-token", form.Get("refresh_token"))
	}
	if form.Get("client_id") != "Iv1.b507a08c87ecfe98" {
		t.Fatalf("client_id = %q, want the Copilot client id", form.Get("client_id"))
	}
	if cfg.AccessToken != "acc-renewed" || cfg.RefreshToken != "ref-renewed" {
		t.Fatalf("renewed config = %+v, want acc-renewed/ref-renewed", cfg)
	}
	if cfg.ExpiresAt < before+3600 || cfg.ExpiresAt > after+3600 {
		t.Fatalf("ExpiresAt = %d, want now+3600 (between %d and %d)", cfg.ExpiresAt, before+3600, after+3600)
	}
}

// TestCopilotRefreshErrorResponses covers the endpoint-side error branches
// table-driven: a server error status with no tokens must surface a
// no-credential failure naming the HTTP status, and a non-JSON response must
// surface the decode error rather than fabricating a zero config. Each row
// expects exactly one of wantErrSubstring (a message match) or wantSyntaxErr
// (a JSON decode error).
func TestCopilotRefreshErrorResponses(t *testing.T) {
	tests := []struct {
		name             string
		status           int
		body             string
		wantErrSubstring string
		wantSyntaxErr    bool
	}{
		{"server error, no credential", http.StatusInternalServerError, `{}`, "no credential (HTTP 500)", false},
		{"malformed json body", http.StatusOK, `this is not json`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			withCopilotTokenURL(t, srv.URL+"/login/oauth/access_token")

			refresh := copilotRefresh(srv.Client())
			_, err := refresh(context.Background(), "old-refresh-token")
			if err == nil {
				t.Fatalf("%s: refresh() error = nil, want an error", tt.name)
			}
			if tt.wantErrSubstring != "" && !strings.Contains(err.Error(), tt.wantErrSubstring) {
				t.Fatalf("%s: refresh() error = %q, want it to contain %q", tt.name, err, tt.wantErrSubstring)
			}
			if tt.wantSyntaxErr {
				var syntaxErr *json.SyntaxError
				if !errors.As(err, &syntaxErr) {
					t.Fatalf("%s: refresh() error = %v, want a JSON decode error", tt.name, err)
				}
			}
		})
	}
}

// TestCopilotRefreshTransportErrorSurfaces covers the transport-failure branch:
// when the HTTP layer itself fails (refused connection, deadline), that error
// is the one surfaced to the caller unchanged. The seam does not need to be set
// up here because the transport fails before any request is issued.
func TestCopilotRefreshTransportErrorSurfaces(t *testing.T) {
	boom := errors.New("connection refused")
	client := &http.Client{Transport: failedTransport{err: boom}}

	refresh := copilotRefresh(client)
	_, err := refresh(context.Background(), "old-refresh-token")
	if err == nil {
		t.Fatal("refresh() error = nil, want the transport error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("refresh() error = %v, want the underlying transport error", err)
	}
}

// failedTransport is a RoundTripper that always fails, standing in for a
// refused or dead HTTP endpoint.
type failedTransport struct{ err error }

func (t failedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}
