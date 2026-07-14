package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func newTestServerAtWorkspace(t *testing.T, workspace string) *httptest.Server {
	t.Helper()
	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewService()
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      workspace,
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestServerAtWorkspace(t, t.TempDir())
}

func newTestServerWithConfigPath(t *testing.T, workspace, configPath string) *httptest.Server {
	t.Helper()
	return newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath})
}

type testServerOptions struct {
	configPath   string
	copilotOAuth api.GitHubCopilotOAuthConfig
}

func newTestServerWithOptions(t *testing.T, workspace string, opts testServerOptions) *httptest.Server {
	t.Helper()
	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewService()
	configPath := opts.configPath
	if configPath == "" {
		configPath = t.TempDir() + "/config.json"
	}
	cfg := api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
		CopilotOAuth:   opts.copilotOAuth,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func extractCopilotFlowID(t *testing.T, body string) string {
	t.Helper()
	matches := regexp.MustCompile(`/api/providers/github_copilot/device-flow/([^"?]+)`).FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatalf("response missing device-flow status URL: %s", body)
	}
	return matches[1]
}

func newTestServerWithLogger(t *testing.T, logger *slog.Logger) *httptest.Server {
	t.Helper()
	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewService()
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
		Logger:         logger,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func newTestServerWithSessionManager(t *testing.T, workspace string, sessionMgr *session.Manager) *httptest.Server {
	t.Helper()
	skillsSvc := skills.NewService()
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      workspace,
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func newTestServerWithSkillsService(t *testing.T, workspace string, skillsSvc *skills.Service) *httptest.Server {
	t.Helper()
	sessionMgr := session.NewManager(10)
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      workspace,
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func createSessionForBrowser(t *testing.T, server *httptest.Server, client *http.Client) (string, *http.Cookie) {
	t.Helper()
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("create session root request: %v", err)
	}
	defer resp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie missing")
	}

	sessionID := strings.TrimPrefix(resp.Header.Get("Location"), "/sessions/")
	if sessionID == "" {
		t.Fatal("session id missing from redirect")
	}
	return sessionID, browserCookie
}

func writeSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: Test skill " + name + "\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return dir
}

func writeSkillMD(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

type mutableProviderServer struct {
	server *httptest.Server
	mu     sync.RWMutex
	status int
	body   string
}

func newMutableProviderServer(t *testing.T, status int, body string) *mutableProviderServer {
	t.Helper()
	m := &mutableProviderServer{status: status, body: body}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		m.mu.RLock()
		defer m.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(m.status)
		_, _ = w.Write([]byte(m.body))
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mutableProviderServer) URL() string {
	return m.server.URL
}

func (m *mutableProviderServer) SetResponse(status int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
	m.body = body
}

// fakeProviderServer returns an httptest.Server that responds to /v1/models
// with the given status code and body.
func fakeProviderServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPutConfigGitHubCopilotDiscoversPickerEnabledChatModels(t *testing.T) {
	var gotPath string
	gotHeaders := http.Header{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]},{"id":"bad-policy-model","policy":{"state":"disabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]},{"id":"picker-off-model","policy":{"state":"enabled"},"model_picker_enabled":false,"supported_endpoints":["/chat/completions"]},{"id":"responses-only-model","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/responses"]}]}`)
	}))
	t.Cleanup(provider.Close)

	server := newTestServer(t)
	body := fmt.Sprintf(`{"provider":"github_copilot","base_url":%q,"api_key":"ghu-token","model":"gpt-4.1"}`, provider.URL)
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if gotPath != "/models" {
		t.Errorf("model discovery path = %q, want /models", gotPath)
	}
	wantHeaders := map[string]string{
		"Authorization":        "Bearer ghu-token",
		"User-Agent":           "Eitri",
		"X-GitHub-Api-Version": "2026-06-01",
		"Openai-Intent":        "conversation-panel",
		"x-initiator":          "user",
	}
	for name, value := range wantHeaders {
		if got := gotHeaders.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}

	respBody := make([]byte, 16384)
	n, _ := resp.Body.Read(respBody)
	content := string(respBody[:n])
	if !strings.Contains(content, "gpt-4.1") {
		t.Errorf("response HTML missing discovered Copilot model")
	}
	for _, filtered := range []string{"bad-policy-model", "picker-off-model", "responses-only-model"} {
		if strings.Contains(content, filtered) {
			t.Errorf("response HTML contains filtered model %q", filtered)
		}
	}

	modelsResp, err := http.Get(server.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer modelsResp.Body.Close()
	var modelsBody struct {
		Data []string `json:"data"`
	}
	if err := json.NewDecoder(modelsResp.Body).Decode(&modelsBody); err != nil {
		t.Fatal(err)
	}
	if len(modelsBody.Data) != 1 || modelsBody.Data[0] != "gpt-4.1" {
		t.Errorf("/api/models data = %#v, want [gpt-4.1]", modelsBody.Data)
	}
}

func TestPutConfigGitHubCopilotAuthFailure(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(provider.Close)

	server := newTestServer(t)
	body := fmt.Sprintf(`{"provider":"github_copilot","base_url":%q,"api_key":"bad-token"}`, provider.URL)
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /api/config status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}

	var errBody map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBody["error"], "Provider authentication failed") {
		t.Errorf("error = %q, want auth guidance", errBody["error"])
	}
}

func TestPutConfigHTMXGitHubCopilotAuthFailureReturnsVisibleFormError(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(provider.Close)

	server := newTestServer(t)
	form := url.Values{
		"provider": {"github_copilot"},
		"base_url": {provider.URL},
		"api_key":  {"bad-token"},
	}
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config HTMX status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	if !strings.Contains(body, `id="settings-form"`) {
		t.Fatalf("response missing settings form: %s", body)
	}
	if !strings.Contains(body, "Provider authentication failed") {
		t.Errorf("response missing auth guidance: %s", body)
	}
	if !strings.Contains(body, `class="error-toast"`) {
		t.Errorf("response missing visible error toast: %s", body)
	}
	if !strings.Contains(body, `value="bad-token"`) {
		t.Errorf("response should preserve attempted token for correction: %s", body)
	}
}

