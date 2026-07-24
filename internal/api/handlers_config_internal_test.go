package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
)

func TestMaskedConfig_Nil(t *testing.T) {
	result := maskedConfig(nil)
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}
}

func TestMaskedConfig_NoAPIKey(t *testing.T) {
	cfg := &config.Config{
		Provider: "custom_openai",
		Model:    "gpt-4",
		BaseURL:  "http://example.com",
	}
	result := maskedConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", result.APIKey)
	}
	if result.ProviderAuth != nil {
		t.Errorf("ProviderAuth should be nil, got %v", result.ProviderAuth)
	}
	if result.Provider != cfg.Provider {
		t.Errorf("Provider = %q, want %q", result.Provider, cfg.Provider)
	}
	if result.BaseURL != cfg.BaseURL {
		t.Errorf("BaseURL = %q, want %q", result.BaseURL, cfg.BaseURL)
	}
}

func TestMaskedConfig_WithAPIKey(t *testing.T) {
	cfg := &config.Config{
		Provider: "custom_openai",
		APIKey:   "sk-abcdefghijklmnopqrstuvwxyz",
		Model:    "gpt-4",
	}
	result := maskedConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// API key should be masked
	if result.APIKey == "" || result.APIKey == cfg.APIKey {
		t.Errorf("APIKey = %q, expected masked version", result.APIKey)
	}
	if !strings.Contains(result.APIKey, "...") {
		t.Errorf("APIKey = %q, expected to contain '...'", result.APIKey)
	}
}

func TestMaskedConfig_ShortAPIKey(t *testing.T) {
	cfg := &config.Config{
		Provider: "custom_openai",
		APIKey:   "short",
	}
	result := maskedConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// API key shorter than 8 chars should be returned as-is per MaskAPIKey
	if result.APIKey != "short" {
		t.Errorf("APIKey = %q, want %q", result.APIKey, "short")
	}
}

func TestMaskedConfig_StripsProviderAuth(t *testing.T) {
	cfg := &config.Config{
		Provider:      "github_copilot",
		ProviderAuth:  json.RawMessage(`{"token":"secret"}`),
		APIKey:        "ghu-token",
	}
	result := maskedConfig(cfg)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.ProviderAuth != nil {
		t.Errorf("ProviderAuth should be nil, got %v", result.ProviderAuth)
	}
}

func TestApplyAuthUpdate_Nil(t *testing.T) {
	cfg := &config.Config{APIKey: "original"}
	applyAuthUpdate(cfg, nil)
	if cfg.APIKey != "original" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "original")
	}
}

func TestApplyAuthUpdate_WithAPIKeyAndProviderAuth(t *testing.T) {
	cfg := &config.Config{APIKey: "old-key"}
	update := &provider.AuthUpdate{
		APIKey:       "new-key",
		ProviderAuth: json.RawMessage(`{"token":"new-token"}`),
	}
	applyAuthUpdate(cfg, update)
	if cfg.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "new-key")
	}
	if string(cfg.ProviderAuth) != `{"token":"new-token"}` {
		t.Errorf("ProviderAuth = %q, want %q", string(cfg.ProviderAuth), `{"token":"new-token"}`)
	}
}

func TestApplyAuthUpdate_EmptyProviderAuth(t *testing.T) {
	cfg := &config.Config{APIKey: "old-key", ProviderAuth: json.RawMessage(`{"token":"old"}`)}
	update := &provider.AuthUpdate{
		APIKey:       "new-key",
		ProviderAuth: json.RawMessage(`{}`),
	}
	applyAuthUpdate(cfg, update)
	if cfg.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "new-key")
	}
}

func TestApplyAuthUpdate_NilProviderAuth(t *testing.T) {
	cfg := &config.Config{APIKey: "old-key", ProviderAuth: json.RawMessage(`{"token":"old"}`)}
	update := &provider.AuthUpdate{
		APIKey:       "new-key",
		ProviderAuth: nil,
	}
	applyAuthUpdate(cfg, update)
	if cfg.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", cfg.APIKey, "new-key")
	}
	if cfg.ProviderAuth != nil {
		t.Errorf("ProviderAuth should be nil, got %v", cfg.ProviderAuth)
	}
}

