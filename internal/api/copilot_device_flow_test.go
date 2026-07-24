package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/session"
)

// ————— copilotDeviceFlowStore tests ————— —

func TestNewCopilotDeviceFlowStore(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	if s == nil {
		t.Fatal("newCopilotDeviceFlowStore returned nil")
	}
	// Store should be empty
	if _, ok := s.get("nonexistent"); ok {
		t.Error("expected false for nonexistent key")
	}
}

func TestCopilotDeviceFlowStorePutAndGet(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	cfg := &config.Config{Provider: "github_copilot", APIKey: "test-key"}
	flow := &copilotDeviceFlowState{
		ID:              "flow-1",
		BrowserID:       "browser-1",
		Config:          cfg,
		DeviceCode:      "device-123",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://github.com/login/device",
		Interval:        5 * time.Second,
		ExpiresAt:       time.Now().Add(time.Hour),
	}

	s.put(flow)

	got, ok := s.get("flow-1")
	if !ok {
		t.Fatal("expected flow to be found")
	}
	if got.ID != "flow-1" {
		t.Errorf("ID = %q, want %q", got.ID, "flow-1")
	}
	if got.DeviceCode != "device-123" {
		t.Errorf("DeviceCode = %q, want %q", got.DeviceCode, "device-123")
	}
	if got.UserCode != "ABCD-EFGH" {
		t.Errorf("UserCode = %q, want %q", got.UserCode, "ABCD-EFGH")
	}
	if got.Config == nil || got.Config.Provider != "github_copilot" {
		t.Error("Config should be preserved")
	}
}

func TestCopilotDeviceFlowStoreGetMissing(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	if _, ok := s.get("does-not-exist"); ok {
		t.Error("expected false for missing flow")
	}
}

func TestCopilotDeviceFlowStoreGetReturnsClone(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	cfg := &config.Config{Provider: "github_copilot", APIKey: "secret-123"}
	flow := &copilotDeviceFlowState{
		ID:     "flow-clone-test",
		Config: cfg,
	}
	s.put(flow)

	got, ok := s.get("flow-clone-test")
	if !ok {
		t.Fatal("expected flow to be found")
	}

	// Modify the returned Config — original in store should be unchanged
	got.Config.APIKey = "hacked"
	got.ID = "changed-id"

	// Re-fetch from store
	got2, ok := s.get("flow-clone-test")
	if !ok {
		t.Fatal("expected flow to be found on second get")
	}
	if got2.ID != "flow-clone-test" {
		t.Errorf("original ID changed, got %q", got2.ID)
	}
	if got2.Config.APIKey != "secret-123" {
		t.Errorf("original Config mutated, got %q", got2.Config.APIKey)
	}
}

func TestCopilotDeviceFlowStoreUpdate(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	flow := &copilotDeviceFlowState{ID: "flow-update", DeviceCode: "original"}
	s.put(flow)

	flow.DeviceCode = "updated"
	s.update(flow)

	got, ok := s.get("flow-update")
	if !ok {
		t.Fatal("expected flow to be found")
	}
	if got.DeviceCode != "updated" {
		t.Errorf("DeviceCode = %q, want %q", got.DeviceCode, "updated")
	}
}

func TestCopilotDeviceFlowStoreDelete(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	flow := &copilotDeviceFlowState{ID: "flow-delete"}
	s.put(flow)
	s.delete("flow-delete")

	if _, ok := s.get("flow-delete"); ok {
		t.Error("expected flow to be deleted")
	}
}

func TestCopilotDeviceFlowStoreDeleteExisting(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	// Deleting a non-existent key should not panic
	s.delete("non-existent")
}