func TestPutConfigGitHubCopilotMissingToken(t *testing.T) {
	server := newTestServer(t)
	body := `{"provider":"github_copilot","base_url":"https://api.githubcopilot.com"}`
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /api/config status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}
	var errBody map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errBody["error"], "token is required") {
		t.Errorf("error = %q, want missing token", errBody["error"])
	}
}

func TestGitHubCopilotDeviceFlowStartAndPollSuccessSavesTokenAndLoadsModels(t *testing.T) {
	var gotClientID string
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			var reqBody map[string]any
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Fatalf("decode device-code request: %v", err)
			}
			gotClientID, _ = reqBody["client_id"].(string)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"device_code":"device-123","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"access_token":"gho_device_token","token_type":"bearer","scope":"read:user"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer gho_device_token" {
			t.Fatalf("Authorization = %q, want Bearer gho_device_token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer providerSrv.Close()

	configPath := t.TempDir() + "/config.json"
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		configPath: configPath,
		copilotOAuth: api.GitHubCopilotOAuthConfig{
			DeviceCodeURL:  oauth.URL + "/login/device/code",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
	})

	startForm := url.Values{
		"provider": {"github_copilot"},
		"base_url": {providerSrv.URL},
	}
	startReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/providers/github_copilot/device-flow/start", strings.NewReader(startForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	startReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	startReq.Header.Set("HX-Request", "true")
	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("device-flow start status = %d, want %d", startResp.StatusCode, http.StatusOK)
	}
	startBodyBytes, err := io.ReadAll(startResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	startBody := string(startBodyBytes)
	if gotClientID != provider.DefaultGitHubCopilotOAuthClientID {
		t.Fatalf("client_id = %q, want %q", gotClientID, provider.DefaultGitHubCopilotOAuthClientID)
	}
	if !strings.Contains(startBody, "ABCD-EFGH") || !strings.Contains(startBody, "https://github.com/login/device") {
		t.Fatalf("start response missing device-flow instructions: %s", startBody)
	}
	cookies := startResp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("start response missing browser cookie")
	}

	flowID := extractCopilotFlowID(t, startBody)
	pollReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/providers/github_copilot/device-flow/"+flowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	pollReq.AddCookie(cookies[0])
	pollResp, err := http.DefaultClient.Do(pollReq)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResp.Body.Close()
	if pollResp.StatusCode != http.StatusOK {
		t.Fatalf("device-flow poll status = %d, want %d", pollResp.StatusCode, http.StatusOK)
	}
	pollBodyBytes, err := io.ReadAll(pollResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	pollBody := string(pollBodyBytes)
	if !strings.Contains(pollBody, "gpt-4.1") {
		t.Fatalf("poll response missing discovered model: %s", pollBody)
	}
	if !strings.Contains(pollBody, "GitHub Copilot connected") {
		t.Fatalf("poll response missing success status: %s", pollBody)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "github_copilot" {
		t.Fatalf("saved provider = %q, want github_copilot", cfg.Provider)
	}
	if cfg.BaseURL != providerSrv.URL {
		t.Fatalf("saved base_url = %q, want %q", cfg.BaseURL, providerSrv.URL)
	}
	if cfg.APIKey != "gho_device_token" {
		t.Fatalf("saved api_key = %q, want gho_device_token", cfg.APIKey)
	}
	resolvedAuth, err := provider.ResolveAuth(cfg.Provider, cfg.APIKey, cfg.ProviderAuth)
	if err != nil {
		t.Fatalf("ResolveAuth(saved config) error: %v", err)
	}
	if resolvedAuth.APIKey != "gho_device_token" {
		t.Fatalf("resolved APIKey = %q, want gho_device_token", resolvedAuth.APIKey)
	}
}

func TestGetModels_GitHubCopilotUsesProviderAuthState(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	var gotAuth string
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer providerSrv.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{AccessToken: "gho-provider-state"})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.Config{
		Provider:            "github_copilot",
		BaseURL:             providerSrv.URL,
		Model:               "gpt-4.1",
		ProviderAuth:        raw,
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}); err != nil {
		t.Fatal(err)
	}

	server := newTestServerWithConfigPath(t, t.TempDir(), configPath)
	resp, err := http.Get(server.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/models status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	if gotAuth != "Bearer gho-provider-state" {
		t.Fatalf("Authorization = %q, want Bearer gho-provider-state", gotAuth)
	}
}

