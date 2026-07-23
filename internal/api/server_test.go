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
	"github.com/glemsom/eitri/internal/debug"
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
		Version:        "dev",
		StartTime:      time.Now(),
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
	configPath     string
	copilotOAuth   api.GitHubCopilotOAuthConfig
	sessionManager *session.Manager
	skillsService  *skills.Service
	debugRecorder  *debug.Recorder
}

func newTestServerWithOptions(t *testing.T, workspace string, opts testServerOptions) *httptest.Server {
	t.Helper()
	sessionMgr := opts.sessionManager
	if sessionMgr == nil {
		sessionMgr = session.NewManager(10)
	}
	skillsSvc := opts.skillsService
	if skillsSvc == nil {
		skillsSvc = skills.NewService()
	}
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
		DebugRecorder:  opts.debugRecorder,
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
		"User-Agent":           "GithubCopilot/1.100.0",
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
	if len(cfg.ProviderAuth) == 0 {
		t.Fatal("saved provider_auth = empty, want persisted auth state")
	}
}

func TestGitHubCopilotDeviceFlowSuccessPersistsAuthAfterDiscoverySeam(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
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
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Fatalf("config saved before discovery seam; err=%v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-4.1","policy":{"state":"enabled"},"model_picker_enabled":true,"supported_endpoints":["/chat/completions"]}]}`)
	}))
	defer providerSrv.Close()

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
	startBodyBytes, err := io.ReadAll(startResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	flowID := extractCopilotFlowID(t, string(startBodyBytes))

	pollReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/providers/github_copilot/device-flow/"+flowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range startResp.Cookies() {
		pollReq.AddCookie(c)
	}
	pollResp, err := http.DefaultClient.Do(pollReq)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResp.Body.Close()
	if pollResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pollResp.Body)
		t.Fatalf("device-flow poll status = %d, want %d; body=%s", pollResp.StatusCode, http.StatusOK, string(body))
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "gho_device_token" {
		t.Fatalf("saved api_key = %q, want gho_device_token", cfg.APIKey)
	}
	if cfg.BaseURL != providerSrv.URL {
		t.Fatalf("saved base_url = %q, want %q", cfg.BaseURL, providerSrv.URL)
	}
	if len(cfg.ProviderAuth) == 0 {
		t.Fatal("saved provider_auth = empty, want persisted auth state")
	}
}

func TestGitHubCopilotDeviceFlowDiscoveryFailureStillSavesToken(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
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
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer providerSrv.Close()

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
	startBodyBytes, err := io.ReadAll(startResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	flowID := extractCopilotFlowID(t, string(startBodyBytes))

	pollReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/providers/github_copilot/device-flow/"+flowID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range startResp.Cookies() {
		pollReq.AddCookie(c)
	}
	pollResp, err := http.DefaultClient.Do(pollReq)
	if err != nil {
		t.Fatal(err)
	}
	defer pollResp.Body.Close()
	if pollResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(pollResp.Body)
		t.Fatalf("device-flow poll status = %d, want %d; body=%s", pollResp.StatusCode, http.StatusOK, string(body))
	}
	pollBodyBytes, err := io.ReadAll(pollResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	pollBody := string(pollBodyBytes)
	if !strings.Contains(pollBody, "GitHub Copilot connected. Token saved.") {
		t.Fatalf("poll response missing token-saved notice: %s", pollBody)
	}
	if !strings.Contains(pollBody, "Model discovery failed: provider returned HTTP 502") {
		t.Fatalf("poll response missing discovery error: %s", pollBody)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "gho_device_token" {
		t.Fatalf("saved api_key = %q, want gho_device_token", cfg.APIKey)
	}
	if len(cfg.ProviderAuth) == 0 {
		t.Fatal("saved provider_auth = empty, want persisted auth state")
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
	if len(cfg.ProviderAuth) == 0 {
		t.Fatal("saved provider_auth = empty, want persisted auth state")
	}
}

func TestGetConfigHTMXRefreshesExpiredGitHubCopilotAuthAndShowsModels(t *testing.T) {
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

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/config", nil)
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
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/config HX status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, string(body))
	}
	if gotAuth != "Bearer gho-refreshed" {
		t.Fatalf("Authorization = %q, want Bearer gho-refreshed", gotAuth)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.Contains(content, `option value="gpt-4.1"`) {
		t.Fatalf("settings fragment missing refreshed model option: %s", content)
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "gho-refreshed" {
		t.Fatalf("saved api_key = %q, want gho-refreshed", cfg.APIKey)
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
			name:        "render post",
			method:      http.MethodPost,
			path:        "/api/sessions/" + sessionID + "/render",
			contentType: "application/json",
			body:        `{"kind":"component","name":"MermaidDiagram","data":{"code":"` + large + `"}}`,
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

// lockedWriter synchronizes writes to an underlying buffer.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
func TestRequestLoggingIncludesMethodPathStatusDurationAndSessionID(t *testing.T) {
	var mu sync.Mutex
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&lockedWriter{&mu, &logs}, nil))
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
		mu.Lock()
		logOutput := logs.String()
		mu.Unlock()
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
	if !strings.Contains(content, filepath.Base(workspace)) {
		t.Fatalf("session page missing workspace indicator (basename) for %q", workspace)
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
		"Workspace: " + filepath.Base(workspace),
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
		`/static/face.webp`,
		`/static/favicon-32.png`,
		`/static/favicon-16.png`,
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
	if h.runSvc.ActiveRun(strings.TrimPrefix(loc, "/sessions/")) != nil {
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
		"Workspace: " + filepath.Base(workspace),
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
		`/static/face.webp`,
		`/static/favicon-32.png`,
		`/static/favicon-16.png`,
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
func TestSkillsPage_ButtonClasses(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)

	// Write two skills — one stays effective, one gets disabled
	writeSkill(t, filepath.Join(rootDir, "eff-skill"), "eff-skill", "# Effective")
	writeSkill(t, filepath.Join(rootDir, "dis-skill"), "dis-skill", "# Disabled")

	// Disable one so both Disable All and Enable All are rendered
	req, _ := http.NewRequest("POST", server.URL+"/api/skills/dis-skill/disable", nil)
	req.Header.Set("HX-Request", "true")
	disableResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	disableResp.Body.Close()

	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, `class="btn btn-primary"`) {
		t.Error("skills page missing btn-primary class on Refresh button")
	}
	if !strings.Contains(content, `class="btn btn-danger"`) {
		t.Error("skills page missing btn-danger class on Disable All button")
	}
	if !strings.Contains(content, `class="btn btn-secondary"`) {
		t.Error("skills page missing btn-secondary class on Enable All button")
	}
}

func TestSkillsPage_HasCollapsibleSections(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)

	writeSkill(t, filepath.Join(rootDir, "test-skill"), "test-skill", "# Test")

	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, `<details class="settings-details"`) {
		t.Error("skills page missing collapsible <details class=\"settings-details\">")
	}
}

func TestSkillsPage_HasSearchFilter(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)

	writeSkill(t, filepath.Join(rootDir, "test-skill"), "test-skill", "# Test")

	resp, err := http.Get(server.URL + "/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, `class="skills-filter-input"`) {
		t.Error("skills page missing skills-filter-input")
	}
}

func TestAPISkillsFilter(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithSkillsService(t, workspace, skillsSvc)

	// Create skills with matching and non-matching names
	writeSkill(t, filepath.Join(rootDir, "foo-skill"), "foo-skill", "# Foo skill")
	writeSkill(t, filepath.Join(rootDir, "bar-skill"), "bar-skill", "# Bar skill")
	writeSkill(t, filepath.Join(rootDir, "foobar-skill"), "foobar-skill", "# Foobar skill")
	writeSkill(t, filepath.Join(rootDir, "baz-skill"), "baz-skill", "# Baz skill")

	t.Run("filter matches case-insensitive", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/api/skills?q=foo", nil)
		req.Header.Set("HX-Request", "true")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html (HTMX fragment)", ct)
		}

		body, _ := io.ReadAll(resp.Body)
		content := string(body)

		if !strings.Contains(content, "foo-skill") {
			t.Error("response missing foo-skill")
		}
		if !strings.Contains(content, "foobar-skill") {
			t.Error("response missing foobar-skill")
		}
		if strings.Contains(content, `>bar-skill<`) {
			t.Error("response should not contain bar-skill")
		}
		if strings.Contains(content, `>baz-skill<`) {
			t.Error("response should not contain baz-skill")
		}
	})

	t.Run("empty q returns all skills", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/skills")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		content := string(body)

		if !strings.Contains(content, "foo-skill") {
			t.Error("response missing foo-skill")
		}
		if !strings.Contains(content, "bar-skill") {
			t.Error("response missing bar-skill")
		}
		if !strings.Contains(content, "foobar-skill") {
			t.Error("response missing foobar-skill")
		}
		if !strings.Contains(content, "baz-skill") {
			t.Error("response missing baz-skill")
		}
	})

	t.Run("no-match q returns no skills", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/skills?q=nonexistent")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		content := string(body)

		if strings.Contains(content, "skill-card") {
			t.Error("no skill cards should be rendered for no-match query")
		}
	})
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
	ts := newManagedTestServerWithRunsAndSkillsService(t, workspace, skillsSvc)
	server := ts.server
	client := noRedirectClient()

	// Configure a valid provider so run start succeeds
	providerSrv := fakeProviderServer(t, 200, `{"object":"list","data":[{"id":"test-model","object":"model"}]}`)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":%q,"api_key":"sk-test","model":"test-model"}`, providerSrv.URL))

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

	payload := `{"kind":"component","name":"MermaidDiagram","data":{"code":"graph TD; A-->B;"}}`
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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

	payload := `{"kind":"component","name":"QuickReplies","data":{"options":["Summarize","Make shorter"]}}`
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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

	payload := `{"kind":"component","name":"DiffCard","data":{"old":"old code","new":"new code","lang":"go"}}`
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
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
	if !strings.Contains(content, "diff-pane-unified") {
		t.Errorf("DiffCard response missing unified pane, got: %s", content[:200])
	}
	if !strings.Contains(content, "diff-pane-side-by-side") {
		t.Errorf("DiffCard response missing side-by-side pane, got: %s", content[:200])
	}
	if !strings.Contains(content, "diff-toggle-btn") {
		t.Errorf("DiffCard response missing view toggle buttons, got: %s", content[:200])
	}
	if !strings.Contains(content, "old code") {
		t.Errorf("DiffCard response missing old code, got: %s", content[:100])
	}
	if !strings.Contains(content, "new code") {
		t.Errorf("DiffCard response missing new code, got: %s", content[:100])
	}
}

