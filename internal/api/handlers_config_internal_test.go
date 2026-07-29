package api

import (
	"context"
	"encoding/json"
	"io"
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
		Provider:     "github_copilot",
		ProviderAuth: json.RawMessage(`{"token":"secret"}`),
		APIKey:       "ghu-token",
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

func TestWriteSettingsForm_ThinkingLevelDropdown_NonReasoningModel(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)

	cfg := &config.Config{
		Provider: "custom_openai",
		Model:    "gpt-4o", // non-reasoning model
		BaseURL:  "http://example.com",
		APIKey:   "sk-test",
	}
	models := []string{"gpt-4o", "gpt-3.5-turbo"}
	writeSettingsForm(w, r, http.StatusOK, cfg, models, "")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)

	// Non-reasoning model should only show "Default (off)" option
	if !strings.Contains(html, "Default (off)") {
		t.Error("expected 'Default (off)' option for non-reasoning model")
	}
	// Should NOT include low/medium/high options
	if strings.Contains(html, "value=\"low\"") {
		t.Error("should NOT contain 'low' option for non-reasoning model")
	}
	if strings.Contains(html, "value=\"medium\"") {
		t.Error("should NOT contain 'medium' option for non-reasoning model")
	}
	if strings.Contains(html, "value=\"high\"") {
		t.Error("should NOT contain 'high' option for non-reasoning model")
	}
}

func TestWriteSettingsForm_ThinkingLevelDropdown_ReasoningModel(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		models   []string
	}{
		{
			name:     "deepseek",
			provider: "custom_openai",
			model:    "deepseek-r1",
			models:   []string{"deepseek-r1", "gpt-4o"},
		},
		{
			name:     "github_copilot_gpt_5_5",
			provider: "github_copilot",
			model:    "gpt-5.5",
			models:   []string{"gpt-5.5", "gpt-4.1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/settings", nil)

			cfg := &config.Config{
				Provider: tc.provider,
				Model:    tc.model,
				BaseURL:  "http://example.com",
				APIKey:   "sk-test",
			}
			writeSettingsForm(w, r, http.StatusOK, cfg, tc.models, "")

			resp := w.Result()
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			html := string(body)

			// Reasoning model should show "Default (off)" plus reasoning options
			if !strings.Contains(html, "Default (off)") {
				t.Error("expected 'Default (off)' option for reasoning model")
			}
			if !strings.Contains(html, "value=\"low\"") {
				t.Error("expected 'low' option for reasoning model")
			}
			if !strings.Contains(html, "value=\"medium\"") {
				t.Error("expected 'medium' option for reasoning model")
			}
			if !strings.Contains(html, "value=\"high\"") {
				t.Error("expected 'high' option for reasoning model")
			}
			// Check that the display text is capitalized
			if !strings.Contains(html, ">Low</option>") {
				t.Error("expected 'Low' display text for reasoning model")
			}
			if !strings.Contains(html, ">Medium</option>") {
				t.Error("expected 'Medium' display text for reasoning model")
			}
			if !strings.Contains(html, ">High</option>") {
				t.Error("expected 'High' display text for reasoning model")
			}
		})
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

func TestHandleGetModels_UsesSubmittedFormValues(t *testing.T) {
	oldProviderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("old saved base URL was queried: %s", r.URL.Path)
	}))
	defer oldProviderSrv.Close()

	newProviderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer submitted-key" {
			t.Errorf("Authorization = %q, want Bearer submitted-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"object":"list","data":[{"id":"submitted-model"}]}`))
	}))
	defer newProviderSrv.Close()

	srv := newInternalTestServer(t)
	saved := &config.Config{
		Provider: "custom_openai",
		BaseURL:  oldProviderSrv.URL,
		APIKey:   "saved-key",
		Model:    "saved-model",
	}
	if err := config.Save(srv.config.ConfigPath, saved); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+newProviderSrv.URL+"&api_key=submitted-key", nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	var body struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0] != "submitted-model" {
		t.Fatalf("data = %#v, want [submitted-model]", body.Data)
	}

	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BaseURL != oldProviderSrv.URL || loaded.APIKey != "saved-key" {
		t.Fatalf("saved config mutated: base_url=%q api_key=%q", loaded.BaseURL, loaded.APIKey)
	}
}

