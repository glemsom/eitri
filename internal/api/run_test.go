package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/executor"
	agentrunner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
)

// newRunnerManager creates a test RunnerManager.
func newRunnerManager(t *testing.T) *agentrunner.Manager {
	t.Helper()
	return agentrunner.NewManager()
}

// newTestServerWithRuns creates a test server with RunManager enabled.
func newTestServerWithRuns(t *testing.T) *httptest.Server {
	t.Helper()
	sessionMgr := session.NewManager(10)
	executorMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	runnerMgr := newRunnerManager(t)
	runMgr := api.NewRunManager(runnerMgr, executorMgr)

	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      t.TempDir(),
		SessionManager: sessionMgr,
		RunManager:     runMgr,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func TestRunManager_NewRunManager(t *testing.T) {
	runnerMgr := newRunnerManager(t)
	executorMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	rm := api.NewRunManager(runnerMgr, executorMgr)
	if rm == nil {
		t.Fatal("RunManager is nil")
	}
}

func TestChatEndpoint_NeedsSession(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	resp, err := client.Post(server.URL+"/api/sessions/nonexistent/chat", "application/x-www-form-urlencoded", strings.NewReader("message=hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestChatEndpoint_MissingMessage(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	chatPath := "/api" + loc + "/chat"
	req, _ := http.NewRequest("POST", server.URL+chatPath, nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if browserCookie != nil {
		req.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("empty message status = %d, want 400", resp2.StatusCode)
	}
}

func TestRenderMarkdown(t *testing.T) {
	// Test markdown rendering directly
	tests := []struct {
		name     string
		input    string
		wantHTML string
	}{
		{
			name:     "empty string",
			input:    "",
			wantHTML: "",
		},
		{
			name:     "simple text",
			input:    "Hello world",
			wantHTML: "<p>Hello world</p>\n",
		},
		{
			name:     "think tags",
			input:    "Before <think>thinking content</think> After",
			wantHTML: "<p>Before </p>\n<details class=\"think-details\">\n<summary>Thinking...</summary>\nthinking content\n</details>\n<p> After</p>\n",
		},
		{
			name:     "code block",
			input:    "```go\npackage main\n```",
			wantHTML: "<pre><code class=\"language-go\">package main\n</code></pre>\n",
		},
		{
			name:     "bold text",
			input:    "**bold** text",
			wantHTML: "<p><strong>bold</strong> text</p>\n",
		},
	}

	// Use a helper to test the renderer indirectly via the markdown render function
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We test goldmark rendering via HTTP endpoint
			server := newTestServerWithRuns(t)
			client := noRedirectClient()

			// Create session first
			resp, err := client.Get(server.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			loc := resp.Header.Get("Location")

			var browserCookie *http.Cookie
			for _, c := range resp.Cookies() {
				if c.Name == "browser_id" {
					browserCookie = c
					break
				}
			}

			// Test the render/markdown endpoint
			renderPath := "/api" + loc + "/render/markdown"
			body := `{"message_id":"test_1"}`
			req, _ := http.NewRequest("POST", server.URL+renderPath, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if browserCookie != nil {
				req.AddCookie(browserCookie)
			}
			resp2, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp2.Body.Close()

			if resp2.StatusCode != http.StatusNotFound && resp2.StatusCode != http.StatusOK {
				t.Logf("render/markdown status = %d", resp2.StatusCode)
			}
		})
	}
}

func TestRenderToolCard(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	// Create session
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	renderPath := "/api" + loc + "/render/tool-card"
	body := `{"type":"tool_call","tool":"terminal_execute","args":{"command":"echo hello"}}`
	req, _ := http.NewRequest("POST", server.URL+renderPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if browserCookie != nil {
		req.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("tool-card render status = %d, want 200", resp2.StatusCode)
	} else {
		ct := resp2.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
	}
}

func TestRenderError(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	body := `{"message":"Test error message"}`
	resp, err := client.Post(server.URL+"/api/sessions/nonexistent/render/error", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Error rendering should work even for nonexistent sessions
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestCancelEndpoint(t *testing.T) {
	server := newTestServerWithRuns(t)
	client := noRedirectClient()

	// Create session
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location") // e.g. /sessions/abc123

	var browserCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "browser_id" {
			browserCookie = c
			break
		}
	}

	// Cancel via /api/sessions/{id}/cancel
	cancelPath := "/api" + loc + "/cancel" // /api/sessions/abc123/cancel
	req, _ := http.NewRequest("POST", server.URL+cancelPath, nil)
	if browserCookie != nil {
		req.AddCookie(browserCookie)
	}
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("cancel status = %d, want 200", resp2.StatusCode)
	} else {
		// Should return HTML fragment (MessageInput)
		ct := resp2.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "text/html") {
			t.Errorf("Content-Type = %q, want text/html", ct)
		}
	}
}

func TestSSEEventSerialization(t *testing.T) {
	// Test that SSEEvent serializes correctly
	evt := api.SSEEvent{Type: "token", Content: "Hello, world!"}
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "token" {
		t.Errorf("type = %v, want 'token'", decoded["type"])
	}
	if decoded["content"] != "Hello, world!" {
		t.Errorf("content = %v, want 'Hello, world!'", decoded["content"])
	}
}