func TestGetModels_GitHubCopilotRefreshesExpiredProviderAuthState(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	now := time.Now().Add(-2 * time.Hour)
	var gotAuth string
	providerSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer providerSrv.Close()

	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode refresh request: %v", err)
		}
		if body["grant_type"] != "refresh_token" {
			t.Fatalf("grant_type = %q, want refresh_token", body["grant_type"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"gho-refreshed","token_type":"bearer","scope":"read:user","refresh_token":"ghr-next","expires_in":28800,"refresh_token_expires_in":15897600}`)
	}))
	defer oauth.Close()

	raw, err := provider.EncodeGitHubCopilotAuthState(provider.GitHubCopilotAuthState{
		AccessToken:           "gho-expired",
		TokenType:             "bearer",
		Scope:                 "read:user",
		RefreshToken:          "ghr-refresh",
		ExpiresAt:             now.Add(-time.Minute),
		RefreshTokenExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(configPath, &config.Config{
		Provider:            "github_copilot",
		BaseURL:             providerSrv.URL,
		Model:               "gpt-4.1",
		ProviderAuth:        raw,
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}); err != nil {
		t.Fatal(err)
	}

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		configPath: configPath,
		copilotOAuth: api.GitHubCopilotOAuthConfig{
			ClientID:       "client-id",
			AccessTokenURL: oauth.URL,
		},
	})
	resp, err := http.Get(server.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/models status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	if gotAuth != "Bearer gho-refreshed" {
		t.Fatalf("Authorization = %q, want Bearer gho-refreshed", gotAuth)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "gho-refreshed" {
		t.Fatalf("saved api_key = %q, want gho-refreshed", cfg.APIKey)
	}
	resolved, err := provider.ResolveAuth(cfg.Provider, cfg.APIKey, cfg.ProviderAuth)
	if err != nil {
		t.Fatalf("ResolveAuth(saved config) error: %v", err)
	}
	if resolved.APIKey != "gho-refreshed" {
		t.Fatalf("resolved APIKey = %q, want gho-refreshed", resolved.APIKey)
	}
}

func TestGitHubCopilotDeviceFlowStartUsesBuiltInClientID(t *testing.T) {
	var gotClientID string
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device/code" {
			http.NotFound(w, r)
			return
		}
		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode device-code request: %v", err)
		}
		gotClientID, _ = reqBody["client_id"].(string)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"device_code":"device-123","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
	}))
	defer oauth.Close()

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		copilotOAuth: api.GitHubCopilotOAuthConfig{
			DeviceCodeURL: oauth.URL + "/login/device/code",
		},
	})
	startForm := url.Values{
		"provider": {"github_copilot"},
		"base_url": {"https://api.githubcopilot.com"},
	}
	startReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/providers/github_copilot/device-flow/start", strings.NewReader(startForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	startReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	startReq.Header.Set("HX-Request", "true")
	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("device-flow start status = %d, want %d", startResp.StatusCode, http.StatusOK)
	}
	bodyBytes, err := io.ReadAll(startResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	if gotClientID == "" {
		t.Fatal("client_id = empty, want built-in default")
	}
	if !strings.Contains(body, "ABCD-EFGH") {
		t.Fatalf("response missing device-flow instructions: %s", body)
	}
}

func TestGitHubCopilotDeviceFlowPollExpiredTokenShowsErrorAndDoesNotSaveToken(t *testing.T) {
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"device_code":"device-123","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
		case "/login/oauth/access_token":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"error":"expired_token"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer oauth.Close()

	configPath := t.TempDir() + "/config.json"
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		configPath: configPath,
		copilotOAuth: api.GitHubCopilotOAuthConfig{
			ClientID:       "client-123",
			DeviceCodeURL:  oauth.URL + "/login/device/code",
			AccessTokenURL: oauth.URL + "/login/oauth/access_token",
		},
	})

	startForm := url.Values{
		"provider": {"github_copilot"},
		"base_url": {"https://api.githubcopilot.com"},
	}
	startReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/providers/github_copilot/device-flow/start", strings.NewReader(startForm.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	startReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	startReq.Header.Set("HX-Request", "true")
	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatal(err)
	}
	defer startResp.Body.Close()
	startBodyBytes, err := io.ReadAll(startResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	cookies := startResp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("start response missing browser cookie")
	}
	flowID := extractCopilotFlowID(t, string(startBodyBytes))

	pollReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/providers/github_copilot/device-flow/"+flowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	pollReq.AddCookie(cookies[0])
	pollResp, err := http.DefaultClient.Do(pollReq)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResp.Body.Close()
	if pollResp.StatusCode != http.StatusOK {
		t.Fatalf("device-flow poll status = %d, want %d", pollResp.StatusCode, http.StatusOK)
	}
	pollBodyBytes, err := io.ReadAll(pollResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	pollBody := string(pollBodyBytes)
	if !strings.Contains(pollBody, "expired") {
		t.Fatalf("poll response missing expiry guidance: %s", pollBody)
	}
	if strings.Contains(pollBody, "/api/providers/github_copilot/device-flow/"+flowID) {
		t.Fatalf("expired flow should stop polling: %s", pollBody)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "" {
		t.Fatalf("saved api_key = %q, want empty after expired flow", cfg.APIKey)
	}
}

func TestSettingsIncludesGitHubCopilotProvider(t *testing.T) {
	server := newTestServer(t)
	resp, err := http.Get(server.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	contentBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(contentBytes)
	if !strings.Contains(content, `value="github_copilot"`) || !strings.Contains(content, "GitHub Copilot") {
		t.Errorf("settings HTML missing GitHub Copilot provider option")
	}
	if !strings.Contains(content, "GitHub Copilot: bearer token required") {
		t.Errorf("settings HTML missing Copilot token hint")
	}
	if !strings.Contains(content, "Authenticate with GitHub") {
		t.Errorf("settings HTML missing GitHub device-flow action")
	}
}

func TestPutConfigExistingProvidersUseProfileModelDiscovery(t *testing.T) {
	for _, tc := range []struct {
		providerID string
		apiKey     string
		wantAuth   string
	}{
		{providerID: "opencode_go", apiKey: "sk-opencode", wantAuth: "Bearer sk-opencode"},
		{providerID: "custom_openai", apiKey: "", wantAuth: ""},
	} {
		t.Run(tc.providerID, func(t *testing.T) {
			var gotPath string
			var gotAuth string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-4"}]}`)
			}))
			t.Cleanup(provider.Close)

			server := newTestServer(t)
			body := fmt.Sprintf(`{"provider":%q,"base_url":%q,"api_key":%q,"model":"gpt-4"}`, tc.providerID, provider.URL+"/v1", tc.apiKey)
			req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("PUT /api/config status = %d, want %d", resp.StatusCode, http.StatusOK)
			}
			if gotPath != "/v1/models" {
				t.Errorf("model discovery path = %q, want /v1/models", gotPath)
			}
			if gotAuth != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", gotAuth, tc.wantAuth)
			}
		})
	}
}

func TestHealthEndpoint(t *testing.T) {
	server := newTestServer(t)

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf(`body["status"] = %q, want "ok"`, body["status"])
	}
}

func TestRequestBodyLimitRejectsOversizedRequests(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)
	client := noRedirectClient()

	rootResp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie missing")
	}
	sessionID := strings.TrimPrefix(rootResp.Header.Get("Location"), "/sessions/")
	large := strings.Repeat("x", 1024*1024+1)

	for _, tc := range []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		withCookie  bool
	}{
		{
			name:        "config put",
			method:      http.MethodPut,
			path:        "/api/config",
			contentType: "application/json",
			body:        `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test","model":"` + large + `"}`,
		},
		{
			name:        "chat post",
			method:      http.MethodPost,
			path:        "/api/sessions/" + sessionID + "/chat",
			contentType: "application/x-www-form-urlencoded",
			body:        "message=" + large,
			withCookie:  true,
		},
		{
			name:        "render component post",
			method:      http.MethodPost,
			path:        "/api/sessions/" + sessionID + "/render/component",
			contentType: "application/json",
			body:        `{"name":"MermaidDiagram","data":{"code":"` + large + `"}}`,
			withCookie:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tc.contentType)
			if tc.withCookie {
				req.AddCookie(browserCookie)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusRequestEntityTooLarge {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("%s status = %d, want %d; body=%s", tc.path, resp.StatusCode, http.StatusRequestEntityTooLarge, body)
			}
		})
	}
}