func TestHandleGetModels_TestConnectionReportsSuccessCountAndVerifiedSelectedModel(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"object":"list","data":[{"id":"draft-model"},{"id":"other-model"}]}`))
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	saved := &config.Config{
		Provider: "custom_openai",
		BaseURL:  "http://saved.invalid",
		APIKey:   "saved-key",
		Model:    "saved-model",
	}
	if err := config.Save(srv.config.ConfigPath, saved); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+providerSrv.URL+"&api_key=draft-key&model=draft-model", nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var body struct {
		Data                     []string `json:"data"`
		Count                    int      `json:"count"`
		SelectedModel            string   `json:"selected_model"`
		SelectedModelVerified    bool     `json:"selected_model_verified"`
		SelectedModelUnavailable bool     `json:"selected_model_unavailable"`
		Message                  string   `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 2 || !body.SelectedModelVerified || body.SelectedModelUnavailable || body.SelectedModel != "draft-model" {
		t.Fatalf("body = %+v, want count=2 verified draft-model", body)
	}
	if !strings.Contains(body.Message, "Connection OK") || !strings.Contains(body.Message, "2 models") || !strings.Contains(body.Message, "draft-model verified") {
		t.Fatalf("message = %q, want success count and verification", body.Message)
	}

	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BaseURL != saved.BaseURL || loaded.APIKey != saved.APIKey || loaded.Model != saved.Model {
		t.Fatalf("saved config mutated: %+v", loaded)
	}
}

func TestHandleGetModels_TestConnectionReportsUnavailableSelectedModelWithoutSaving(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"object":"list","data":[{"id":"available-model"}]}`))
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	saved := &config.Config{Provider: "custom_openai", BaseURL: providerSrv.URL, APIKey: "saved-key", Model: "saved-model"}
	if err := config.Save(srv.config.ConfigPath, saved); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+providerSrv.URL+"&api_key=draft-key&model=missing-model", nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var body struct {
		SelectedModelUnavailable bool   `json:"selected_model_unavailable"`
		SelectedModelVerified    bool   `json:"selected_model_verified"`
		Message                  string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.SelectedModelUnavailable || body.SelectedModelVerified || !strings.Contains(body.Message, "missing-model unavailable") {
		t.Fatalf("body = %+v, want selected model unavailable", body)
	}
	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Model != saved.Model || loaded.APIKey != saved.APIKey {
		t.Fatalf("saved config mutated: %+v", loaded)
	}
}

func TestHandleGetModels_TestConnectionBadCredentialAndEndpointDoNotSave(t *testing.T) {
	t.Run("bad credential", func(t *testing.T) {
		providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer bad-key" {
				t.Errorf("Authorization = %q, want Bearer bad-key", got)
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer providerSrv.Close()

		srv := newInternalTestServer(t)
		saved := &config.Config{Provider: "custom_openai", BaseURL: providerSrv.URL, APIKey: "saved-key", Model: "saved-model"}
		if err := config.Save(srv.config.ConfigPath, saved); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+providerSrv.URL+"&api_key=bad-key", nil)
		w := httptest.NewRecorder()
		srv.handleGetModels(w, req)

		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 502; body=%s", resp.StatusCode, body)
		}
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ToLower(body["error"]), "authentication failed") {
			t.Fatalf("error = %q, want actionable authentication failure", body["error"])
		}
		loaded, err := config.Load(srv.config.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.APIKey != saved.APIKey || loaded.Model != saved.Model {
			t.Fatalf("saved config mutated: %+v", loaded)
		}
	})

	t.Run("bad endpoint", func(t *testing.T) {
		providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		badURL := providerSrv.URL
		providerSrv.Close()

		srv := newInternalTestServer(t)
		saved := &config.Config{Provider: "custom_openai", BaseURL: "http://saved.invalid", APIKey: "saved-key", Model: "saved-model"}
		if err := config.Save(srv.config.ConfigPath, saved); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+badURL+"&api_key=draft-key", nil)
		w := httptest.NewRecorder()
		srv.handleGetModels(w, req)

		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 502; body=%s", resp.StatusCode, body)
		}
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(body["error"], "Provider unreachable") {
			t.Fatalf("error = %q, want actionable endpoint failure", body["error"])
		}
		loaded, err := config.Load(srv.config.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.BaseURL != saved.BaseURL || loaded.APIKey != saved.APIKey || loaded.Model != saved.Model {
			t.Fatalf("saved config mutated: %+v", loaded)
		}
	})
}

func TestHandleGetModels_TestConnectionUsesNewDraftAPIKey(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-new-draft-key" {
			t.Errorf("Authorization = %q, want Bearer sk-new-draft-key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"object":"list","data":[{"id":"new-key-ok"}]}`))
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	saved := &config.Config{Provider: "custom_openai", BaseURL: providerSrv.URL, APIKey: "sk-saved-secret-value"}
	if err := config.Save(srv.config.ConfigPath, saved); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+providerSrv.URL+"&api_key=sk-new-draft-key", nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKey != saved.APIKey {
		t.Fatalf("saved APIKey = %q, want %q", loaded.APIKey, saved.APIKey)
	}
}

