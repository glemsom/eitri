package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	sessionMgr := session.NewManager(10)
	skillsSvc := skills.NewService()
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
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
	server := newTestServer(t)

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

	body := make([]byte, 8192)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	// Check for expected form elements
	checks := []string{"provider", "api_key", "base_url", "model", "session_timeout", "command_timeout", "max_turns"}
	for _, c := range checks {
		if !strings.Contains(content, c) {
			t.Errorf("settings page missing %q", c)
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
		t.Errorf("PUT /api/config status = %d, want %d", resp.StatusCode, http.StatusOK)
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
	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test-key"}`
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
	form.Set("model", "")

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
	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-existing"}`
	req, _ := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(req)

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
	getResp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()

	var cfgData map[string]interface{}
	json.NewDecoder(getResp.Body).Decode(&cfgData)
	if key, ok := cfgData["api_key"].(string); !ok || key == "" {
		t.Errorf("api_key = %q after empty form submit, want preserved", key)
	}
}

func TestGetConfigHTMLFragmentWithModels(t *testing.T) {
	provider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"claude-3"}]}`)
	server := newTestServer(t)

	// Save to populate config
	body := `{"provider":"custom_openai","base_url":"` + provider.URL + `","api_key":"sk-test"}`
	req, _ := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(req)

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
	server := newTestServer(t)

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

	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])

	if !strings.Contains(content, "Agent Skills") {
		t.Errorf("skills page missing 'Agent Skills' heading, got: %s", content[:200])
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
