package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/provider"
)

func TestCopilotConnectPersistsFreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login/device/code" {
			_, _ = w.Write([]byte(`{"device_code":"dev-x","user_code":"ZZ-AA","verification_uri":"https://github.com/login/device","expires_in":900}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tui-fresh-access","refresh_token":"tui-fresh-refresh","expires_in":28800}`))
	}))
	defer srv.Close()

	orig := newDeviceFlow
	newDeviceFlow = func(h *http.Client, _ map[string]string) *provider.DeviceFlow {
		return provider.NewDeviceFlow(h, map[string]string{
			"code":  srv.URL + "/login/device/code",
			"token": srv.URL + "/login/oauth/access_token",
		})
	}
	defer func() { newDeviceFlow = orig }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	var sawCode string

	cfg, err := CopilotConnect(context.Background(), cfgPath, srv.Client(), func(cd provider.DeviceCode) { sawCode = cd.UserCode })
	if err != nil {
		t.Fatalf("CopilotConnect() error = %v, want nil", err)
	}
	if sawCode != "ZZ-AA" {
		t.Fatalf("onCode() saw user code %q, want ZZ-AA", sawCode)
	}
	if cfg.Provider != provider.ProviderCopilot {
		t.Fatalf("copilot config Provider = %q, want github-copilot", cfg.Provider)
	}
	if cfg.Copilot.AccessToken != "tui-fresh-access" || cfg.Copilot.RefreshToken != "tui-fresh-refresh" {
		t.Fatalf("copilot config = %+v, want the fresh device-flow tokens", cfg.Copilot)
	}

	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if !strings.Contains(string(raw), "tui-fresh-access") {
		t.Fatalf("persisted config %q missing the fresh access token", raw)
	}
}

func TestCopilotConnectRetriesAuthorizationPending(t *testing.T) {
	var polls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login/device/code" {
			_, _ = w.Write([]byte(`{"device_code":"dev-x","user_code":"ZZ-AA","verification_uri":"https://github.com/login/device","expires_in":900,"interval":0}`))
			return
		}
		polls++
		if polls == 1 {
			_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"tui-fresh-access","refresh_token":"tui-fresh-refresh","expires_in":28800}`))
	}))
	defer srv.Close()

	orig := newDeviceFlow
	newDeviceFlow = func(h *http.Client, _ map[string]string) *provider.DeviceFlow {
		return provider.NewDeviceFlow(h, map[string]string{
			"code":  srv.URL + "/login/device/code",
			"token": srv.URL + "/login/oauth/access_token",
		})
	}
	defer func() { newDeviceFlow = orig }()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := CopilotConnect(context.Background(), cfgPath, srv.Client(), nil)
	if err != nil {
		t.Fatalf("CopilotConnect() error = %v, want nil after pending->success", err)
	}
	if polls != 2 {
		t.Fatalf("poll count = %d, want 2", polls)
	}
	if cfg.Copilot.AccessToken != "tui-fresh-access" {
		t.Fatalf("copilot access token = %q, want tui-fresh-access", cfg.Copilot.AccessToken)
	}
}