func TestRenderComponent_OwnershipMismatch(t *testing.T) {
	server := newTestServer(t)
	client := noRedirectClient()

	// Use no browser cookie — should get 404
	payload := `{"kind":"component","name":"MermaidDiagram","data":{}}`

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/nonexistent/render", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("render component no auth status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestUnifiedRender_Error(t *testing.T) {
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

	body := map[string]interface{}{
		"kind":    "error",
		"message": "Something went wrong",
	}
	bodyJSON, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("unified render error status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	respBody, _ := io.ReadAll(resp.Body)
	content := string(respBody)
	if !strings.Contains(content, "Something went wrong") {
		t.Errorf("unified render error missing message, got: %s", content[:200])
	}
}

func TestUnifiedRender_Component(t *testing.T) {
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

	body := map[string]interface{}{
		"kind": "component",
		"name": "MermaidDiagram",
		"data": map[string]interface{}{
			"code": "graph TD; A-->B;",
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("unified render mermaid status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	respBody, _ := io.ReadAll(resp.Body)
	content := string(respBody)
	if !strings.Contains(content, "mermaid") {
		t.Errorf("unified render mermaid missing 'mermaid' class, got: %s", content[:200])
	}
}

func TestUnifiedRender_ComponentQuickReplies(t *testing.T) {
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

	body := map[string]interface{}{
		"kind": "component",
		"name": "QuickReplies",
		"data": map[string]interface{}{
			"options": []string{"Summarize", "Make shorter"},
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("unified render QuickReplies status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	respBody, _ := io.ReadAll(resp.Body)
	content := string(respBody)
	if !strings.Contains(content, "Summarize") {
		t.Errorf("unified render QuickReplies missing 'Summarize', got: %s", content[:200])
	}
}

func TestUnifiedRender_ComponentDiffCard(t *testing.T) {
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

	body := map[string]interface{}{
		"kind": "component",
		"name": "DiffCard",
		"data": map[string]interface{}{
			"old":  "old code",
			"new":  "new code",
			"lang": "go",
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("unified render DiffCard status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	respBody, _ := io.ReadAll(resp.Body)
	content := string(respBody)
	if !strings.Contains(content, "diff-card") {
		t.Errorf("unified render DiffCard missing 'diff-card' class, got: %s", content[:200])
	}
}

func TestUnifiedRender_UnknownKind(t *testing.T) {
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

	body := map[string]interface{}{
		"kind": "unknown_kind",
	}
	bodyJSON, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/render", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(browserCookie)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unified render unknown kind status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestUnifiedRender_OwnershipMismatch(t *testing.T) {
	server := newTestServer(t)

	// Error rendering works without auth
	body := map[string]interface{}{
		"kind": "error",
		"message": "test",
	}
	bodyJSON, _ := json.Marshal(body)
	resp, err := http.Post(server.URL+"/api/sessions/nonexistent/render", "application/json", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("unified render error without auth status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	// Non-error kinds without auth -> 404
	body2 := map[string]interface{}{
		"kind": "markdown",
		"message_id": "test",
	}
	bodyJSON2, _ := json.Marshal(body2)
	resp2, err := http.Post(server.URL+"/api/sessions/nonexistent/render", "application/json", bytes.NewReader(bodyJSON2))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("unified render markdown without auth status = %d, want %d", resp2.StatusCode, http.StatusNotFound)
	}
}

func TestSkillsEndpoint_DisableToggle(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	configPath := t.TempDir() + "/config.json"
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath, skillsService: skillsSvc})
	client := noRedirectClient()

	writeSkill(t, filepath.Join(rootDir, "my-skill"), "my-skill", "# My Skill")

	// Disable via POST
	req, _ := http.NewRequest("POST", server.URL+"/api/skills/my-skill/disable", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "Disabled Skills") {
		t.Fatalf("disable response missing 'Disabled Skills' section: %s", content)
	}
	if !strings.Contains(content, "my-skill") {
		t.Fatalf("disable response missing skill name: %s", content)
	}
	if !strings.Contains(content, `hx-post="/api/skills/my-skill/enable"`) {
		t.Fatalf("disable response missing enable button: %s", content)
	}
	if !strings.Contains(content, "disabled") {
		t.Fatalf("disable response missing 'disabled' status badge: %s", content)
	}

	// Verify config reflects disabled skill
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range cfg.DisabledSkills {
		if d == "my-skill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("config.DisabledSkills does not contain 'my-skill'")
	}
}

func TestSkillsEndpoint_EnableToggle(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	configPath := t.TempDir() + "/config.json"
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath, skillsService: skillsSvc})
	client := noRedirectClient()

	writeSkill(t, filepath.Join(rootDir, "my-skill"), "my-skill", "# My Skill")

	// First disable it
	req, _ := http.NewRequest("POST", server.URL+"/api/skills/my-skill/disable", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Now enable it
	req, _ = http.NewRequest("POST", server.URL+"/api/skills/my-skill/enable", nil)
	req.Header.Set("HX-Request", "true")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "Effective Skills") {
		t.Fatalf("enable response missing 'Effective Skills' section: %s", content)
	}
	if !strings.Contains(content, "my-skill") {
		t.Fatalf("enable response missing skill name: %s", content)
	}
	if !strings.Contains(content, `hx-post="/api/skills/my-skill/disable"`) {
		t.Fatalf("enable response missing disable button: %s", content)
	}
	if !strings.Contains(content, "effective") {
		t.Fatalf("enable response missing 'effective' status badge: %s", content)
	}

	// Verify config reflects enabled skill
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range cfg.DisabledSkills {
		if d == "my-skill" {
			t.Fatal("config.DisabledSkills still contains 'my-skill' after enable")
		}
	}
}

func TestAPISkillsEndpoint_OmitDisabled(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	configPath := t.TempDir() + "/config.json"
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath, skillsService: skillsSvc})
	client := noRedirectClient()

	writeSkill(t, filepath.Join(rootDir, "skill-a"), "skill-a", "# A")
	writeSkill(t, filepath.Join(rootDir, "skill-b"), "skill-b", "# B")

	// Disable skill-a
	req, _ := http.NewRequest("POST", server.URL+"/api/skills/skill-a/disable", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// GET /api/skills JSON should omit disabled skill
	resp, err = http.Get(server.URL + "/api/skills")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/skills status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	skillsJSON, ok := body["skills"].([]interface{})
	if !ok {
		t.Fatalf("response 'skills' field is not an array: %#v", body["skills"])
	}

	if len(skillsJSON) != 1 {
		t.Fatalf("skills count = %d, want 1 (only skill-b)", len(skillsJSON))
	}
	first := skillsJSON[0].(map[string]interface{})
	if first["name"] != "skill-b" {
		t.Errorf("skill name = %q, want 'skill-b'", first["name"])
	}
}

func TestSkillsEndpoint_AutoDeactivate(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	configPath := t.TempDir() + "/config.json"
	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath, sessionManager: sessionMgr, skillsService: skillsSvc})
	client := noRedirectClient()

	writeSkill(t, filepath.Join(rootDir, "my-skill"), "my-skill", "# My Skill")
	sessionID, browserCookie := createSessionForBrowser(t, server, client)

	// Activate the skill in the session
	req, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sessionID+"/skills/my-skill/activate", nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(browserCookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Verify skill is active
	sess := sessionMgr.Get(sessionID)
	if sess == nil {
		t.Fatal("session not found")
	}
	hasActive := false
	for _, name := range sess.ActiveSkills {
		if name == "my-skill" {
			hasActive = true
			break
		}
	}
	if !hasActive {
		t.Fatal("skill should be active before disable")
	}

	// Disable the skill via POST
	req, _ = http.NewRequest("POST", server.URL+"/api/skills/my-skill/disable", nil)
	req.Header.Set("HX-Request", "true")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Verify skill is no longer active in the session
	sess = sessionMgr.Get(sessionID)
	if sess == nil {
		t.Fatal("session not found")
	}
	for _, name := range sess.ActiveSkills {
		if name == "my-skill" {
			t.Fatal("skill should be deactivated after disable")
		}
	}
}

func TestSkillsEndpoint_DisableAll(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	configPath := t.TempDir() + "/config.json"
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath, skillsService: skillsSvc})
	client := noRedirectClient()

	writeSkill(t, filepath.Join(rootDir, "skill-a"), "skill-a", "# Skill A")
	writeSkill(t, filepath.Join(rootDir, "skill-b"), "skill-b", "# Skill B")

	// Disable all via POST
	req, _ := http.NewRequest("POST", server.URL+"/api/skills/disable-all", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("disable-all status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "Disabled Skills") {
		t.Fatalf("disable-all response missing 'Disabled Skills' section: %s", content)
	}
	if !strings.Contains(content, "skill-a") {
		t.Fatalf("disable-all response missing skill-a: %s", content)
	}
	if !strings.Contains(content, "skill-b") {
		t.Fatalf("disable-all response missing skill-b: %s", content)
	}

	// Verify config has both skills disabled
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	disabledMap := make(map[string]bool)
	for _, d := range cfg.DisabledSkills {
		disabledMap[d] = true
	}
	if !disabledMap["skill-a"] {
		t.Fatal("config.DisabledSkills missing skill-a")
	}
	if !disabledMap["skill-b"] {
		t.Fatal("config.DisabledSkills missing skill-b")
	}
}

func TestSkillsEndpoint_EnableAll(t *testing.T) {
	workspace := t.TempDir()
	rootDir := t.TempDir()
	configPath := t.TempDir() + "/config.json"
	skillsSvc := skills.NewServiceWithRoots([]skills.Root{{Path: rootDir, Scope: skills.ScopeProjectEitri}})
	server := newTestServerWithOptions(t, workspace, testServerOptions{configPath: configPath, skillsService: skillsSvc})
	client := noRedirectClient()

	writeSkill(t, filepath.Join(rootDir, "skill-a"), "skill-a", "# Skill A")
	writeSkill(t, filepath.Join(rootDir, "skill-b"), "skill-b", "# Skill B")

	// First disable all
	req, _ := http.NewRequest("POST", server.URL+"/api/skills/disable-all", nil)
	req.Header.Set("HX-Request", "true")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Now enable all
	req, _ = http.NewRequest("POST", server.URL+"/api/skills/enable-all", nil)
	req.Header.Set("HX-Request", "true")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable-all status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "Effective Skills") {
		t.Fatalf("enable-all response missing 'Effective Skills' section: %s", content)
	}
	if !strings.Contains(content, "skill-a") {
		t.Fatalf("enable-all response missing skill-a: %s", content)
	}
	if !strings.Contains(content, "skill-b") {
		t.Fatalf("enable-all response missing skill-b: %s", content)
	}

	// Verify config has empty disabled list
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DisabledSkills) != 0 {
		t.Fatalf("config.DisabledSkills should be empty after enable-all, got %v", cfg.DisabledSkills)
	}
}

func TestPutConfigOpenCodeGoPreservesAPIKey(t *testing.T) {
	// Regression test for GH-454: fetchModelList PersistAuth reload branch
	// must not overwrite a new API key with the old empty config from disk.
	ts := newManagedTestServerWithRuns(t)
	server := ts.server

	// Write initial config with empty API key and opencode_go provider
	initialCfg := &config.Config{
		Provider:            "opencode_go",
		BaseURL:             "https://api.opencode.ai/v1",
		Model:               "gpt-4",
		APIKey:              "",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}
	if err := config.Save(ts.configPath, initialCfg); err != nil {
		t.Fatal(err)
	}

	// Provider server that always succeeds
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)

	// PUT config with a real API key — this triggers fetchModelList which
	// should NOT reload the API key from disk (where it's still empty).
	body := fmt.Sprintf(`{"provider":"opencode_go","base_url":%q,"api_key":"sk-test-key-123","model":"gpt-4"}`, provider.URL)
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT /api/config status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, string(bodyBytes))
	}

	// Read config from disk and verify API key was saved
	savedCfg, err := config.Load(ts.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if savedCfg.APIKey != "sk-test-key-123" {
		t.Fatalf("saved api_key = %q, want %q", savedCfg.APIKey, "sk-test-key-123")
	}
}

func TestPutConfigCustomOpenAIPreservesAPIKey(t *testing.T) {
	// Regression test for GH-454: same bug for custom_openai (no auth handler).
	ts := newManagedTestServerWithRuns(t)
	server := ts.server

	initialCfg := &config.Config{
		Provider:            "custom_openai",
		BaseURL:             "https://api.custom.com/v1",
		Model:               "gpt-4",
		APIKey:              "",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 256000,
	}
	if err := config.Save(ts.configPath, initialCfg); err != nil {
		t.Fatal(err)
	}

	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"}]}`)

	body := fmt.Sprintf(`{"provider":"custom_openai","base_url":%q,"api_key":"sk-custom-key","model":"gpt-4"}`, provider.URL)
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
		bodyBytes, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT /api/config status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, string(bodyBytes))
	}

	savedCfg, err := config.Load(ts.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if savedCfg.APIKey != "sk-custom-key" {
		t.Fatalf("saved api_key = %q, want %q", savedCfg.APIKey, "sk-custom-key")
	}
}

