package api_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persona"
	"github.com/glemsom/eitri/internal/llm"
	runner "github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
)

// newTestServerForCompact creates a test server with RunService and a history
// session manager, writing a minimal config file so the config is loadable.
func newTestServerForCompact(t *testing.T) *testServerWithRuns {
	t.Helper()
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	historySessionMgr := history.NewSessionManager(50)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr:      sessionMgr,
		HistorySessionMgr: historySessionMgr,
	})

	if err := persona.EnsureGeneric(workspace); err != nil {
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
	msgs := []llm.Message{
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