func TestRequestLoggingIncludesMethodPathStatusDurationAndSessionID(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServerWithLogger(t, logger)
	client := noRedirectClient()

	rootResp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie missing")
	}
	sessionPath := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(sessionPath, "/sessions/")

	req, err := http.NewRequest(http.MethodGet, server.URL+sessionPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", sessionPath, resp.StatusCode, http.StatusOK)
	}

	wants := []string{
		`"method":"GET"`,
		`"path":"` + sessionPath + `"`,
		fmt.Sprintf(`"status":%d`, http.StatusOK),
		`"session_id":"` + sessionID + `"`,
		`"duration_ms":`,
	}

	deadline := time.Now().Add(1 * time.Second)
	for {
		logOutput := logs.String()
		missing := ""
		for _, want := range wants {
			if !strings.Contains(logOutput, want) {
				missing = want
				break
			}
		}
		if missing == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log output missing %q:\n%s", missing, logOutput)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// noRedirectClient returns an http.Client that does not follow redirects.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func TestRootRedirectCreatesSession(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// First request should redirect to /sessions/{id}
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("GET / status = %d, want %d (redirect)", resp.StatusCode, http.StatusFound)
	}

	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/sessions/") {
		t.Errorf("Location = %q, want /sessions/{id}", loc)
	}

	// Check browser_id cookie was set
	cookies := resp.Cookies()
	var browserID string
	for _, c := range cookies {
		if c.Name == "browser_id" {
			browserID = c.Value
			break
		}
	}
	if browserID == "" {
		t.Error("browser_id cookie was not set")
	}
}

func TestRootRedirectExistingSession(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Make initial request to get browser_id cookie
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Extract cookie
	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("no browser_id cookie set")
	}

	// Follow redirect to get session page
	loc := resp.Header.Get("Location")
	req, _ := http.NewRequest("GET", server.URL+loc, nil)
	req.AddCookie(browserCookie)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET %s status = %d, want %d", loc, resp2.StatusCode, http.StatusOK)
	}

	// Second root request should redirect to the same session
	req3, _ := http.NewRequest("GET", server.URL+"/", nil)
	req3.AddCookie(browserCookie)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()

	if resp3.StatusCode != http.StatusFound {
		t.Errorf("second GET / status = %d, want redirect", resp3.StatusCode)
	}
	loc2 := resp3.Header.Get("Location")
	if loc2 != loc {
		t.Errorf("second redirect Location = %q, want %q (same session)", loc2, loc)
	}
}

func TestSessionPageShowsWorkspaceIndicator(t *testing.T) {
	workspace := t.TempDir()
	server := newTestServerAtWorkspace(t, workspace)
	client := noRedirectClient()

	rootResp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()

	loc := rootResp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/sessions/") {
		t.Fatalf("Location = %q, want /sessions/{id}", loc)
	}

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("browser_id cookie missing")
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.Contains(content, "Workspace: "+workspace) {
		t.Fatalf("session page missing workspace indicator for %q", workspace)
	}
}

func TestHealthMethodNotAllowed(t *testing.T) {
	server := newTestServer(t)

	resp, err := http.Post(server.URL+"/health", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /health status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestSettingsPage(t *testing.T) {
	workspace := t.TempDir()
	server := newTestServerAtWorkspace(t, workspace)

	resp, err := http.Get(server.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /settings status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)

	// Check for expected form elements
	checks := []string{"provider", "api_key", "base_url", "model", "system_prompt", "session_timeout", "command_timeout", "max_turns"}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("settings page missing %q", c)
		}
	}

	for _, required := range []string{
		"Workspace: " + workspace,
		`href="/"`,
		`href="/settings"`,
		`href="/skills"`,
		`/static/prism-core.min.js`,
		`/static/prism-go.min.js`,
		`/static/prism.min.css`,
		`/static/katex.min.js`,
		`/static/katex-auto-render.min.js`,
		`/static/katex.min.css`,
		`/static/mermaid.min.js`,
		`/static/eitri-stream.js`,
		`/static/eitri-composer.js`,
		`/static/eitri-renderers.js`,
		`/static/eitri-mermaid.js`,
		`hx-ext="head-support"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("settings page missing %q", required)
		}
	}
}

func TestSessionPageRendersAssistantMarkdownAndRichAssets(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10)
	sess, err := sessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "show rich output"})
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: strings.Join([]string{
		"**bold** answer",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"```mermaid",
		"graph TD; A-->B;",
		"```",
	}, "\n")})

	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	req, err := http.NewRequest("GET", server.URL+"/sessions/"+sess.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "browser-1"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /sessions/{id} status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)

	for _, want := range []string{
		`<strong>bold</strong> answer`,
		`class="code-btn copy-btn"`,
		`<pre class="mermaid">graph TD; A--&gt;B;`,
		`/static/prism-core.min.js`,
		`/static/prism.min.css`,
		`/static/katex.min.js`,
		`/static/katex.min.css`,
		`/static/katex-auto-render.min.js`,
		`/static/mermaid.min.js`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("session page missing %q\nbody: %s", want, content)
		}
	}
}

func TestGetConfigJSON(t *testing.T) {
	server := newTestServer(t)

	resp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/config status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if body["provider"] != "opencode_go" {
		t.Errorf("provider = %q, want %q", body["provider"], "opencode_go")
	}
	// API key should be empty (no key set)
	if body["api_key"] != "" {
		t.Errorf("api_key = %q, want empty string", body["api_key"])
	}
	if body["system_prompt"] != "" {
		t.Errorf("system_prompt = %q, want empty string", body["system_prompt"])
	}
	if _, ok := body["provider_auth"]; ok {
		t.Errorf("provider_auth should be omitted from GET /api/config")
	}
}

