package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
)

// ————— handleConfirm —————

func TestHandleConfirm_Approved(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})
	server := newTestServerWithRunService(t, workspace, sessionMgr, nil, runSvc)
	defer server.Close()

	browserID := "test-browser-confirm-approved"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a pending confirmation by adding a confirmation channel
	// The handler calls ResolveConfirmation which looks up the channel.
	// We need a channel registered first.
	_ = runSvc

	body := map[string]any{
		"path":     "/some/path",
		"approved": true,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/confirm", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/confirm failed: %v", err)
	}
	defer resp.Body.Close()

	// ResolveConfirmation will return false because no channel is registered.
	// The handler returns 404 in that case.
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404 (no pending confirmation), got %d", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(respBody), "no pending confirmation") {
		t.Errorf("response body missing error message: %s", string(respBody))
	}
}

func TestHandleConfirm_Denied(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})
	server := newTestServerWithRunService(t, workspace, sessionMgr, nil, runSvc)
	defer server.Close()

	browserID := "test-browser-confirm-denied"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"path":     "/some/path",
		"approved": false,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/confirm", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/confirm failed: %v", err)
	}
	defer resp.Body.Close()

	// No pending confirmation channel, so it returns 404
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404 (no pending confirmation), got %d", resp.StatusCode)
	}
}

func TestHandleConfirm_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})
	server := newTestServerWithRunService(t, workspace, sessionMgr, nil, runSvc)
	defer server.Close()

	body := map[string]any{
		"path":     "/some/path",
		"approved": true,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/nonexistent/confirm", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: "some-browser"})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/confirm failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleConfirm_MissingPath(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})
	server := newTestServerWithRunService(t, workspace, sessionMgr, nil, runSvc)
	defer server.Close()

	browserID := "test-browser-missing-path"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"approved": true,
		// missing "path"
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/confirm", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/confirm failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleConfirm_ApprovedSavesPathToConfig(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})

	configPath := filepath.Join(t.TempDir(), "config.json")

	// Write initial config
	initialCfg := `{"allowed_read_paths":[]}`
	if err := os.WriteFile(configPath, []byte(initialCfg), 0644); err != nil {
		t.Fatal(err)
	}

	// Build server with explicit config path + run service
	server := newTestServerWithOptions(t, workspace, testServerOptions{
		configPath:     configPath,
		sessionManager: sessionMgr,
	})
	// We need RunService on the same server, so build a custom one.
	// newTestServerWithOptions doesn't take RunService, so we use a different approach.
	// Instead, we test the config save path directly by calling the handler's logic:
	// The handler loads config, adds path, saves — all before calling ResolveConfirmation.
	// Since ResolveConfirmation returns false (no channel), the handler returns 404
	// but the config should already be updated.
	_ = server

	// Build a server that DOES have RunService
	cfg := api.ServerConfig{
		ConfigPath:     configPath,
		Workspace:      workspace,
		SessionManager: sessionMgr,
		RunService:     runSvc,
	}
	apiServer := api.NewServer(cfg)
	httpServer := httptest.NewServer(apiServer.Handler())
	defer httpServer.Close()

	browserID := "test-browser-approved-save"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"path":     workspace + "/allowed-dir",
		"approved": true,
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", httpServer.URL+"/api/sessions/"+sess.ID+"/confirm", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/confirm failed: %v", err)
	}
	defer resp.Body.Close()

	// Even though ResolveConfirmation returns false (no channel),
	// the config save step happens before resolution.
	// So config should have been updated.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), workspace+"/allowed-dir") {
		t.Errorf("config does not contain the approved path: %s", string(data))
	}
}

// ————— handleRender —————

func TestHandleRender_ErrorKind(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-error"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind":    "error",
		"message": "Something went wrong",
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(bodyBytes), "error-toast") && !strings.Contains(string(bodyBytes), "Something went wrong") {
		t.Errorf("response missing error toast: %s", string(bodyBytes))
	}
}