// --- Debug API tests (issue #556) ---

func TestDebugHealth(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %q, want ok", body["status"])
	}
}

func TestDebugRuntime(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Check top-level keys exist
	for _, k := range []string{"version", "up_since", "active_run_count", "session_count", "recorded_http_traces", "config_summary"} {
		if _, ok := body[k]; !ok {
			t.Errorf("response missing key %q", k)
		}
	}

	// version defaults to "dev" in tests
	if body["version"] != "dev" {
		t.Errorf("version = %v, want dev", body["version"])
	}

	// session_count should be 0 in empty test server
	if n, ok := body["session_count"].(float64); ok && n != 0 {
		t.Errorf("session_count = %v, want 0", n)
	}

	// recorded_http_traces should be 0
	if n, ok := body["recorded_http_traces"].(float64); ok && n != 0 {
		t.Errorf("recorded_http_traces = %v, want 0", n)
	}

	// config_summary should have expected fields
	cfgSummary, ok := body["config_summary"].(map[string]interface{})
	if !ok {
		t.Fatal("config_summary missing or not an object")
	}
	for _, k := range []string{"provider_id", "model", "base_url", "context_window_tokens", "max_turns", "command_timeout", "has_api_key"} {
		if _, ok := cfgSummary[k]; !ok {
			t.Errorf("config_summary missing key %q", k)
		}
	}

	// has_api_key should be false since no config saved
	if v, ok := cfgSummary["has_api_key"].(bool); ok && v {
		t.Errorf("has_api_key = true, want false (no config saved)")
	}
}