func TestGetConfigHTMLFragment(t *testing.T) {
	server := newTestServer(t)

	req, err := http.NewRequest("GET", server.URL+"/api/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api config HX-Request status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "provider") {
		t.Errorf("HTML fragment missing provider field")
	}
}

func TestPutConfigValid(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test","model":"gpt-4"}`
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("PUT /api/config status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

func TestPutConfigPersistsSystemPrompt(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"sk-test","model":"gpt-4","system_prompt":"Speak like blacksmith."}`)

	cfgData := getConfigJSON(t, server)
	if got := cfgData["system_prompt"]; got != "Speak like blacksmith." {
		t.Fatalf("system_prompt = %v, want custom prompt", got)
	}

	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"sk-test","model":"gpt-4","system_prompt":""}`)

	cfgData = getConfigJSON(t, server)
	if got := cfgData["system_prompt"]; got != "" {
		t.Fatalf("system_prompt after clear = %v, want empty string", got)
	}
}

func TestPutConfigWithoutModelPopulatesDiscoveredModels(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-4.1"}]}`)
	server := newTestServer(t)

	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test"}`
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config missing model status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	contentBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(contentBytes)
	for _, model := range []string{"gpt-4", "gpt-4.1"} {
		if !strings.Contains(content, model) {
			t.Errorf("response HTML missing discovered model %q", model)
		}
	}

	cfgResp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer cfgResp.Body.Close()
	var cfgData map[string]interface{}
	if err := json.NewDecoder(cfgResp.Body).Decode(&cfgData); err != nil {
		t.Fatal(err)
	}
	if got := cfgData["provider"]; got != "custom_openai" {
		t.Errorf("provider = %v, want custom_openai", got)
	}
	if got := cfgData["model"]; got != "" {
		t.Errorf("model = %v, want empty string until user selects one", got)
	}
}

func TestPutConfigRejectsUnavailableSelectedModel(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test","model":"missing-model"}`
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /api/config stale model status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}

	var bodyJSON map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&bodyJSON); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(bodyJSON["error"], "missing-model") {
		t.Errorf("error = %q, want stale model name", bodyJSON["error"])
	}
}

func TestPutConfigInvalidProvider(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[]}`)
	server := newTestServer(t)

	// Invalid provider value should fail before model discovery
	body := `{"provider":"invalid","base_url":"` + provider.URL + `"}`
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("PUT /api config invalid provider status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}
}

func TestPutConfigDiscoveryFailure(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	server := newTestServer(t)

	// Provider returns 401 on model discovery => save should be rejected
	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-bad"}`
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("PUT /api config discovery fail status = %d, want %d", resp.StatusCode, http.StatusUnprocessableEntity)
	}
}

func TestPutConfig_ClearAPIKey(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	// First save a config with an API key
	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test-key","model":"gpt-4"}`
	req, _ := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()

	// Now clear it
	body = `{"clear_api_key":"true","provider":"custom_openai","base_url":"` + provider.URL + `"}`
	req, _ = http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("PUT clear_api_key status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Verify the key was cleared by reading config
	getResp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var cfgData map[string]interface{}
	json.NewDecoder(getResp.Body).Decode(&cfgData)
	if key, ok := cfgData["api_key"].(string); ok && key != "" {
		t.Errorf("api_key = %q after clear, want empty", key)
	}
}

func TestPutConfigFormURLEncoded(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	// Submit as URL-encoded form data (HTMX default)
	form := url.Values{}
	form.Set("provider", "custom_openai")
	form.Set("base_url", provider.URL)
	form.Set("api_key", "sk-form-test")
	form.Set("session_timeout", "120")
	form.Set("command_timeout", "30")
	form.Set("max_turns", "10")
	form.Set("context_window_tokens", "128000")
	form.Set("model", "gpt-4")

	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("PUT /api/config form-encoded status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Verify models appear in the response HTML
	body := make([]byte, 16384)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])
	if !strings.Contains(content, "gpt-4") {
		t.Errorf("response HTML missing discovered model gpt-4")
	}
	if !strings.Contains(content, "gpt-3.5-turbo") {
		t.Errorf("response HTML missing discovered model gpt-3.5-turbo")
	}
}

func TestPutConfigFormPreservesAPIKeyWhenEmpty(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	// First save with an API key via JSON
	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-existing","model":"gpt-4"}`
	putJSONConfig(t, server, body)

	// Now submit form with empty api_key (no clear_key checkbox — real HTML behavior)
	form := url.Values{}
	form.Set("provider", "custom_openai")
	form.Set("base_url", provider.URL)
	form.Set("api_key", "") // empty — should preserve existing
	// NOTE: clear_api_key is NOT set — unchecked checkboxes aren't submitted

	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("PUT preserve api_key status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Verify the key was preserved
	cfgData := getConfigJSON(t, server)
	if key, ok := cfgData["api_key"].(string); !ok || key == "" {
		t.Errorf("api_key = %q after empty form submit, want preserved", key)
	}
}

func TestPutConfigJSONPreservesAPIKeyWhenEmpty(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"sk-existing-secret","model":"gpt-4"}`)
	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"","model":"gpt-4"}`)

	cfgData := getConfigJSON(t, server)
	if key, ok := cfgData["api_key"].(string); !ok || key == "" {
		t.Errorf("api_key = %q after empty JSON submit, want preserved", key)
	}
}

func TestPutConfigJSONProviderSwitchPreservesExplicitModel(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"sk-existing","model":"gpt-4"}`)
	putJSONConfig(t, server, `{"provider":"opencode_go","base_url":"`+provider.URL+`/v1","api_key":"sk-opencode","model":"gpt-4"}`)

	cfgData := getConfigJSON(t, server)
	if cfgData["provider"] != "opencode_go" {
		t.Errorf("provider = %q, want opencode_go", cfgData["provider"])
	}
	if cfgData["model"] != "gpt-4" {
		t.Errorf("model = %q after provider switch JSON, want gpt-4", cfgData["model"])
	}
}

func TestPutConfigFormProviderSwitchPreservesExplicitModel(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	server := newTestServer(t)

	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"sk-existing","model":"gpt-4"}`)

	form := url.Values{}
	form.Set("provider", "opencode_go")
	form.Set("base_url", provider.URL+"/v1")
	form.Set("api_key", "sk-opencode")
	form.Set("model", "gpt-4")
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /api/config form provider switch status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	cfgData := getConfigJSON(t, server)
	if cfgData["provider"] != "opencode_go" {
		t.Errorf("provider = %q, want opencode_go", cfgData["provider"])
	}
	if cfgData["model"] != "gpt-4" {
		t.Errorf("model = %q after provider switch form, want gpt-4", cfgData["model"])
	}
}

