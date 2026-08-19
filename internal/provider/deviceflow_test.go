package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// TestDeviceFlowStartRequestsCode verifies Start hits the GitHub /login/device/code
// endpoint and surfaces the verification URL + device code for the TUI approval
// screen.
func TestDeviceFlowStartRequestsCode(t *testing.T) {
	t.Parallel()
	var path, contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		contentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"dev-1","user_code":"AB12-CD","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`))
	}))
	defer srv.Close()

	d := NewDeviceFlow(srv.Client(), codeEndpoints(srv))
	cd, err := d.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if path != "/login/device/code" {
		t.Fatalf("Start() path = %q, want /login/device/code", path)
	}
	if !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		t.Fatalf("Start() content-type = %q, want form urlencoded", contentType)
	}
	if cd.DeviceCode != "dev-1" || cd.UserCode != "AB12-CD" || cd.VerificationURI != "https://github.com/login/device" {
		t.Fatalf("Start() code = %+v, want device-code dev-1 / user-code AB12-CD", cd)
	}
}

// TestDeviceFlowPollExchangesTokens verifies Poll exchanges the device code for
// a credential on approval, giving the TUI a fresh token set to persist to
// config. Expiry is derived from the endpoint's expires_in.
func TestDeviceFlowPollExchangesTokens(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
			t.Errorf("grant_type = %q, want device-code grant", r.Form.Get("grant_type"))
		}
		if r.Form.Get("device_code") != "dev-9" {
			t.Errorf("device_code = %q, want dev-9", r.Form.Get("device_code"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc-new","refresh_token":"ref-new","expires_in":28800}`))
	}))
	defer srv.Close()

	d := NewDeviceFlow(srv.Client(), codeEndpoints(srv))
	cfg, err := d.Poll(context.Background(), "dev-9")
	if err != nil {
		t.Fatalf("Poll() error = %v, want nil", err)
	}
	if cfg.AccessToken != "acc-new" || cfg.RefreshToken != "ref-new" {
		t.Fatalf("Poll() = %+v, want fresh access+refresh tokens", cfg)
	}
	if cfg.ExpiresAt < 1 {
		t.Fatalf("Poll() ExpiresAt = %d, want a future expiry", cfg.ExpiresAt)
	}
}

// TestDeviceFlowPollTokenExchangeError verifies a device-login exchange that
// responds with an error (no access/refresh token) surfaces the flowError
// mismatch-value error rather than silently succeeding.
func TestDeviceFlowPollTokenExchangeError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"incorrect_device_code"}`))
	}))
	defer srv.Close()

	d := NewDeviceFlow(srv.Client(), codeEndpoints(srv))
	_, err := d.Poll(context.Background(), "dev-1")
	if err == nil {
		t.Fatal("Poll() error = nil, want flowError on token exchange failure")
	}
	var fe *flowError
	if !errors.As(err, &fe) {
		t.Fatalf("Poll() error = %v, want *flowError", err)
	}
	if !strings.Contains(fe.Error(), "incorrect_device_code") {
		t.Fatalf("flowError = %q, want it to surface the mismatch value", fe.Error())
	}
}

// TestDeviceFlowPollAuthorizationPending verifies a not-yet-approved poll is a
// clean retryable signal, and the TUI keeps waiting.
func TestDeviceFlowPollAuthorizationPending(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer srv.Close()

	d := NewDeviceFlow(srv.Client(), codeEndpoints(srv))
	_, err := d.Poll(context.Background(), "dev-1")
	if !IsAuthorizationPending(err) {
		t.Fatalf("Poll() error = %v, want authorization_pending signal", err)
	}
}

// TestDeviceFlowToConfig verifies a completed device flow yields a credential
// ready to persist into config (acceptance criterion (c) wiring: TUI device-flow
// re-auth produces the fresh token set stored for later batch runs).
func TestDeviceFlowToConfig(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login/device/code" {
			_, _ = w.Write([]byte(`{"device_code":"d","user_code":"U","verification_uri":"u","expires_in":900}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
	}))
	defer srv.Close()

	d := NewDeviceFlow(srv.Client(), map[string]string{
		"code":  srv.URL + "/login/device/code",
		"token": srv.URL + "/login/oauth/access_token",
	})
	cd, err := d.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	cfg, err := d.Poll(context.Background(), cd.DeviceCode)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	got := config.CopilotConfig{
		AccessToken:  cfg.AccessToken,
		RefreshToken: cfg.RefreshToken,
		ExpiresAt:    cfg.ExpiresAt,
	}
	if got.AccessToken != "a" || got.RefreshToken != "r" {
		t.Fatalf("copilot config = %+v, want a/r", got)
	}
}

// TestDeviceFlowConcurrentSafe guards the shared poll against data races.
func TestDeviceFlowConcurrentSafe(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a","expires_in":60}`))
	}))
	defer srv.Close()
	d := NewDeviceFlow(srv.Client(), codeEndpoints(srv))
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, _ = d.Poll(context.Background(), "d")
		})
	}
	wg.Wait()
}

// codeEndpoints points the device-flow client's code/token endpoints at the
// httptest server with the real GitHub path suffixes.
func codeEndpoints(srv *httptest.Server) map[string]string {
	return map[string]string{
		"code":  srv.URL + "/login/device/code",
		"token": srv.URL + "/login/oauth/access_token",
	}
}