func TestDebugConfig(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"provider_id", "model", "base_url", "context_window_tokens", "max_turns", "command_timeout", "has_api_key"} {
		if _, ok := body[k]; !ok {
			t.Errorf("/api/debug/config missing key %q", k)
		}
	}

	// Ensure no secret field keys leaked at top level
	secretKeys := []string{"api_key", "provider_auth"}
	for _, secret := range secretKeys {
		if _, ok := body[secret]; ok {
			t.Errorf("/api/debug/config leaks secret field %q at top level", secret)
		}
	}

	// has_api_key should be false (no config saved)
	if v, ok := body["has_api_key"].(bool); ok && v {
		t.Errorf("has_api_key = true, want false")
	}
}

func TestDebugSessionsEmpty(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var sessions []interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("sessions = %#v, want empty list", sessions)
	}
}

func TestDebugSessionsWithSession(t *testing.T) {
	// Create server with a session manager that has one session
	sessionMgr := session.NewManager(10)
	_, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	server := newTestServerWithSessionManager(t, t.TempDir(), sessionMgr)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var sessions []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	if s["status"] != "idle" {
		t.Errorf("status = %v, want idle", s["status"])
	}
	if _, ok := s["id"]; !ok {
		t.Errorf("session missing id")
	}
	if _, ok := s["title"]; !ok {
		t.Errorf("session missing title")
	}
	if _, ok := s["message_count"]; !ok {
		t.Errorf("session missing message_count")
	}
	if _, ok := s["active_skills"]; !ok {
		t.Errorf("session missing active_skills")
	}
	if _, ok := s["last_message_timestamp"]; !ok {
		t.Errorf("session missing last_message_timestamp")
	}
	// Idle sessions should not have a "run" field
	if _, ok := s["run"]; ok {
		t.Errorf("idle session should not have run field")
	}
}