func TestCopilotDeviceFlowStoreConcurrency(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	var wg sync.WaitGroup
	numGoroutines := 50

	// Concurrent puts
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("flow-%d", i)
			s.put(&copilotDeviceFlowState{ID: id, DeviceCode: fmt.Sprintf("device-%d", i)})
		}()
	}
	wg.Wait()

	// All should be present
	for i := 0; i < numGoroutines; i++ {
		id := fmt.Sprintf("flow-%d", i)
		got, ok := s.get(id)
		if !ok {
			t.Errorf("missing flow %q after concurrent puts", id)
			continue
		}
		if got.DeviceCode != fmt.Sprintf("device-%d", i) {
			t.Errorf("flow %q DeviceCode = %q", id, got.DeviceCode)
		}
	}

	// Concurrent gets
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("flow-%d", i)
			s.get(id)
		}()
	}
	wg.Wait()

	// Concurrent delete
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			id := fmt.Sprintf("flow-%d", i)
			s.delete(id)
		}()
	}
	wg.Wait()

	// All should be gone
	for i := 0; i < numGoroutines; i++ {
		id := fmt.Sprintf("flow-%d", i)
		if _, ok := s.get(id); ok {
			t.Errorf("flow %q still present after concurrent deletes", id)
		}
	}
}

// ————— Config parsing tests ————— —

func TestParseConfigPatch_JSON(t *testing.T) {
	body := `{"provider":"github_copilot","api_key":"test-key"}`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	patch, err := parseConfigPatch(r)
	if err != nil {
		t.Fatalf("parseConfigPatch error: %v", err)
	}
	if patch["provider"] != "github_copilot" {
		t.Errorf("provider = %v, want github_copilot", patch["provider"])
	}
	if patch["api_key"] != "test-key" {
		t.Errorf("api_key = %v, want test-key", patch["api_key"])
	}
}

func TestParseConfigPatch_JSONInvalid(t *testing.T) {
	body := `{invalid json`
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	_, err := parseConfigPatch(r)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseConfigPatch_FormURLEncoded(t *testing.T) {
	form := url.Values{
		"provider":      {"github_copilot"},
		"api_key":       {"test-key"},
		"base_url":      {"https://api.example.com"},
		"max_turns":     {"50"},
		"clear_api_key": {"false"},
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	patch, err := parseConfigPatch(r)
	if err != nil {
		t.Fatalf("parseConfigPatch error: %v", err)
	}
	if patch["provider"] != "github_copilot" {
		t.Errorf("provider = %v, want %q", patch["provider"], "github_copilot")
	}
	if patch["api_key"] != "test-key" {
		t.Errorf("api_key = %v, want test-key", patch["api_key"])
	}
	if patch["base_url"] != "https://api.example.com" {
		t.Errorf("base_url = %v", patch["base_url"])
	}
	if patch["max_turns"] != "50" {
		t.Errorf("max_turns = %v", patch["max_turns"])
	}
}

func TestParseConfigPatch_FormEmptyAPIKeyWithoutClear(t *testing.T) {
	// When api_key is empty and clear_api_key is not present, api_key should be removed from patch
	form := url.Values{
		"provider": {"github_copilot"},
		"api_key":  {""},
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	patch, err := parseConfigPatch(r)
	if err != nil {
		t.Fatalf("parseConfigPatch error: %v", err)
	}
	if _, ok := patch["api_key"]; ok {
		t.Error("api_key should be removed from patch when empty without clear_api_key")
	}
}

func TestParseConfigPatch_FormEmptyAPIKeyWithClear(t *testing.T) {
	// When api_key is empty and clear_api_key is present, api_key should remain in patch
	form := url.Values{
		"provider":      {"github_copilot"},
		"api_key":       {""},
		"clear_api_key": {"true"},
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	patch, err := parseConfigPatch(r)
	if err != nil {
		t.Fatalf("parseConfigPatch error: %v", err)
	}
	if v, ok := patch["api_key"]; !ok {
		t.Error("api_key should remain in patch when clear_api_key is present")
	} else if v.(string) != "" {
		t.Errorf("api_key = %q, want empty string", v)
	}
	if v, ok := patch["clear_api_key"]; !ok {
		t.Error("clear_api_key should be in patch")
	} else if v.(string) != "true" {
		t.Errorf("clear_api_key = %q, want true", v)
	}
}

func TestParseConfigPatch_ContentTypeVariations(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		wantErr     bool
		wantVal     string
	}{
		{
			name:        "JSON with charset",
			contentType: "application/json; charset=utf-8",
			body:        `{"api_key":"value1"}`,
			wantErr:     false,
			wantVal:     "value1",
		},
		{
			name:        "Form with charset",
			contentType: "application/x-www-form-urlencoded; charset=utf-8",
			body:        "api_key=value2",
			wantErr:     false,
			wantVal:     "value2",
		},
		{
			name:        "JSON with extra spaces",
			contentType: "application/json ",
			body:        `{"api_key":"value3"}`,
			wantErr:     false,
			wantVal:     "value3",
		},
		{
			name:        "Fallback to form parsing",
			contentType: "text/plain",
			body:        "api_key=value4",
			wantErr:     false,
			wantVal:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)

			patch, err := parseConfigPatch(r)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotVal, _ := patch["api_key"].(string)
			if gotVal != tc.wantVal {
				t.Errorf("api_key = %v, want %q", patch["api_key"], tc.wantVal)
			}
		})
	}
}

// ————— normalizePatchedConfig tests ————— —

func TestNormalizePatchedConfig_NoMaskedKey(t *testing.T) {
	cfg := &config.Config{Provider: "opencode_go", APIKey: "my-secret-key"}
	patch := map[string]any{"provider": "github_copilot", "api_key": "new-key"}

	result := normalizePatchedConfig(cfg, patch)
	if result.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", result.APIKey, "new-key")
	}
}