func TestSettingsPagePopulatesDiscoveredModelsOnDirectNavigation(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-4.1"}]}`)
	workspace := t.TempDir()
	configPath := workspace + "/config.json"
	server := newTestServerWithConfigPath(t, workspace, configPath)

	if err := config.Save(configPath, &config.Config{
		Provider:            "custom_openai",
		APIKey:              "sk-test",
		BaseURL:             provider.URL,
		Model:               "gpt-4",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}

	resp, err := http.Get(server.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.Contains(content, `option value="gpt-4"`) {
		t.Errorf("settings page missing discovered model gpt-4")
	}
	if !strings.Contains(content, `option value="gpt-4.1"`) {
		t.Errorf("settings page missing discovered model gpt-4.1")
	}
}

func TestGetSessionPageShowsSetupBannerWhenSavedModelUnavailable(t *testing.T) {
	provider := newMutableProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	workspace := t.TempDir()
	configPath := workspace + "/config.json"
	server := newTestServerWithConfigPath(t, workspace, configPath)
	client := noRedirectClient()

	if err := config.Save(configPath, &config.Config{
		Provider:            "custom_openai",
		APIKey:              "sk-test",
		BaseURL:             provider.URL(),
		Model:               "gpt-4",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}); err != nil {
		t.Fatalf("Save config: %v", err)
	}
	provider.SetResponse(http.StatusOK, `{"object":"list","data":[{"id":"other-model"}]}`)

	rootResp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()

	loc := rootResp.Header.Get("Location")
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("missing browser cookie")
	}

	pageReq, _ := http.NewRequest(http.MethodGet, server.URL+loc, nil)
	pageReq.AddCookie(browserCookie)
	pageResp, err := client.Do(pageReq)
	if err != nil {
		t.Fatal(err)
	}
	defer pageResp.Body.Close()

	body, err := io.ReadAll(pageResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.Contains(content, `id="setup-banner"`) {
		t.Fatalf("session page missing setup banner when saved model stale")
	}
	if !strings.Contains(content, `id="chat-input"`) || !strings.Contains(content, `disabled`) {
		t.Errorf("session page should render disabled chat input when config invalid")
	}
}

func TestChatRejectsUnavailableSavedModel(t *testing.T) {
	provider := newMutableProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)
	h := newManagedTestServerWithRuns(t)
	client := noRedirectClient()

	putJSONConfig(t, h.server, `{"provider":"custom_openai","base_url":"`+provider.URL()+`","api_key":"sk-test","model":"gpt-4"}`)
	provider.SetResponse(http.StatusOK, `{"object":"list","data":[{"id":"other-model"}]}`)

	rootResp, err := client.Get(h.server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer rootResp.Body.Close()
	loc := rootResp.Header.Get("Location")

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("missing browser cookie")
	}

	chatReq, _ := http.NewRequest(http.MethodPost, h.server.URL+"/api"+loc+"/chat", strings.NewReader("message=hello"))
	chatReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	chatReq.Header.Set("HX-Request", "true")
	chatReq.AddCookie(browserCookie)
	chatResp, err := client.Do(chatReq)
	if err != nil {
		t.Fatal(err)
	}
	defer chatResp.Body.Close()

	if chatResp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("chat status = %d, want %d", chatResp.StatusCode, http.StatusUnprocessableEntity)
	}

	body, err := io.ReadAll(chatResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "selected model") {
		t.Errorf("chat error = %q, want stale model message", string(body))
	}
	if h.runMgr.ActiveRun(strings.TrimPrefix(loc, "/sessions/")) != nil {
		t.Fatal("chat started run despite stale saved model")
	}
}

func putJSONConfig(t *testing.T, server *httptest.Server, body string) {
	t.Helper()
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		content, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT /api/config status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, content)
	}
}

func getConfigJSON(t *testing.T, server *httptest.Server) map[string]interface{} {
	t.Helper()
	getResp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var cfgData map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&cfgData); err != nil {
		t.Fatal(err)
	}
	return cfgData
}