func TestDebugSessionByIDNotFound(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	var errBody map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&errBody); err != nil {
		t.Fatal(err)
	}
	if errBody["error"] == "" {
		t.Errorf("expected error message, got empty")
	}
}

func TestDebugSessionByID(t *testing.T) {
	sessionMgr := session.NewManager(10)
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	// Add a message to the session
	sessionMgr.AppendMessage(sess.ID, session.Message{
		Role:      "user",
		Content:   "Hello",
		CreatedAt: time.Now(),
	})

	server := newTestServerWithSessionManager(t, t.TempDir(), sessionMgr)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var detail map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}

	// Check session summary
	sessionData, ok := detail["session"].(map[string]interface{})
	if !ok {
		t.Fatal("session field missing or not an object")
	}
	if sessionData["status"] != "idle" {
		t.Errorf("session.status = %v, want idle", sessionData["status"])
	}

	// Check messages array
	messages, ok := detail["messages"].([]interface{})
	if !ok {
		t.Fatal("messages field missing or not an array")
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	msg := messages[0].(map[string]interface{})
	if msg["role"] != "user" {
		t.Errorf("message.role = %v, want user", msg["role"])
	}
	if msg["content"] != "Hello" {
		t.Errorf("message.content = %v, want Hello", msg["content"])
	}
	if _, ok := msg["created_at"]; !ok {
		t.Errorf("message missing created_at")
	}

	// Check active_skills
	if _, ok := detail["active_skills"]; !ok {
		t.Errorf("detail missing active_skills")
	}
}