func TestHandleGetModels_GitHubCopilotMissingAuthDoesNotStartDeviceFlow(t *testing.T) {
	srv := newInternalTestServer(t)
	saved := &config.Config{Provider: "github_copilot", BaseURL: "http://saved.invalid", APIKey: "", Model: ""}
	if err := config.Save(srv.config.ConfigPath, saved); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=github_copilot&base_url=http://example.invalid&api_key=", nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 502; body=%s", resp.StatusCode, body)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["error"], "Authenticate with GitHub") && !strings.Contains(body["error"], "enter a token") {
		t.Fatalf("error = %q, want auth guidance without device flow", body["error"])
	}
}

func TestHandleGetModels_SubmittedMaskedAPIKeyKeepsSavedSecret(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-saved-secret-value" {
			t.Errorf("Authorization = %q, want Bearer sk-saved-secret-value", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"object":"list","data":[{"id":"masked-ok"}]}`))
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	saved := &config.Config{
		Provider: "custom_openai",
		BaseURL:  providerSrv.URL,
		APIKey:   "sk-saved-secret-value",
	}
	if err := config.Save(srv.config.ConfigPath, saved); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=custom_openai&base_url="+providerSrv.URL+"&api_key="+config.MaskAPIKey(saved.APIKey), nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
}

func TestSettingsForm_TestConnectionIncludesCurrentFormValues(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/settings", nil)
	cfg := &config.Config{Provider: "custom_openai", BaseURL: "http://example.com", APIKey: "sk-test"}

	writeSettingsForm(w, r, http.StatusOK, cfg, []string{"gpt-4o"}, "")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if !strings.Contains(html, `id="test-connection-btn"`) {
		t.Fatal("settings form missing Test Connection button")
	}
	if !strings.Contains(html, `hx-include="closest form"`) {
		t.Fatalf("Test Connection button missing hx-include closest form: %s", html)
	}
}

func TestHandlePutConfig_AutoPopulatesContextWindowFromDiscovery(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/gpt-4o" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"id":"gpt-4o","context_length":128000}`))
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"},{"id":"gpt-4o-mini"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	cfg := &config.Config{
		Provider:            "custom_openai",
		BaseURL:             providerSrv.URL,
		APIKey:              "sk-test",
		Model:               "", // no model selected yet
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000, // default
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	// PUT with model change — should auto-populate context window
	body := `{"model":"gpt-4o","api_key":"sk-test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Verify saved config has auto-populated context window
	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	// After auto-populate from discovery, context should be 128000
	if loaded.ContextWindowTokens != 128000 {
		t.Errorf("ContextWindowTokens = %d, want 128000 (auto-populated from discovery)", loaded.ContextWindowTokens)
	}
}

func TestHandlePutConfig_DoesNotOverrideWhenContextWindowOverridden(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	cfg := &config.Config{
		Provider:                "custom_openai",
		BaseURL:                 providerSrv.URL,
		APIKey:                  "sk-test",
		Model:                   "gpt-4o",
		SessionTimeout:          30 * 60_000_000_000,
		CommandTimeout:          60 * 1_000_000_000,
		MaxTurns:                25,
		ContextWindowTokens:     99999, // manually set value
		ContextWindowOverridden: true,
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	// PUT with same model — should NOT override ContextWindowTokens
	body := `{"model":"gpt-4o","api_key":"sk-test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ContextWindowTokens != 99999 {
		t.Errorf("ContextWindowTokens = %d, want 99999 (should not override)", loaded.ContextWindowTokens)
	}
}