func TestGetConfigHTMLFragmentWithModels(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"claude-3"}]}`)
	server := newTestServer(t)

	putJSONConfig(t, server, `{"provider":"custom_openai","base_url":"`+provider.URL+`","api_key":"sk-test","model":"claude-3"}`)

	// Verify via JSON that model was returned in models endpoint
	modelsResp, err := http.Get(fmt.Sprintf("%s/api/models", server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer modelsResp.Body.Close()

	var modelsData map[string]interface{}
	if err := json.NewDecoder(modelsResp.Body).Decode(&modelsData); err != nil {
		t.Fatal(err)
	}
	data, ok := modelsData["data"]
	if !ok {
		t.Errorf("/api/models response missing 'data' field")
	}
	modelsArr, ok := data.([]interface{})
	if !ok || len(modelsArr) == 0 {
		t.Errorf("/api/models data is empty or not an array: %v", data)
	}
}

func TestGetAPIAndConfigModelsEndpoint(t *testing.T) {
	server := newTestServer(t)

	// Should return 412 Precondition Failed when no base URL
	resp, err := http.Get(server.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("GET /api/models without config status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestCreateSessionEndpoint(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// First get a browser_id
	rootResp, err := client.Get(server.URL + "/")
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
		t.Fatal("no browser_id cookie")
	}

	// Create a new session via POST
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions", nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Non-HTMX request should get redirect
	if resp.StatusCode != http.StatusFound {
		t.Errorf("POST /api/sessions status = %d, want %d", resp.StatusCode, http.StatusFound)
	}

	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/sessions/") {
		t.Errorf("redirect Location = %q, want /sessions/{id}", loc)
	}
}

func TestDeleteSessionEndpoint(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Get browser_id and session
	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	if browserCookie == nil {
		t.Fatal("no browser_id cookie")
	}

	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")
	if sessionID == "" {
		t.Fatal("no session ID from redirect")
	}

	// Delete session
	req, _ := http.NewRequest("DELETE", server.URL+"/api/sessions/"+sessionID, nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should redirect (create new session after delete)
	if resp.StatusCode != http.StatusFound {
		t.Errorf("DELETE /api/sessions/%s status = %d, want %d", sessionID, resp.StatusCode, http.StatusFound)
	}

	// Verify session no longer accessible (or redirects to new one)
	// Follow new session redirect
	newLoc := resp.Header.Get("Location")
	if !strings.HasPrefix(newLoc, "/sessions/") {
		t.Errorf("redirect Location = %q, want /sessions/{id}", newLoc)
	}

	// The old session should be gone - accessing it should redirect
	req2, _ := http.NewRequest("GET", server.URL+"/sessions/"+sessionID, nil)
	req2.AddCookie(browserCookie)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusFound {
		t.Errorf("GET deleted session status = %d, want redirect", resp2.StatusCode)
	}
}

func TestGetSessionPage(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Get a session
	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	loc := rootResp.Header.Get("Location")

	// Follow redirect to session page
	req, _ := http.NewRequest("GET", server.URL+loc, nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s status = %d, want %d", loc, resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body := make([]byte, 8192)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	// Should contain chat view elements
	checks := []string{"chat-view", "messages", "composer"}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("session page missing %q", c)
		}
	}
}

func TestStaleSessionRedirect(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Try accessing a non-existent session (no browser cookie)
	req, _ := http.NewRequest("GET", server.URL+"/sessions/nonexistent-id", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should redirect to root
	if resp.StatusCode != http.StatusFound {
		t.Errorf("stale session status = %d, want redirect (found)", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

func TestSessionCapReached(t *testing.T) {
	// Use a manager with cap of 1
	sessionMgr := session.NewManager(1)
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	client := noRedirectClient()

	// First request creates session
	resp, _ := client.Get(server.URL + "/")
	resp.Body.Close()

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	// Second request should hit cap on new session
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions", nil)
	req.AddCookie(browserCookie)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusTooManyRequests {
		t.Errorf("POST /api/sessions at cap status = %d, want %d", resp2.StatusCode, http.StatusTooManyRequests)
	}
}

func TestSkillsEndpoint(t *testing.T) {
	workspace := t.TempDir()
	server := newTestServerAtWorkspace(t, workspace)

	// Test GET /skills returns a page
	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)

	for _, required := range []string{
		"Agent Skills",
		"Workspace: " + workspace,
		`/static/prism-core.min.js`,
		`/static/prism-go.min.js`,
		`/static/prism.min.css`,
		`/static/katex.min.js`,
		`/static/katex-auto-render.min.js`,
		`/static/katex.min.css`,
		`/static/mermaid.min.js`,
		`/static/eitri-stream.js`,
		`/static/eitri-composer.js`,
		`/static/eitri-renderers.js`,
		`/static/eitri-mermaid.js`,
		`hx-ext="head-support"`,
	} {
		if !strings.Contains(content, required) {
			t.Errorf("skills page missing %q", required)
		}
	}
}

func TestAPISkillsEndpoint(t *testing.T) {
	server := newTestServer(t)

	// GET /api/skills returns JSON
	resp, err := http.Get(server.URL + "/api/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /api/skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if _, ok := body["skills"]; !ok {
		t.Errorf("response missing 'skills' field")
	}
}

func TestSkillsEndpointRefreshesRegistry(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)

	writeSkill(t, filepath.Join(rootDir, "fresh-skill"), "fresh-skill", "# Fresh")

	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "fresh-skill") {
		t.Fatalf("GET /skills body missing refreshed skill: %s", string(body))
	}
}

func TestSkillsEndpointShowsInvalidSkillDiagnostics(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	writeSkillMD(t, filepath.Join(rootDir, "broken-skill"), "---\ndescription: missing name\n---\nBody")
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)

	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	for _, want := range []string{"Invalid Skills", "broken-skill", "name field missing or empty"} {
		if !strings.Contains(content, want) {
			t.Fatalf("GET /skills missing %q in body: %s", want, content)
		}
	}
}

func TestCompleteSkillsRefreshesRegistry(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	client := noRedirectClient()

	sessionID, browserCookie := createSessionForBrowser(t, server, client)
	writeSkill(t, filepath.Join(rootDir, "code-review"), "code-review", "# Review")

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/skills?q=code", nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /complete/skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Name != "code-review" {
		t.Fatalf("completion items = %#v, want refreshed code-review", body.Items)
	}
}

func TestActivateSessionSkillUsesRefreshedRegistry(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	client := noRedirectClient()

	sessionID, browserCookie := createSessionForBrowser(t, server, client)
	writeSkill(t, filepath.Join(rootDir, "code-review"), "code-review", "# Review")

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/skills/code-review/activate", nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("activate status = %d, want %d, body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "code-review") {
		t.Fatalf("activate response missing refreshed skill chip: %s", string(body))
	}
}

func TestChatSlashActivationRefreshesRegistryAtRunStart(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)
	client := noRedirectClient()

	sessionID, browserCookie := createSessionForBrowser(t, server, client)
	writeSkill(t, filepath.Join(rootDir, "code-review"), "code-review", "# Review")

	form := url.Values{"message": {"/code-review"}}
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/chat", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("slash activation status = %d, want %d, body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "code-review") {
		t.Fatalf("slash activation response missing refreshed skill chip: %s", string(body))
	}
}

func TestOwnershipMismatch(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Get browser_id and session
	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()

	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")
	if sessionID == "" {
		t.Fatal("no session ID")
	}

	// Try accessing with a different browser_id
	req, _ := http.NewRequest("GET", server.URL+"/sessions/"+sessionID, nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "different-browser"})
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	// Should return 404 (ownership mismatch)
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("ownership mismatch status = %d, want %d", resp2.StatusCode, http.StatusNotFound)
	}
}

func TestRenderComponent_MermaidDiagram(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Get a session with browser_id
	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	// POST to render/component as form-encoded
	form := url.Values{}
	form.Set("name", "MermaidDiagram")
	form.Set("data", `{"code":"graph TD; A-->B;"}`)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render/component", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("render MermaidDiagram status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body := make([]byte, 2048)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "mermaid") {
		t.Errorf("MermaidDiagram response missing 'mermaid' class, got: %s", content[:100])
	}
	// Templ escapes > to &gt;
	if !strings.Contains(content, "A--&gt;B;") && !strings.Contains(content, "A-->B;") {
		t.Errorf("MermaidDiagram response missing code content, got: %s", content[50:150])
	}
}

func TestRenderComponent_QuickReplies(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	form := url.Values{}
	form.Set("name", "QuickReplies")
	form.Set("data", `{"options":["Summarize","Make shorter"]}`)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render/component", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("render QuickReplies status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body := make([]byte, 2048)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "Summarize") {
		t.Errorf("QuickReplies response missing 'Summarize', got: %s", content[:100])
	}
	if !strings.Contains(content, "Make shorter") {
		t.Errorf("QuickReplies response missing 'Make shorter', got: %s", content[:100])
	}
	if !strings.Contains(content, "quick-reply") {
		t.Errorf("QuickReplies response missing quick-reply class, got: %s", content[:100])
	}
}

func TestRenderComponent_DiffCard(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	form := url.Values{}
	form.Set("name", "DiffCard")
	form.Set("data", `{"old":"old code","new":"new code","lang":"go"}`)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render/component", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("render DiffCard status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body := make([]byte, 2048)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "diff-card") {
		t.Errorf("DiffCard response missing 'diff-card' class, got: %s", content[:100])
	}
	if !strings.Contains(content, "old code") {
		t.Errorf("DiffCard response missing old code, got: %s", content[:100])
	}
	if !strings.Contains(content, "new code") {
		t.Errorf("DiffCard response missing new code, got: %s", content[:100])
	}
}

func TestRenderComponent_Unknown(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	form := url.Values{}
	form.Set("name", "UnknownComponent")
	form.Set("data", `{}`)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render/component", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown component status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestCompleteFiles(t *testing.T) {
	// Create a temp workspace with test files
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewService()
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      workspace,
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	client := noRedirectClient()

	// Create some files in workspace
	os.WriteFile(workspace+"/main.go", []byte("package main"), 0644)
	os.WriteFile(workspace+"/README.md", []byte("# Readme"), 0644)
	os.MkdirAll(workspace+"/internal/api", 0755)
	os.WriteFile(workspace+"/internal/api/handler.go", []byte("package api"), 0644)

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	// Test with empty prefix — should list workspace root
	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/files", nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /complete/files status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Items []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Should return workspace entries (at least main.go)
	if len(body.Items) == 0 {
		t.Errorf("expected at least one file completion item, got 0")
	}

	// Test prefix filtering — 'main' should match main.go
	req2, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/files?q=main", nil)
	req2.AddCookie(browserCookie)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	var body2 struct {
		Items []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	json.NewDecoder(resp2.Body).Decode(&body2)

	if len(body2.Items) != 1 || body2.Items[0].Path != "main.go" {
		t.Errorf("prefix 'main' expected 1 item 'main.go', got %d items: %+v", len(body2.Items), body2.Items)
	}

	// Test subdirectory navigation
	req3, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/files?q=internal/", nil)
	req3.AddCookie(browserCookie)
	resp3, err := client.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()

	var body3 struct {
		Items []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	json.NewDecoder(resp3.Body).Decode(&body3)

	if len(body3.Items) == 0 {
		t.Errorf("expected items in subdirectory, got 0")
	}
}

func TestCompleteFiles_PreservesWorkspaceRelativeNestedPaths(t *testing.T) {
	workspace := t.TempDir()
	server := newTestServerAtWorkspace(t, workspace)
	client := noRedirectClient()

	if err := os.MkdirAll(workspace+"/alpha", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace+"/alpha/nested.txt", []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	sessionID := strings.TrimPrefix(rootResp.Header.Get("Location"), "/sessions/")

	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/files?q=alpha/", nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var body struct {
		Items []struct {
			Path string `json:"path"`
			Kind string `json:"kind"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Items) != 1 {
		t.Fatalf("nested completion item count = %d, want 1", len(body.Items))
	}
	if body.Items[0].Path != "alpha/nested.txt" {
		t.Fatalf("nested completion path = %q, want %q", body.Items[0].Path, "alpha/nested.txt")
	}
	if body.Items[0].Kind != "file" {
		t.Fatalf("nested completion kind = %q, want %q", body.Items[0].Kind, "file")
	}
	if !strings.HasPrefix(body.Items[0].Path, "alpha/") {
		t.Fatalf("nested completion lost directory prefix: %+v", body.Items[0])
	}
}