func TestDebugSessionByIDLimitMessages(t *testing.T) {
	sessionMgr := session.NewManager(10)
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	// Add multiple messages
	for i := 0; i < 5; i++ {
		sessionMgr.AppendMessage(sess.ID, session.Message{
			Role:      "user",
			Content:   fmt.Sprintf("Message %d", i),
			CreatedAt: time.Now(),
		})
	}

	server := newTestServerWithSessionManager(t, t.TempDir(), sessionMgr)
	defer server.Close()

	// Request limit_messages=2
	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "?limit_messages=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var detail map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}

	messages, ok := detail["messages"].([]interface{})
	if !ok {
		t.Fatal("messages field missing or not an array")
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages with limit_messages=2, got %d", len(messages))
	}
}

func TestDebugConfigWithSavedConfig(t *testing.T) {
	configPath := t.TempDir() + "/config.json"
	initialCfg := &config.Config{
		Provider:            "opencode_go",
		APIKey:              "sk-my-secret",
		BaseURL:             "https://api.example.com/v1",
		Model:               "gpt-4",
		SessionTimeout:      30 * 60_000_000_000,
		CommandTimeout:      60 * 1_000_000_000,
		MaxTurns:            25,
		ContextWindowTokens: 128000,
	}
	if err := config.Save(configPath, initialCfg); err != nil {
		t.Fatal(err)
	}

	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewService()
	srv := api.NewServer(api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
		Version:        "test-version",
		StartTime:      time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	server := httptest.NewServer(srv.Handler())
	defer server.Close()

	// Test /api/debug/config - must not leak api_key
	resp, err := http.Get(server.URL + "/api/debug/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var cfgResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&cfgResp); err != nil {
		t.Fatal(err)
	}

	// Check fields match
	if cfgResp["provider_id"] != "opencode_go" {
		t.Errorf("provider_id = %v, want opencode_go", cfgResp["provider_id"])
	}
	if cfgResp["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", cfgResp["model"])
	}
	if cfgResp["has_api_key"] != true {
		t.Errorf("has_api_key = %v, want true", cfgResp["has_api_key"])
	}

	// Ensure api_key is NOT in the response body
	bodyBytes, err := json.Marshal(cfgResp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bodyBytes), "sk-my-secret") {
		t.Errorf("response leaks API key secret")
	}

	// Test /api/debug/runtime - check version, up_since
	resp2, err := http.Get(server.URL + "/api/debug/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("runtime status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	var runtimeResp map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&runtimeResp); err != nil {
		t.Fatal(err)
	}

	if runtimeResp["version"] != "test-version" {
		t.Errorf("version = %v, want test-version", runtimeResp["version"])
	}

	// up_since should be present
	if _, ok := runtimeResp["up_since"]; !ok {
		t.Errorf("runtime response missing up_since")
	}

	// session_count should be 0
	if n, ok := runtimeResp["session_count"].(float64); ok && n != 0 {
		t.Errorf("session_count = %v, want 0", n)
	}
}