func TestHandlePutConfig_FallsBackToStaticTableWhenDiscoveryHasNoData(t *testing.T) {
	// Provider returns models but no context_length field and no model detail endpoint
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	cfg := &config.Config{
		Provider:            "custom_openai",
		BaseURL:             providerSrv.URL,
		APIKey:              "sk-test",
		Model:               "old-model",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000, // default
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	// PUT with model change to gpt-4o — should fall back to static table (128000)
	body := `{"model":"gpt-4o","api_key":"sk-test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ContextWindowTokens != 128000 {
		t.Errorf("ContextWindowTokens = %d, want 128000 (fallback to static table)", loaded.ContextWindowTokens)
	}
}

func TestHandlePutConfig_ClearsThinkingLevelForUnsupportedModel(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	cfg := &config.Config{
		Provider:            "custom_openai",
		BaseURL:             providerSrv.URL,
		APIKey:              "sk-test",
		Model:               "gpt-4o", // does not support thinking levels
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 128000,
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	// PUT with thinking_level=high on a non-reasoning model
	body := `{"thinking_level":"high","api_key":"sk-test"}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handlePutConfig(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	// Verify thinking_level was cleared in saved config
	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ThinkingLevel != "" {
		t.Errorf("ThinkingLevel = %q, want empty (cleared for unsupported model)", loaded.ThinkingLevel)
	}

	// Verify response body contains a notice about the clearing
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	bodyStr := string(bodyBytes)
	if !strings.Contains(bodyStr, "does not support reasoning effort") {
		t.Errorf("response body should contain notice about clearing, got: %s", bodyStr)
	}
}

func TestHandleGetModels_ReturnsEmptyModelsOnProviderOverride(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate auth failure for the "wrong" provider
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	cfg := &config.Config{
		Provider:       "custom_openai",
		BaseURL:        providerSrv.URL,
		APIKey:         "sk-old-provider",
		SessionTimeout: 30 * 60_000_000_000,
		CommandTimeout: 60 * 1_000_000_000,
		MaxTurns:       25,
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	// GET /api/models with provider override — should return empty models, not error
	req := httptest.NewRequest(http.MethodGet, "/api/models?provider=opencode_go&base_url="+providerSrv.URL, nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	data, ok := body["data"]
	if !ok {
		t.Fatal("response missing 'data' field")
	}
	models, ok := data.([]any)
	if !ok {
		t.Fatalf("data is %T, want []any", data)
	}
	if len(models) != 0 {
		t.Errorf("expected 0 models on auth failure with overridden provider, got %d", len(models))
	}

	// Verify saved config was NOT corrupted
	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != "custom_openai" {
		t.Errorf("saved provider was corrupted: got %q, want %q", loaded.Provider, "custom_openai")
	}
	if loaded.BaseURL != providerSrv.URL {
		t.Errorf("saved base_url was corrupted: got %q, want %q", loaded.BaseURL, providerSrv.URL)
	}
	if loaded.APIKey != "sk-old-provider" {
		t.Errorf("saved api_key was corrupted: got %q, want %q", loaded.APIKey, "sk-old-provider")
	}
}

func TestHandleGetModels_ReturnsModelsForSameProvider(t *testing.T) {
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer providerSrv.Close()

	srv := newInternalTestServer(t)
	cfg := &config.Config{
		Provider:       "custom_openai",
		BaseURL:        providerSrv.URL,
		APIKey:         "sk-test",
		SessionTimeout: 30 * 60_000_000_000,
		CommandTimeout: 60 * 1_000_000_000,
		MaxTurns:       25,
	}
	if err := config.Save(srv.config.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}

	// GET /api/models without provider override — should return models
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	w := httptest.NewRecorder()
	srv.handleGetModels(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	data, ok := body["data"]
	if !ok {
		t.Fatal("response missing 'data' field")
	}
	models, ok := data.([]any)
	if !ok {
		t.Fatalf("data is %T, want []any", data)
	}
	if len(models) != 2 {
		t.Errorf("expected 2 models, got %d", len(models))
	}

	// Verify saved config was not corrupted
	loaded, err := config.Load(srv.config.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != "custom_openai" {
		t.Errorf("saved provider was corrupted: got %q, want %q", loaded.Provider, "custom_openai")
	}
}