func TestCompleteFiles_RejectEscape(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	// Test with ../ escape
	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/files?q=../", nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("complete/files with escape status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Items []interface{} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Should return empty items for escape attempts
	if len(body.Items) != 0 {
		t.Errorf("expected empty items for ../ escape, got %d", len(body.Items))
	}
}

func TestCompleteSkills(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	rootResp, _ := client.Get(server.URL + "/")
	rootResp.Body.Close()
	var browserCookie *http.Cookie
	for _, c := range rootResp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}
	loc := rootResp.Header.Get("Location")
	sessionID := strings.TrimPrefix(loc, "/sessions/")

	// Test with empty query
	req, _ := http.NewRequest("GET", server.URL+"/api/sessions/"+sessionID+"/complete/skills", nil)
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /complete/skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Should return at least some skills
	if len(body.Items) == 0 {
		t.Errorf("expected at least one skill completion item, got 0")
	}
}

func TestRenderComponent_OwnershipMismatch(t *testing.T) {
	server := newTestServer(t)

	// Use no browser cookie — should get 404
	form := url.Values{}
	form.Set("name", "MermaidDiagram")
	form.Set("data", `{}`)

	resp, err := http.PostForm(server.URL+"/api/sessions/nonexistent/render/component", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// No browser_id cookie means session not found
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("render component no auth status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}