func TestDebugHTTP_NoRecorder(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDebugHTTP_List(t *testing.T) {
	rec := debug.NewRecorder(10)
	rec.Record("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"test"}`), []byte(`{"response":"ok"}`), 200, time.Second, "")

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Traces   []interface{} `json:"traces"`
		InFlight []interface{} `json:"in_flight"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(body.Traces))
	}
	if len(body.InFlight) != 0 {
		t.Fatalf("in_flight = %d, want 0", len(body.InFlight))
	}
}

func TestDebugHTTP_ListEmpty(t *testing.T) {
	rec := debug.NewRecorder(10)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Traces   []interface{} `json:"traces"`
		InFlight []interface{} `json:"in_flight"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Traces) != 0 {
		t.Fatalf("traces = %d, want 0", len(body.Traces))
	}
}

func TestDebugHTTP_SessionFilter(t *testing.T) {
	rec := debug.NewRecorder(10)
	rec.Record("session-a", "p1", "GET", "/path", nil, nil, 200, 0, "")
	rec.Record("session-b", "p2", "GET", "/path", nil, nil, 200, 0, "")

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http?session_id=session-a")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Traces   []interface{} `json:"traces"`
		InFlight []interface{} `json:"in_flight"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(body.Traces))
	}
}

func TestDebugHTTP_ProviderFilter(t *testing.T) {
	rec := debug.NewRecorder(10)
	rec.Record("s1", "p1", "GET", "/path", nil, nil, 200, 0, "")
	rec.Record("s2", "p2", "GET", "/path", nil, nil, 200, 0, "")

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http?provider_id=p1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Traces   []interface{} `json:"traces"`
		InFlight []interface{} `json:"in_flight"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	if len(body.Traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(body.Traces))
	}
}