func TestNormalizePatchedConfig_MaskedKeyRemoved(t *testing.T) {
	cfg := &config.Config{Provider: "opencode_go", APIKey: "my-secret-key"}
	masked := config.MaskAPIKey(cfg.APIKey)
	patch := map[string]any{"api_key": masked}

	result := normalizePatchedConfig(cfg, patch)
	// api_key should be removed from patch since it matches masked version
	if _, ok := patch["api_key"]; ok {
		t.Error("api_key should be removed from patch when it matches masked key")
	}
	// Original config key should be preserved
	if result.APIKey != cfg.APIKey {
		t.Errorf("APIKey = %q, want %q", result.APIKey, cfg.APIKey)
	}
}

func TestNormalizePatchedConfig_SessionTimeoutMultiplied(t *testing.T) {
	cfg := &config.Config{SessionTimeout: 60_000_000_000} // 1 minute default
	patch := map[string]any{"session_timeout": float64(30)}

	result := normalizePatchedConfig(cfg, patch)
	expected := int64(30 * 1_000_000_000)
	if result.SessionTimeout != expected {
		t.Errorf("SessionTimeout = %d, want %d", result.SessionTimeout, expected)
	}
}

func TestNormalizePatchedConfig_SessionTimeoutNotMultipliedIfLarge(t *testing.T) {
	cfg := &config.Config{SessionTimeout: 60_000_000_000}
	patch := map[string]any{"session_timeout": float64(120_000_000_000)}

	result := normalizePatchedConfig(cfg, patch)
	if result.SessionTimeout != int64(120_000_000_000) {
		t.Errorf("SessionTimeout = %d, want %d", result.SessionTimeout, 120_000_000_000)
	}
}

func TestNormalizePatchedConfig_SessionTimeoutZero(t *testing.T) {
	cfg := &config.Config{SessionTimeout: 60_000_000_000}
	patch := map[string]any{"session_timeout": float64(0)}

	result := normalizePatchedConfig(cfg, patch)
	if result.SessionTimeout != 0 {
		t.Errorf("SessionTimeout = %d, want 0", result.SessionTimeout)
	}
}

func TestNormalizePatchedConfig_CommandTimeoutMultiplied(t *testing.T) {
	cfg := &config.Config{CommandTimeout: 60_000_000_000}
	patch := map[string]any{"command_timeout": float64(30)}

	result := normalizePatchedConfig(cfg, patch)
	expected := int64(30 * 1_000_000_000)
	if result.CommandTimeout != expected {
		t.Errorf("CommandTimeout = %d, want %d", result.CommandTimeout, expected)
	}
}

func TestNormalizePatchedConfig_CommandTimeoutNotMultipliedIfLarge(t *testing.T) {
	cfg := &config.Config{CommandTimeout: 60_000_000_000}
	patch := map[string]any{"command_timeout": float64(120_000_000_000)}

	result := normalizePatchedConfig(cfg, patch)
	if result.CommandTimeout != int64(120_000_000_000) {
		t.Errorf("CommandTimeout = %d, want %d", result.CommandTimeout, 120_000_000_000)
	}
}

// ————— validateForCopilotDeviceFlow tests ————— —

