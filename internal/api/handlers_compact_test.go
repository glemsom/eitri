package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/message"
	runner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
)

// fakeCompactLLMServer returns an httptest.Server that responds to
// POST /v1/chat/completions with a canned OpenAI-compatible response
// containing the given summary text.
func fakeCompactLLMServer(t *testing.T, summary string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{
				"id": "chatcmpl-test",
				"object": "chat.completion",
				"created": 1234567890,
				"model": "test-model",
				"choices": [{
					"index": 0,
					"message": {
						"role": "assistant",
						"content": %q
					},
					"finish_reason": "stop"
				}],
				"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
			}`, summary)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// readBody is a small helper to read the full body from a response.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}

// newTestServerForCompact creates a test server with RunService and a history
// session manager, writing a minimal config file so the config is loadable.
func newTestServerForCompact(t *testing.T) *testServerWithRuns {
	t.Helper()
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	historySessionMgr := history.NewSessionManager(50)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr:      sessionMgr,
		HistorySessionMgr: historySessionMgr,
	})

	if err := persona.EnsureGenericWithHome(homeDir); err != nil {
		t.Fatalf("ensure generic persona: %v", err)
	}

	// Write a minimal config to disk so loadConfigState succeeds.
	cfgContent := `{
		"provider": "opencode_go",
		"base_url": "https://api.example.com",
		"model": "some-model",
		"api_key": "test-key",
		"compaction_enabled": true,
		"compaction_threshold_percent": 90,
		"compaction_low_water_percent": 30,
		"context_window_tokens": 128000
	}`
	configPath := t.TempDir() + "/config.json"
	if err := os.WriteFile(configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: sessionMgr,
		RunService:     runSvc,
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return &testServerWithRuns{
		server:     server,
		configPath: configPath,
		workspace:  workspace,
		sessionMgr: sessionMgr,
		runSvc:     runSvc,
	}
}

func TestHandleCompact_RequiresAuth(t *testing.T) {
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	// No browser cookie → 404 (session not found)
	resp, err := http.Post(ts.server.URL+"/api/sessions/nonexistent/compact", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404 for missing session/auth, got %d", resp.StatusCode)
	}
}

func TestHandleCompact_NoHistoryToCompact(t *testing.T) {
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	// Create a session via API
	browserID := "test-browser-compact-empty"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure the history session manager also has this session
	ts.runSvc.HistorySessionManager().Create(sess.ID)

	// Build request with browser cookie
	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	// Should succeed but indicate no compaction needed (empty history doesn't trigger)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestHandleCompact_DisabledInConfig(t *testing.T) {
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	// Overwrite config with compaction disabled
	cfgContent := `{
		"provider": "opencode_go",
		"base_url": "https://api.example.com",
		"model": "some-model",
		"api_key": "test-key",
		"compaction_enabled": false,
		"compaction_threshold_percent": 90,
		"compaction_low_water_percent": 30,
		"context_window_tokens": 128000
	}`
	if err := os.WriteFile(ts.configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	browserID := "test-browser-compact-disabled"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}
	ts.runSvc.HistorySessionManager().Create(sess.ID)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	// Should succeed (manual compaction is allowed even when auto-compaction is disabled).
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for manual compaction with disabled auto-compaction, got %d", resp.StatusCode)
	}
}

func TestHandleCompact_WithHistory_NoOpWhenLLMFails(t *testing.T) {
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	// Overwrite config with a very small context window so compaction would trigger
	// if the LLM were available. Since no real LLM is running, the compactor will
	// skip the tool message and no compaction will occur.
	cfgContent := `{
		"provider": "opencode_go",
		"base_url": "https://api.example.com",
		"model": "some-model",
		"api_key": "test-key",
		"compaction_enabled": true,
		"compaction_threshold_percent": 50,
		"compaction_low_water_percent": 10,
		"context_window_tokens": 1024
	}`
	if err := os.WriteFile(ts.configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	browserID := "test-browser-compact-llm"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Populate history with a tool result above the high-water mark (1024*50/100=512).
	// 200 lines of ~34 chars = ~6800 chars ≈ 1700 tokens, well over the threshold.
	msgs := []message.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "run the build"},
		{Role: "tool", Content: strings.Repeat("Build output with lots of detail\n", 200)},
	}
	ts.runSvc.HistorySessionManager().RestoreHistory(sess.ID, msgs)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	// LLM call will fail, compactor skips it, returns count=0 → handler responds 200
	// with "no compaction needed" toast.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestHandleCompact_ActiveRunNotChecked(t *testing.T) {
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	browserID := "test-browser-compact-active"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}
	ts.runSvc.HistorySessionManager().Create(sess.ID)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	// Should succeed (no compaction needed due to empty history).
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestHandleCompact_InvalidConfig_Returns422(t *testing.T) {
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	// Overwrite config with invalid values (max_turns=0 fails validation).
	cfgContent := `{
		"provider": "opencode_go",
		"base_url": "https://api.example.com",
		"model": "some-model",
		"api_key": "test-key",
		"compaction_enabled": true,
		"max_turns": 0,
		"context_window_tokens": 128000
	}`
	if err := os.WriteFile(ts.configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	browserID := "test-browser-compact-invalid-cfg"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}
	ts.runSvc.HistorySessionManager().Create(sess.ID)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected status 422 for invalid config, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Config invalid") && !strings.Contains(body, "max_turns") {
		t.Errorf("response body should mention config validation error, got: %s", body)
	}
}

func TestHandleCompact_Success_ReturnsStatsToast(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "summarised build output")
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	// Overwrite config to point at our fake LLM. Thresholds are valid for
	// config validation but manual compaction ignores them (uses 0,0,0).
	cfgContent := fmt.Sprintf(`{
		"provider": "custom_openai",
		"base_url": %q,
		"model": "test-model",
		"api_key": "test-key",
		"compaction_enabled": true,
		"compaction_threshold_percent": 90,
		"compaction_low_water_percent": 30,
		"context_window_tokens": 1024
	}`, fakeLLM.URL)
	if err := os.WriteFile(ts.configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	browserID := "test-browser-compact-success"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Populate history with a large tool message that exceeds the threshold.
	msgs := []message.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "run the build"},
		{Role: "tool", Content: strings.Repeat("Build output with lots of detail\n", 300)},
	}
	ts.runSvc.HistorySessionManager().RestoreHistory(sess.ID, msgs)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Compacted") {
		t.Errorf("response body should contain compaction stats, got: %s", body)
	}
	// Should mention freed tokens
	if !strings.Contains(body, "freed") {
		t.Errorf("response body should mention freed tokens, got: %s", body)
	}
	// Should NOT say "No messages found to compact"
	if strings.Contains(body, "No messages found to compact") {
		t.Errorf("response body should indicate compaction happened, got: %s", body)
	}
}

func TestHandleCompact_PrunedToolCallsReported(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "summarised output")
	ts := newTestServerForCompact(t)
	defer ts.server.Close()

	cfgContent := fmt.Sprintf(`{
		"provider": "custom_openai",
		"base_url": %q,
		"model": "test-model",
		"api_key": "test-key",
		"compaction_enabled": true,
		"compaction_threshold_percent": 90,
		"compaction_low_water_percent": 30,
		"compaction_tool_call_retention_turns": 0,
		"context_window_tokens": 1024
	}`, fakeLLM.URL)
	if err := os.WriteFile(ts.configPath, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}

	browserID := "test-browser-compact-pruned"
	sess, err := ts.sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Create history with assistant messages containing tool calls AND
	// a large tool result so compaction runs.
	msgs := []message.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "list files"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []message.ToolCall{
				{ID: "call_1", Function: message.FunctionCall{Name: "bash", Arguments: `{"command":"ls -la"}`}},
			},
		},
		{Role: "tool", Content: strings.Repeat("file1.txt file2.txt\n", 200), ToolCallID: "call_1"},
	}
	ts.runSvc.HistorySessionManager().RestoreHistory(sess.ID, msgs)

	req, _ := http.NewRequest("POST", ts.server.URL+"/api/sessions/"+sess.ID+"/compact", nil)
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/compact failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	// Should mention tool calls pruned
	if !strings.Contains(body, "pruned") {
		t.Errorf("response body should mention pruned tool calls, got: %s", body)
	}
}
