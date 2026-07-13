package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	cfg := api.ServerConfig{
		ConfigPath: t.TempDir() + "/config.json",
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

func TestRootPage(t *testing.T) {
	server := newTestServer(t)

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestRootPageTitle(t *testing.T) {
	server := newTestServer(t)

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Check title is present in body
	body := make([]byte, 4096)
	n, _ := resp.Body.Read(body)
	content := string(body[:n])
	if !strings.Contains(content, "Eitri — AI Assistant") {
		t.Errorf("page body missing title, got: %s", content)
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