func TestDebugHTTP_ByID_NotFound(t *testing.T) {
	rec := debug.NewRecorder(10)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDebugHTTP_ByID_Success(t *testing.T) {
	rec := debug.NewRecorder(10)
	rec.Record("s1", "p1", "POST", "/v1/chat", []byte("req"), []byte("resp"), 200, time.Second, "")

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	// Get the trace ID from list
	resp, err := http.Get(server.URL + "/api/debug/http")
	if err != nil {
		t.Fatal(err)
	}

	var listBody struct {
		Traces []struct {
			ID string `json:"id"`
		} `json:"traces"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listBody); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(listBody.Traces) != 1 {
		t.Fatalf("traces = %d, want 1", len(listBody.Traces))
	}

	traceID := listBody.Traces[0].ID

	// Get by ID
	resp2, err := http.Get(server.URL + "/api/debug/http/" + traceID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	var trace map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&trace); err != nil {
		t.Fatal(err)
	}

	if trace["id"] != traceID {
		t.Fatalf("id = %v, want %s", trace["id"], traceID)
	}
	if trace["session_id"] != "s1" {
		t.Fatalf("session_id = %v, want s1", trace["session_id"])
	}
	if trace["provider_id"] != "p1" {
		t.Fatalf("provider_id = %v, want p1", trace["provider_id"])
	}
	if trace["status"] != float64(200) {
		t.Fatalf("status = %v, want 200", trace["status"])
	}
}

func TestDebugUmbrella(t *testing.T) {
	t.Parallel()

	rec := debug.NewRecorder(10)
	rec.Record("s1", "p1", "POST", "/v1/chat", []byte(`{"model":"test"}`), []byte(`{"response":"ok"}`), 200, time.Second, "")

	sessionMgr := session.NewManager(10)
	sess, err := sessionMgr.Create("test-session")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder:  rec,
		sessionManager: sessionMgr,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Check all expected top-level keys
	expectedKeys := []string{"version", "up_since", "runtime", "sessions", "http_traces", "config_summary"}
	for _, k := range expectedKeys {
		if _, ok := body[k]; !ok {
			t.Errorf("/api/debug missing key %q", k)
		}
	}

	// Check runtime sub-fields
	runtime, ok := body["runtime"].(map[string]interface{})
	if !ok {
		t.Fatal("runtime is not an object")
	}
	if runtime["active_run_count"] != float64(0) {
		t.Errorf("active_run_count = %v, want 0", runtime["active_run_count"])
	}
	if runtime["session_count"] != float64(1) {
		t.Errorf("session_count = %v, want 1", runtime["session_count"])
	}
	if runtime["recorded_http_traces"] != float64(1) {
		t.Errorf("recorded_http_traces = %v, want 1", runtime["recorded_http_traces"])
	}

	// Check sessions is array with 1 entry
	sessions, ok := body["sessions"].([]interface{})
	if !ok {
		t.Fatal("sessions is not an array")
	}
	if len(sessions) != 1 {
		t.Errorf("sessions length = %d, want 1", len(sessions))
	}

	// Check http_traces has 1 trace
	httpTraces, ok := body["http_traces"].(map[string]interface{})
	if !ok {
		t.Fatal("http_traces is not an object")
	}
	traces, ok := httpTraces["traces"].([]interface{})
	if !ok {
		t.Fatal("http_traces.traces is not an array")
	}
	if len(traces) != 1 {
		t.Errorf("http_traces.traces length = %d, want 1", len(traces))
	}

	// Check config_summary has expected keys
	cfgSummary, ok := body["config_summary"].(map[string]interface{})
	if !ok {
		t.Fatal("config_summary is not an object")
	}
	for _, k := range []string{"provider_id", "model", "base_url", "context_window_tokens", "max_turns", "command_timeout", "has_api_key"} {
		if _, ok := cfgSummary[k]; !ok {
			t.Errorf("config_summary missing key %q", k)
		}
	}

	// Ensure no secret leakage
	for _, secret := range []string{"api_key", "provider_auth"} {
		if _, ok := cfgSummary[secret]; ok {
			t.Errorf("config_summary leaks secret field %q", secret)
		}
	}
}

func TestDebugUmbrella_EmptyTrace(t *testing.T) {
	t.Parallel()

	rec := debug.NewRecorder(10)

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	runtime, ok := body["runtime"].(map[string]interface{})
	if !ok {
		t.Fatal("runtime is not an object")
	}
	if runtime["recorded_http_traces"] != float64(0) {
		t.Errorf("recorded_http_traces = %v, want 0", runtime["recorded_http_traces"])
	}

	httpTraces, ok := body["http_traces"].(map[string]interface{})
	if !ok {
		t.Fatal("http_traces is not an object")
	}
	traces, ok := httpTraces["traces"].([]interface{})
	if !ok {
		t.Fatal("http_traces.traces is not an array")
	}
	if len(traces) != 0 {
		t.Errorf("http_traces.traces length = %d, want 0", len(traces))
	}
}