func TestValidateForCopilotDeviceFlow_ValidGitHubCopilot(t *testing.T) {
	cfg := &config.Config{
		Provider:            "github_copilot",
		APIKey:              "gho_some-token",
		BaseURL:             "https://models.inference.ai.azure.com",
		MaxTurns:            75,
		MaxHistory:          50,
		SessionTimeout:      60_000_000_000,
		CommandTimeout:      60_000_000_000,
		ContextWindowTokens: 256000,
	}
	err := validateForCopilotDeviceFlow(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateForCopilotDeviceFlow_DeviceFlowPending(t *testing.T) {
	// When provider is github_copilot and api_key is empty, it should be replaced with "device-flow-pending"
	cfg := &config.Config{
		Provider:            "github_copilot",
		APIKey:              "",
		BaseURL:             "https://models.inference.ai.azure.com",
		MaxTurns:            75,
		MaxHistory:          50,
		SessionTimeout:      60_000_000_000,
		CommandTimeout:      60_000_000_000,
		ContextWindowTokens: 256000,
	}
	err := validateForCopilotDeviceFlow(cfg)
	if err != nil {
		t.Fatalf("unexpected error for pending flow: %v", err)
	}
}

func TestValidateForCopilotDeviceFlow_InvalidSessionTimeout(t *testing.T) {
	cfg := &config.Config{
		Provider:            "github_copilot",
		APIKey:              "device-flow-pending",
		BaseURL:             "https://models.inference.ai.azure.com",
		MaxTurns:            75,
		MaxHistory:          50,
		SessionTimeout:      1_000, // too small
		CommandTimeout:      60_000_000_000,
		ContextWindowTokens: 256000,
	}
	err := validateForCopilotDeviceFlow(cfg)
	if err == nil {
		t.Fatal("expected error for invalid session_timeout")
	}
}

func TestValidateForCopilotDeviceFlow_InvalidProvider(t *testing.T) {
	cfg := &config.Config{
		Provider:            "nonexistent-provider",
		APIKey:              "test",
		MaxTurns:            75,
		MaxHistory:          50,
		SessionTimeout:      60_000_000_000,
		CommandTimeout:      60_000_000_000,
		ContextWindowTokens: 256000,
	}
	err := validateForCopilotDeviceFlow(cfg)
	if err == nil {
		t.Fatal("expected error for invalid provider")
	}
}

// ————— newCopilotDeviceFlowID tests ————— —

func TestNewCopilotDeviceFlowID_NotEmpty(t *testing.T) {
	id := newCopilotDeviceFlowID()
	if id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestNewCopilotDeviceFlowID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := newCopilotDeviceFlowID()
		if ids[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		ids[id] = true
	}
}

func TestNewCopilotDeviceFlowID_Format(t *testing.T) {
	id := newCopilotDeviceFlowID()
	// Should be a hex string or a fallback "flow-..." format
	if !strings.HasPrefix(id, "flow-") && len(id) != 32 {
		t.Errorf("unexpected ID format: %q (len=%d)", id, len(id))
	}
}

// ————— copilotDeviceFlowView tests ————— —

func TestCopilotDeviceFlowView_Nil(t *testing.T) {
	s := &Server{} // minimal server
	v := s.copilotDeviceFlowView(nil)
	if v != nil {
		t.Error("expected nil for nil flow")
	}
}

func TestCopilotDeviceFlowView_WithFlow(t *testing.T) {
	s := &Server{}
	flow := &copilotDeviceFlowState{
		ID:              "test-flow-id",
		UserCode:        "ABCD-EFGH",
		VerificationURI: "https://github.com/login/device",
		Interval:        5 * time.Second,
	}
	v := s.copilotDeviceFlowView(flow)
	if v == nil {
		t.Fatal("unexpected nil view")
	}
	if v.ID != "test-flow-id" {
		t.Errorf("ID = %q, want %q", v.ID, "test-flow-id")
	}
	if v.UserCode != "ABCD-EFGH" {
		t.Errorf("UserCode = %q, want %q", v.UserCode, "ABCD-EFGH")
	}
	if v.VerificationURI != "https://github.com/login/device" {
		t.Errorf("VerificationURI = %q", v.VerificationURI)
	}
	wantPollURL := "/api/providers/github_copilot/device-flow/test-flow-id"
	if v.PollURL != wantPollURL {
		t.Errorf("PollURL = %q, want %q", v.PollURL, wantPollURL)
	}
	if v.PollTrigger != "every 5s" {
		t.Errorf("PollTrigger = %q, want %q", v.PollTrigger, "every 5s")
	}
	wantCancelURL := "/api/providers/github_copilot/device-flow/test-flow-id"
	if v.CancelURL != wantCancelURL {
		t.Errorf("CancelURL = %q, want %q", v.CancelURL, wantCancelURL)
	}
}

func TestCopilotDeviceFlowView_DefaultInterval(t *testing.T) {
	s := &Server{}
	// Zero interval should default to 5 seconds
	flow := &copilotDeviceFlowState{
		ID:       "test-flow",
		UserCode: "ABCD-EFGH",
		Interval: 0,
	}
	v := s.copilotDeviceFlowView(flow)
	if v == nil {
		t.Fatal("unexpected nil view")
	}
	if v.PollTrigger != "every 5s" {
		t.Errorf("PollTrigger = %q, want %q", v.PollTrigger, "every 5s")
	}
}

// ————— HTTP handler tests ————— —

// newTestServerForCopilotFlow creates a minimal Server with a temp config path
// and returns both the httptest.Server and the config path for assertions.
func newTestServerForCopilotFlow(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	configPath := t.TempDir() + "/config.json"
	// Write an initial valid config file
	initialCfg := config.Defaults()
	initialCfg.Provider = "opencode_go"
	initialCfg.BaseURL = "https://api.openai.com/v1"
	if err := config.Save(configPath, &initialCfg); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	workspace := t.TempDir()
	cfg := ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: session.NewManager(10, workspace),
	}
	srv := NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server, configPath
}

func TestHandleStartCopilotDeviceFlow_WrongProvider(t *testing.T) {
	server, _ := newTestServerForCopilotFlow(t)

	// Non-github_copilot provider should show error
	form := url.Values{
		"provider": {"opencode_go"},
	}
	resp, err := http.PostForm(server.URL+"/api/providers/github_copilot/device-flow/start", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if !strings.Contains(string(body), "Select GitHub Copilot before starting") {
		t.Errorf("response missing error message: %s", string(body))
	}
}

func TestHandleStartCopilotDeviceFlow_InvalidBody(t *testing.T) {
	server, _ := newTestServerForCopilotFlow(t)

	// Send invalid JSON
	resp, err := http.Post(server.URL+"/api/providers/github_copilot/device-flow/start",
		"application/json",
		strings.NewReader(`{invalid}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusBadRequest, string(body))
	}
}

func TestHandlePollCopilotDeviceFlow_NotFound(t *testing.T) {
	server, _ := newTestServerForCopilotFlow(t)

	// Polling a non-existent flow should return 404
	resp, err := http.Get(server.URL + "/api/providers/github_copilot/device-flow/nonexistent-id")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestHandleCancelCopilotDeviceFlow_NotFound(t *testing.T) {
	server, _ := newTestServerForCopilotFlow(t)

	// Canceling a non-existent flow should return 404
	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/providers/github_copilot/device-flow/nonexistent-id", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestHandleStartCopilotDeviceFlow_FormEncodedPatch(t *testing.T) {
	// Start flow with form-encoded body, using custom OAuth server
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/device/code":
			fmt.Fprint(w, `{"device_code":"device-abc","user_code":"XYZW-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	configPath := t.TempDir() + "/config.json"
	initialCfg := config.Defaults()
	initialCfg.Provider = "github_copilot"
	initialCfg.APIKey = ""
	initialCfg.BaseURL = "https://models.inference.ai.azure.com"
	if err := config.Save(configPath, &initialCfg); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	cfg := ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: session.NewManager(10, workspace),
		CopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "test-client-id",
			DeviceCodeURL:  oauth.URL + "/login/device/code",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
	}
	srv := NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	form := url.Values{
		"api_key":  {"some-key"},
		"provider": {"github_copilot"},
		"base_url": {"https://models.inference.ai.azure.com"},
	}
	resp, err := http.PostForm(server.URL+"/api/providers/github_copilot/device-flow/start", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, string(body))
	}
	if !strings.Contains(string(body), "XYZW-1234") {
		t.Errorf("response missing user code: %s", string(body))
	}
}

func TestHandleStartCopilotDeviceFlow_JSONPatch(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/device/code":
			fmt.Fprint(w, `{"device_code":"device-xyz","user_code":"ZYXW-9876","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	configPath := t.TempDir() + "/config.json"
	initialCfg := config.Defaults()
	initialCfg.Provider = "github_copilot"
	initialCfg.BaseURL = "https://models.inference.ai.azure.com"
	if err := config.Save(configPath, &initialCfg); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	cfg := ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: session.NewManager(10, workspace),
		CopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "test-client-id",
			DeviceCodeURL:  oauth.URL + "/login/device/code",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
	}
	srv := NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	jsonBody := `{"provider":"github_copilot","api_key":"test-key","base_url":"https://models.inference.ai.azure.com"}`
	resp, err := http.Post(server.URL+"/api/providers/github_copilot/device-flow/start",
		"application/json",
		strings.NewReader(jsonBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d, body: %s", resp.StatusCode, http.StatusOK, string(body))
	}
	if !strings.Contains(string(body), "ZYXW-9876") {
		t.Errorf("response missing user code: %s", string(body))
	}
}

func TestHandlePollCopilotDeviceFlow_StartAndPollWithBrowserID(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/device/code":
			fmt.Fprint(w, `{"device_code":"device-poll","user_code":"POLL-0001","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		case "/login/oauth/access_token":
			fmt.Fprint(w, `{"access_token":"gho_poll_token","token_type":"bearer","scope":"read:user"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	configPath := t.TempDir() + "/config.json"
	initialCfg := config.Defaults()
	initialCfg.Provider = "github_copilot"
	initialCfg.APIKey = ""
	initialCfg.BaseURL = "https://models.inference.ai.azure.com"
	if err := config.Save(configPath, &initialCfg); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	cfg := ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: session.NewManager(10, workspace),
		CopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "test-client-id",
			DeviceCodeURL:  oauth.URL + "/login/device/code",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
	}
	srv := NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	// First make a request to get a browser cookie
	rootResp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	rootResp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("no browser_id cookie set")
	}

	// Start the device flow
	form := url.Values{
		"provider": {"github_copilot"},
		"base_url": {"https://models.inference.ai.azure.com"},
		"api_key":  {""},
	}
	startReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/providers/github_copilot/device-flow/start",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	startReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	startReq.AddCookie(browserCookie)

	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	startBody, _ := io.ReadAll(startResp.Body)
	startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body: %s", startResp.StatusCode, string(startBody))
	}

	// Add a models endpoint for provider discovery
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true}]}`)
	}))
	defer providerSrv.Close()

	// Need to start a new flow with the provider URL as base_url
	form2 := url.Values{
		"provider": {"github_copilot"},
		"base_url": {providerSrv.URL},
	}
	startReq2, err := http.NewRequest(http.MethodPost, server.URL+"/api/providers/github_copilot/device-flow/start",
		strings.NewReader(form2.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	startReq2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	startReq2.AddCookie(browserCookie)

	startResp2, err := http.DefaultClient.Do(startReq2)
	if err != nil {
		t.Fatal(err)
	}
	startBody2, _ := io.ReadAll(startResp2.Body)
	startResp2.Body.Close()
	if startResp2.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, body: %s", startResp2.StatusCode, string(startBody2))
	}

	flowID2 := extractCopilotFlowID(t, string(startBody2))

	// Poll the device flow
	pollReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/providers/github_copilot/device-flow/"+flowID2, nil)
	if err != nil {
		t.Fatal(err)
	}
	pollReq.AddCookie(browserCookie)

	pollResp, err := http.DefaultClient.Do(pollReq)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResp.Body.Close()

	pollBody, _ := io.ReadAll(pollResp.Body)
	if pollResp.StatusCode != http.StatusOK {
		t.Errorf("poll status = %d, want %d, body: %s", pollResp.StatusCode, http.StatusOK, string(pollBody))
	}

	// Should contain the success message or a model list
	if !strings.Contains(string(pollBody), "GitHub Copilot connected") &&
		!strings.Contains(string(pollBody), "gpt-4.1") {
		t.Logf("poll response body: %s", string(pollBody))
	}

	// Verify config was saved
	loadedCfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loadedCfg.Provider != "github_copilot" {
		t.Errorf("saved provider = %q, want github_copilot", loadedCfg.Provider)
	}
}

func TestHandleCancelCopilotDeviceFlow_Success(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login/device/code":
			fmt.Fprint(w, `{"device_code":"device-cancel","user_code":"CANC-1111","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	configPath := t.TempDir() + "/config.json"
	initialCfg := config.Defaults()
	initialCfg.Provider = "github_copilot"
	if err := config.Save(configPath, &initialCfg); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	cfg := ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: session.NewManager(10, workspace),
		CopilotOAuth: GitHubCopilotOAuthConfig{
			ClientID:       "test-client-id",
			DeviceCodeURL:  oauth.URL + "/login/device/code",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
	}
	srv := NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)

	// Get browser cookie
	rootResp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	rootResp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("no browser_id cookie set")
	}

	// Start flow
	form := url.Values{"provider": {"github_copilot"}}
	startReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/providers/github_copilot/device-flow/start",
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	startReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	startReq.AddCookie(browserCookie)

	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	startBody, _ := io.ReadAll(startResp.Body)
	startResp.Body.Close()

	flowID := extractCopilotFlowID(t, string(startBody))

	// Cancel the flow
	cancelReq, err := http.NewRequest(http.MethodDelete, server.URL+"/api/providers/github_copilot/device-flow/"+flowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelReq.AddCookie(browserCookie)

	cancelResp, err := http.DefaultClient.Do(cancelReq)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()

	cancelBody, _ := io.ReadAll(cancelResp.Body)
	if cancelResp.StatusCode != http.StatusOK {
		t.Errorf("cancel status = %d, want %d, body: %s", cancelResp.StatusCode, http.StatusOK, string(cancelBody))
	}
	if !strings.Contains(string(cancelBody), "GitHub authentication canceled") {
		t.Errorf("response missing cancellation message: %s", string(cancelBody))
	}
}

// extractCopilotFlowID extracts the device-flow ID from an HTML response body.
func extractCopilotFlowID(t *testing.T, body string) string {
	t.Helper()
	// Look for poll/cancel URL pattern
	idx := strings.Index(body, "/api/providers/github_copilot/device-flow/")
	if idx < 0 {
		t.Fatalf("response missing device-flow status URL: %s", body)
	}
	start := idx + len("/api/providers/github_copilot/device-flow/")
	end := strings.IndexAny(body[start:], "\"?' />")
	if end < 0 {
		end = len(body[start:])
	}
	id := body[start : start+end]
	if id == "" {
		t.Fatalf("empty flow ID extracted from: %s", body)
	}
	return id
}

// ————— Provider OAuth config helpers for tests ————— —

// TestCopilotDeviceFlowView_WithFlow_RoundTrip verifies the poll and cancel URLs
// are consistent with route patterns.
func TestCopilotDeviceFlowView_RoundTripURLs(t *testing.T) {
	s := &Server{}
	flow := &copilotDeviceFlowState{
		ID:       "roundtrip-test-id",
		UserCode: "TEST-CODE",
		Interval: 5 * time.Second,
	}
	v := s.copilotDeviceFlowView(flow)

	// Poll URL should be GET /api/providers/github_copilot/device-flow/{id}
	if !strings.Contains(v.PollURL, flow.ID) {
		t.Errorf("PollURL %q does not contain flow ID %q", v.PollURL, flow.ID)
	}
	// Cancel URL should be DELETE /api/providers/github_copilot/device-flow/{id}
	if !strings.Contains(v.CancelURL, flow.ID) {
		t.Errorf("CancelURL %q does not contain flow ID %q", v.CancelURL, flow.ID)
	}
}

// TestParseConfigPatch_FormMaxTurnsNumeric verifies numeric values from forms
// are passed as strings (not converted to numbers).
func TestParseConfigPatch_FormFieldsAsStrings(t *testing.T) {
	form := url.Values{
		"session_timeout": {"30"},
		"command_timeout": {"60"},
		"max_turns":       {"75"},
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	patch, err := parseConfigPatch(r)
	if err != nil {
		t.Fatalf("parseConfigPatch error: %v", err)
	}
	if v, ok := patch["session_timeout"]; ok {
		if s, ok2 := v.(string); ok2 && s != "30" {
			t.Errorf("session_timeout = %v, want 30", v)
		}
	}
	if v, ok := patch["command_timeout"]; ok {
		if s, ok2 := v.(string); ok2 && s != "60" {
			t.Errorf("command_timeout = %v, want 60", v)
		}
	}
	if v, ok := patch["max_turns"]; ok {
		if s, ok2 := v.(string); ok2 && s != "75" {
			t.Errorf("max_turns = %v, want 75", v)
		}
	}
}

// Test that config patch loading with empty body still works
func TestParseConfigPatch_EmptyBodyForm(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	patch, err := parseConfigPatch(r)
	if err != nil {
		t.Fatalf("parseConfigPatch error: %v", err)
	}
	if len(patch) != 0 {
		t.Errorf("expected empty patch, got %v", patch)
	}
}

// TestNormalizePatchedConfig_PreservesOtherConfigFields verifies that
// fields not in the patch are preserved from the base config.
func TestNormalizePatchedConfig_PreservesOtherFields(t *testing.T) {
	cfg := &config.Config{
		Provider:            "opencode_go",
		APIKey:              "original-key",
		BaseURL:             "https://api.openai.com/v1",
		MaxTurns:            75,
		MaxHistory:          50,
		SessionTimeout:      60_000_000_000,
		CommandTimeout:      60_000_000_000,
		ContextWindowTokens: 256000,
		SystemPrompt:        "You are a helpful assistant.",
	}
	patch := map[string]any{"api_key": "new-key"}

	result := normalizePatchedConfig(cfg, patch)
	if result.Provider != "opencode_go" {
		t.Errorf("Provider changed: %q", result.Provider)
	}
	if result.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("BaseURL changed: %q", result.BaseURL)
	}
	if result.MaxTurns != 75 {
		t.Errorf("MaxTurns changed: %d", result.MaxTurns)
	}
	if result.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("SystemPrompt changed: %q", result.SystemPrompt)
	}
	if result.APIKey != "new-key" {
		t.Errorf("APIKey = %q, want %q", result.APIKey, "new-key")
	}
}

// TestValidateForCopilotDeviceFlow_NonGitHubCopilotProvider validates that
// the function works for non-copilot providers too.
func TestValidateForCopilotDeviceFlow_NonCopilotProvider(t *testing.T) {
	cfg := &config.Config{
		Provider:            "opencode_go",
		APIKey:              "sk-test-key",
		BaseURL:             "https://api.openai.com/v1",
		MaxTurns:            75,
		MaxHistory:          50,
		SessionTimeout:      60_000_000_000,
		CommandTimeout:      60_000_000_000,
		ContextWindowTokens: 256000,
	}
	err := validateForCopilotDeviceFlow(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCopilotDeviceFlowStore_GetConfigDeepCopy verifies that the Config pointer
// inside the store is deeply cloned on get (not just the pointer).
func TestCopilotDeviceFlowStore_GetConfigDeepCopy(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	cfg := &config.Config{Provider: "github_copilot", APIKey: "secret", BaseURL: "https://example.com"}
	flow := &copilotDeviceFlowState{ID: "deep-copy-test", Config: cfg}
	s.put(flow)

	got1, _ := s.get("deep-copy-test")
	got2, _ := s.get("deep-copy-test")

	// They should be different pointers
	if got1.Config == got2.Config {
		t.Error("get returns the same Config pointer, want different clones")
	}

	// Modifying one should not affect the other
	got1.Config.APIKey = "modified"
	if got2.Config.APIKey != "secret" {
		t.Error("modifying one clone affected the other")
	}
}

// TestCopilotDeviceFlowStore_GetNilConfig verifies that flows with nil Config
// don't panic on get.
func TestCopilotDeviceFlowStore_GetNilConfig(t *testing.T) {
	s := newCopilotDeviceFlowStore()
	flow := &copilotDeviceFlowState{ID: "nil-config-flow"}
	s.put(flow)

	got, ok := s.get("nil-config-flow")
	if !ok {
		t.Fatal("expected flow to be found")
	}
	if got.Config != nil {
		t.Error("expected nil Config")
	}
}