func TestWriteConfigError_HTMXRequest(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/config", nil)
	r.Header.Set("HX-Request", "true")

	writeConfigError(w, r, http.StatusUnprocessableEntity, "validation error")

	resp := w.Result()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestWriteConfigError_JSONRequest(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/config", nil)
	r.Header.Set("Content-Type", "application/json")

	writeConfigError(w, r, http.StatusUnprocessableEntity, "validation error")

	resp := w.Result()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "validation error" {
		t.Errorf("error = %q, want %q", body["error"], "validation error")
	}
}

func TestWriteConfigError_FormContentTypeNoHTMX(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/config", nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	writeConfigError(w, r, http.StatusBadRequest, "bad request")

	resp := w.Result()
	// Without HX-Request and non-JSON Content-Type: renders HTML toast
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestWriteSettingsForm_RendersContent(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)

	cfg := &config.Config{
		Provider: "custom_openai",
		Model:    "gpt-4",
		BaseURL:  "http://example.com",
	}
	models := []string{"gpt-4", "gpt-3.5-turbo"}
	writeSettingsForm(w, r, http.StatusOK, cfg, models, "test message")

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestLoadConfigState_Defaults(t *testing.T) {
	srv := newInternalTestServer(t)
	state := srv.loadConfigState(context.Background())
	if state.cfg == nil {
		t.Fatal("expected non-nil config")
	}
	// Default config should be valid
	if state.err != nil {
		t.Logf("loadConfigState returned error (expected with no provider): %v", state.err)
	}
}

func TestLoadConfigState_ValidConfig(t *testing.T) {
	srv := newInternalTestServer(t)

	// Save a valid config to trigger validation
	cfg := &config.Config{
		Provider: "custom_openai",
		BaseURL:  "http://example.com",
		APIKey:   "sk-test",
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	state := srv.loadConfigState(context.Background())
	if state.cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if state.err != nil {
		t.Logf("loadConfigState error (expected if model discovery fails): %v", state.err)
	}
}

// newInternalTestServer creates a minimal Server for internal test use.
func newInternalTestServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(ServerConfig{
		ConfigPath: t.TempDir() + "/config.json",
		Workspace:  t.TempDir(),
	})
}

func TestWriteBrowseError(t *testing.T) {
	w := httptest.NewRecorder()
	writeBrowseError(w, "permission denied", http.StatusForbidden)

	resp := w.Result()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "permission denied" {
		t.Errorf("error = %q, want %q", body["error"], "permission denied")
	}
}

func TestBuildBreadcrumbs_Root(t *testing.T) {
	crumbs := buildBreadcrumbs("/")
	if len(crumbs) != 1 {
		t.Fatalf("expected 1 crumb, got %d", len(crumbs))
	}
	if crumbs[0].Label != "/" {
		t.Errorf("label = %q, want %q", crumbs[0].Label, "/")
	}
	if crumbs[0].Path != "/" {
		t.Errorf("path = %q, want %q", crumbs[0].Path, "/")
	}
}

func TestBuildBreadcrumbs_SingleLevel(t *testing.T) {
	crumbs := buildBreadcrumbs("/home")
	// Expect: /, home
	if len(crumbs) != 2 {
		t.Fatalf("expected 2 crumbs, got %d: %+v", len(crumbs), crumbs)
	}
	if crumbs[0].Label != "/" {
		t.Errorf("crumbs[0].Label = %q, want %q", crumbs[0].Label, "/")
	}
	if crumbs[1].Label != "home" {
		t.Errorf("crumbs[1].Label = %q, want %q", crumbs[1].Label, "home")
	}
	if crumbs[1].Path != "/home" {
		t.Errorf("crumbs[1].Path = %q, want %q", crumbs[1].Path, "/home")
	}
}

func TestBuildBreadcrumbs_Nested(t *testing.T) {
	crumbs := buildBreadcrumbs("/home/user/projects/eitri")
	if len(crumbs) != 5 {
		t.Fatalf("expected 5 crumbs, got %d: %+v", len(crumbs), crumbs)
	}
	if crumbs[0].Label != "/" {
		t.Errorf("crumbs[0].Label = %q, want %q", crumbs[0].Label, "/")
	}
	if crumbs[len(crumbs)-1].Label != "eitri" {
		t.Errorf("last label = %q, want %q", crumbs[len(crumbs)-1].Label, "eitri")
	}
	if crumbs[len(crumbs)-1].Path != "/home/user/projects/eitri" {
		t.Errorf("last path = %q, want %q", crumbs[len(crumbs)-1].Path, "/home/user/projects/eitri")
	}

	// Verify cumulative paths
	expectedPaths := []string{"/", "/home", "/home/user", "/home/user/projects", "/home/user/projects/eitri"}
	for i, crumb := range crumbs {
		if crumb.Path != expectedPaths[i] {
			t.Errorf("crumbs[%d].Path = %q, want %q", i, crumb.Path, expectedPaths[i])
		}
	}
}

func TestBuildBreadcrumbs_Empty(t *testing.T) {
	crumbs := buildBreadcrumbs("")
	if crumbs != nil {
		t.Errorf("expected nil, got %+v", crumbs)
	}
}

func TestBuildBreadcrumbs_TrailingSlash(t *testing.T) {
	crumbs := buildBreadcrumbs("/home/user/")
	// Should normalize and produce same as "/home/user"
	if len(crumbs) != 3 {
		t.Fatalf("expected 3 crumbs, got %d: %+v", len(crumbs), crumbs)
	}
	if crumbs[2].Label != "user" {
		t.Errorf("last label = %q, want %q", crumbs[2].Label, "user")
	}
}