func TestHandleRender_MarkdownKind(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-md"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Add an assistant message so markdown render can find it
	sessionMgr.AppendMessage(sess.ID, session.Message{
		Role:    "assistant",
		Content: "Hello **world**!",
	})

	body := map[string]any{
		"kind":       "markdown",
		"message_id": "test-msg-1",
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	content := string(bodyBytes)
	if !strings.Contains(content, "assistant-bubble") && !strings.Contains(content, "<strong>world</strong>") {
		t.Errorf("response missing assistant bubble with rendered markdown: %s", content)
	}
}

func TestHandleRender_MarkdownDedupByMessageID(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-dedup"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	sessionMgr.AppendMessage(sess.ID, session.Message{
		Role:    "assistant",
		Content: "Test content",
	})

	body := map[string]any{
		"kind":       "markdown",
		"message_id": "dedup-test-msg",
	}
	bodyJSON, _ := json.Marshal(body)

	// First render
	req1, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	req1.Header.Set("Content-Type", "application/json")
	req1.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("first render request failed: %v", err)
	}
	resp1.Body.Close()

	// Second render with same message_id should be dedup'd (empty response)
	req2, _ := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	req2.Header.Set("Content-Type", "application/json")
	req2.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second render request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 for dedup, got %d", resp2.StatusCode)
	}

	bodyBytes2, _ := io.ReadAll(resp2.Body)
	if len(bodyBytes2) > 0 {
		t.Errorf("expected empty body for dedup, got %d bytes: %s", len(bodyBytes2), string(bodyBytes2))
	}
}

func TestHandleRender_ComponentKind_MermaidDiagram(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-mermaid"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind":  "component",
		"name":  "MermaidDiagram",
		"data":  map[string]any{"code": "graph TD; A-->B;"},
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	content := string(bodyBytes)
	if !strings.Contains(content, "mermaid") {
		t.Errorf("response missing mermaid diagram: %s", content)
	}
}

func TestHandleRender_ComponentKind_QuickReplies(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-qr"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind": "component",
		"name": "QuickReplies",
		"data": map[string]any{"options": []string{"Yes", "No", "Maybe"}},
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	content := string(bodyBytes)
	if !strings.Contains(content, "Yes") || !strings.Contains(content, "No") || !strings.Contains(content, "Maybe") {
		t.Errorf("response missing quick reply options: %s", content)
	}
}

func TestHandleRender_ComponentKind_DiffCard(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-diff"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind": "component",
		"name": "DiffCard",
		"data": map[string]any{
			"old":  "hello",
			"new":  "world",
			"lang": "text",
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	content := string(bodyBytes)
	if !strings.Contains(content, "diff-card") && !strings.Contains(content, "hello") {
		t.Errorf("response missing diff card: %s", content)
	}
}

func TestHandleRender_ComponentKind_FileEditCard(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-fileedit"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind": "component",
		"name": "FileEditCard",
		"data": map[string]any{
			"path":          "/test/file.txt",
			"mode":          "edit",
			"old":           "old content",
			"new":           "new content",
			"bytes_written": 11,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	content := string(bodyBytes)
	if !strings.Contains(content, "file-edit-card") && !strings.Contains(content, "/test/file.txt") {
		t.Errorf("response missing file edit card: %s", content)
	}
}

func TestHandleRender_UnknownComponent(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-unknown"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind": "component",
		"name": "NonExistentComponent",
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleRender_UnknownKind(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-unknown-kind"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"kind": "nonexistent",
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "browser_id", Value: browserID})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleRender_SessionNotFound(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	// Error kind doesn't require a session, so it should render even without one
	// But we test with a non-existent session ID to verify the session check for non-error kinds
	body := map[string]any{
		"kind": "markdown",
	}
	bodyJSON, _ := json.Marshal(body)

	req, err := http.NewRequest("POST", server.URL+"/api/sessions/nonexistent/render", bytes.NewReader(bodyJSON))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestHandleRender_FormURLEncodedBody(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, workspace)
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)
	defer server.Close()

	browserID := "test-browser-render-form"
	sess, err := sessionMgr.Create(browserID)
	if err != nil {
		t.Fatal(err)
	}

	// Send form-urlencoded body (HTMX 2.0 fallback)
	formBody := "kind=error&message=Form+error"
	req, err := http.NewRequest("POST", server.URL+"/api/sessions/"+sess.ID+"/render", bytes.NewReader([]byte(formBody)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/sessions/{id}/render failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	content := string(bodyBytes)
	if !strings.Contains(content, "Form error") {
		t.Errorf("response missing error message: %s", content)
	}
}
