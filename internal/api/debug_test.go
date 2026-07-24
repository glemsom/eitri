package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/api"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/runner"
	"github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// newTestServerWithRunService creates a server with the given session manager,
// debug recorder, and run service. Any nil parameter is replaced with a default.
func newTestServerWithRunService(t *testing.T, workspace string, sessionMgr *session.Manager, rec *debug.Recorder, runSvc *runner.RunService) *httptest.Server {
	t.Helper()
	if sessionMgr == nil {
		sessionMgr = session.NewManager(10, workspace)
	}
	skillsSvc := skills.NewService()
	if runSvc == nil {
		runSvc = runner.NewRunService(runner.RunServiceDeps{
			UISessionMgr: sessionMgr,
		})
	}
	cfg := api.ServerConfig{
		ConfigPath:     t.TempDir() + "/config.json",
		Workspace:      workspace,
		SessionManager: sessionMgr,
		SkillsService:  skillsSvc,
		RunService:     runSvc,
		DebugRecorder:  rec,
		Version:        "dev",
		StartTime:      time.Now(),
	}
	srv := api.NewServer(cfg)
	server := httptest.NewServer(srv.Handler())
	t.Cleanup(server.Close)
	return server
}

func TestDebugSessions_RunningSessionWithRunService(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}
	sessionMgr.UpdateStatus(sess.ID, session.StatusRunning)

	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})

	server := newTestServerWithRunService(t, t.TempDir(), sessionMgr, nil, runSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var sessions []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	// Session should have a "run" field since status is running
	run, ok := s["run"].(map[string]any)
	if !ok {
		t.Fatal("session missing 'run' field for running session")
	}
	if run["status"] != "running" {
		t.Errorf("run.status = %v, want running", run["status"])
	}
	// Busy should be false since no active run (snap was nil)
	if busy, ok := run["busy"].(bool); ok && busy {
		t.Errorf("run.busy = true, want false")
	}
}

func TestDebugSessions_RunningSessionWithoutRunService(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}
	sessionMgr.UpdateStatus(sess.ID, session.StatusRunning)

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		sessionManager: sessionMgr,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var sessions []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	// Should have run info even without RunService (sessionToSummary sets it)
	run, ok := s["run"].(map[string]any)
	if !ok {
		t.Fatal("session missing 'run' field for running session")
	}
	if run["status"] != "running" {
		t.Errorf("run.status = %v, want running", run["status"])
	}
}

func TestDebugSessionByID_RunningWithRunService(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}
	sessionMgr.UpdateStatus(sess.ID, session.StatusRunning)

	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})

	rec := debug.NewRecorder(10)
	rec.Record(sess.ID, "p1", "POST", "/v1/chat", []byte("req"), []byte("resp"), 200, time.Second, "", nil)

	server := newTestServerWithRunService(t, t.TempDir(), sessionMgr, rec, runSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var detail map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}

	// Should have session wrapper
	sessionData, ok := detail["session"].(map[string]any)
	if !ok {
		t.Fatal("response missing 'session' field")
	}
	if sessionData["status"] != "running" {
		t.Errorf("session.status = %v, want running", sessionData["status"])
	}
	// Should have messages array
	messages, ok := detail["messages"].([]any)
	if !ok {
		t.Fatal("response missing 'messages' array")
	}
	if len(messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(messages))
	}
	// Should have run info
	run, ok := detail["run"].(map[string]any)
	if !ok {
		t.Fatal("response missing 'run' field")
	}
	if run["status"] != "running" {
		t.Errorf("run.status = %v, want running", run["status"])
	}
	// Should have SSE history if run was active (but it's not, so empty/nil)
	_ = detail["sse_history"]
}

func TestDebugSessionByID_WithMessages(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "Hello", CreatedAt: time.Now()})
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "Hi!", CreatedAt: time.Now()})

	rec := debug.NewRecorder(10)
	rec.Record(sess.ID, "p1", "POST", "/v1/chat", []byte("req"), []byte("resp"), 200, time.Second, "", nil)

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		sessionManager: sessionMgr,
		debugRecorder:  rec,
	})
	defer server.Close()

	// Test without limit_messages
	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var detail map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	messages, ok := detail["messages"].([]any)
	if !ok {
		t.Fatal("response missing 'messages' array")
	}
	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}

	// Test with limit_messages=1
	resp2, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "?limit_messages=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	var detail2 map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&detail2); err != nil {
		t.Fatal(err)
	}
	messages2, ok := detail2["messages"].([]any)
	if !ok {
		t.Fatal("response missing 'messages' array")
	}
	if len(messages2) != 1 {
		t.Errorf("expected 1 message with limit_messages=1, got %d", len(messages2))
	}
	// The remaining message should be the last one (assistant)
	msg := messages2[0].(map[string]any)
	if msg["role"] != "assistant" {
		t.Errorf("msg.role = %v, want 'assistant'", msg["role"])
	}
}

func TestDebugSessionByID_InvalidLimit(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "Hello", CreatedAt: time.Now()})
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "Hi!", CreatedAt: time.Now()})

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		sessionManager: sessionMgr,
	})
	defer server.Close()

	// Test with invalid limit_messages (negative)
	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "?limit_messages=-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var detail map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatal(err)
	}
	// Invalid limit should be ignored, all messages returned
	messages, ok := detail["messages"].([]any)
	if !ok {
		t.Fatal("response missing 'messages' array")
	}
	if len(messages) != 2 {
		t.Errorf("expected 2 messages with invalid limit, got %d", len(messages))
	}

	// Test with limit_messages larger than message count
	resp2, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "?limit_messages=100")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	var detail2 map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&detail2); err != nil {
		t.Fatal(err)
	}
	messages2, ok := detail2["messages"].([]any)
	if !ok {
		t.Fatal("response missing 'messages' array after too-large limit")
	}
	if len(messages2) != 2 {
		t.Errorf("expected 2 messages with too-large limit, got %d", len(messages2))
	}
}

func TestDebugSessionByID_NotFound_New(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "session not found" {
		t.Errorf("error = %q, want %q", body["error"], "session not found")
	}
}

func TestDebugSessionHTTP_WithLimit(t *testing.T) {
	rec := debug.NewRecorder(10)
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	// Record 5 traces for this session
	for i := 0; i < 5; i++ {
		rec.Record(sess.ID, "p1", "POST", "/v1/chat",
			[]byte("req"), []byte("resp"), 200, time.Second, "", nil)
	}

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder:  rec,
		sessionManager: sessionMgr,
	})
	defer server.Close()

	// Test with limit=2
	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "/http?limit=2")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	traces, ok := result["traces"].([]any)
	if !ok {
		t.Fatal("response missing 'traces' array")
	}
	if len(traces) > 2 {
		t.Errorf("expected at most 2 traces with limit=2, got %d", len(traces))
	}
}

func TestDebugSessionHTTP_LimitExceedsMax(t *testing.T) {
	rec := debug.NewRecorder(10)
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	// Record enough traces
	for i := 0; i < 150; i++ {
		rec.Record(sess.ID, "p1", "POST", "/v1/chat",
			[]byte("req"), []byte("resp"), 200, time.Second, "", nil)
	}

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder:  rec,
		sessionManager: sessionMgr,
	})
	defer server.Close()

	// Test with limit=200 (should be capped at 100)
	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "/http?limit=200")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	traces, ok := result["traces"].([]any)
	if !ok {
		t.Fatal("response missing 'traces' array")
	}
	if len(traces) > 100 {
		t.Errorf("expected at most 100 traces (capped), got %d", len(traces))
	}
}

func TestDebugSessionHTTP_NoRecorder_New(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		sessionManager: sessionMgr,
	})
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/sessions/" + sess.ID + "/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDebugSessionHTTP_EmptySessionID(t *testing.T) {
	rec := debug.NewRecorder(10)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	// Empty session ID in path
	resp, err := http.Get(server.URL + "/api/debug/sessions//http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// With empty session ID, the route won't match, but just check no crash
	_ = resp.StatusCode
}

func TestDebugRuntime_WithRunService(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	// Create a session so session_count is 1
	_, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}

	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})

	server := newTestServerWithRunService(t, t.TempDir(), sessionMgr, nil, runSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/runtime")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	for _, k := range []string{"version", "up_since", "active_run_count", "session_count", "recorded_http_traces", "config_summary"} {
		if _, ok := body[k]; !ok {
			t.Errorf("response missing key %q", k)
		}
	}

	// active_run_count should be 0
	if n, ok := body["active_run_count"].(float64); ok && n != 0 {
		t.Errorf("active_run_count = %v, want 0", n)
	}
	// session_count should be 1
	if n, ok := body["session_count"].(float64); ok && n != 1 {
		t.Errorf("session_count = %v, want 1", n)
	}
	// active_sessions should not be present when no active runs
	if _, ok := body["active_sessions"]; ok {
		t.Errorf("active_sessions should not be present when no active runs")
	}
}

func TestDebugConfig_WithRunService(t *testing.T) {
	runSvc := runner.NewRunService(runner.RunServiceDeps{})

	server := newTestServerWithRunService(t, t.TempDir(), nil, nil, runSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Should have completed_run_retention_ms when RunService is set
	if _, ok := body["completed_run_retention_ms"]; !ok {
		t.Errorf("response missing 'completed_run_retention_ms'")
	}

	// Verify completed_run_retention_ms is a positive number
	if v, ok := body["completed_run_retention_ms"].(float64); ok && v <= 0 {
		t.Errorf("completed_run_retention_ms = %v, want > 0", v)
	}
}

func TestDebugHTTP_QueryParams(t *testing.T) {
	rec := debug.NewRecorder(10)
	sessionMgr := session.NewManager(10, t.TempDir())
	sess1, _ := sessionMgr.Create("browser1")
	sess2, _ := sessionMgr.Create("browser2")

	// Record traces with different sessions and providers
	rec.Record(sess1.ID, "provider-a", "POST", "/v1/chat", []byte("req1"), []byte("resp1"), 200, time.Second, "", nil)
	rec.Record(sess1.ID, "provider-b", "POST", "/v1/chat", []byte("req2"), []byte("resp2"), 200, time.Second, "", nil)
	rec.Record(sess2.ID, "provider-a", "POST", "/v1/chat", []byte("req3"), []byte("resp3"), 200, time.Second, "", nil)
	rec.Record(sess2.ID, "provider-b", "POST", "/v1/chat", []byte("req4"), []byte("resp4"), 200, time.Second, "", nil)

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder:  rec,
		sessionManager: sessionMgr,
	})
	defer server.Close()

	// Test with session_id filter
	resp, err := http.Get(server.URL + "/api/debug/http?session_id=" + sess1.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	traces, ok := result["traces"].([]any)
	if !ok {
		t.Fatal("response missing 'traces'")
	}
	if len(traces) != 2 {
		t.Errorf("expected 2 traces for session filter, got %d", len(traces))
	}

	// Test with provider_id filter
	resp2, err := http.Get(server.URL + "/api/debug/http?provider_id=provider-a")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	var result2 map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&result2); err != nil {
		t.Fatal(err)
	}
	traces2, ok := result2["traces"].([]any)
	if !ok {
		t.Fatal("response missing 'traces'")
	}
	if len(traces2) != 2 {
		t.Errorf("expected 2 traces for provider filter, got %d", len(traces2))
	}

	// Test with limit
	resp3, err := http.Get(server.URL + "/api/debug/http?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp3.StatusCode, http.StatusOK)
	}

	var result3 map[string]any
	if err := json.NewDecoder(resp3.Body).Decode(&result3); err != nil {
		t.Fatal(err)
	}
	traces3, ok := result3["traces"].([]any)
	if !ok {
		t.Fatal("response missing 'traces'")
	}
	if len(traces3) > 1 {
		t.Errorf("expected at most 1 trace with limit=1, got %d", len(traces3))
	}
}

func TestDebugHTTP_NoRecorder_New(t *testing.T) {
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

func TestDebugHTTPByID_EmptyID(t *testing.T) {
	rec := debug.NewRecorder(10)
	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder: rec,
	})
	defer server.Close()

	// Missing trace_id in URL - the route won't match, just check no crash
	resp, err := http.Get(server.URL + "/api/debug/http/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_ = resp.StatusCode
}

func TestDebugHTTPByID_NotFound_New(t *testing.T) {
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

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "trace not found" {
		t.Errorf("error = %q, want %q", body["error"], "trace not found")
	}
}

func TestDebugHTTPByID_NoRecorder(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug/http/trace_1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestDebugHTTPByID_Success(t *testing.T) {
	rec := debug.NewRecorder(10)
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, _ := sessionMgr.Create("test-browser")

	// Record a trace
	rec.Record(sess.ID, "p1", "POST", "/v1/chat", []byte("request body"), []byte("response body"), 200, time.Second, "", nil)

	server := newTestServerWithOptions(t, t.TempDir(), testServerOptions{
		debugRecorder:  rec,
		sessionManager: sessionMgr,
	})
	defer server.Close()

	// First get the list to find the trace ID
	resp, err := http.Get(server.URL + "/api/debug/http")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	var list map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	traces, ok := list["traces"].([]any)
	if !ok || len(traces) == 0 {
		t.Fatal("no traces in list")
	}
	trace := traces[0].(map[string]any)
	traceID, ok := trace["id"].(string)
	if !ok || traceID == "" {
		t.Fatal("trace missing id")
	}

	// Now fetch by ID
	resp2, err := http.Get(server.URL + "/api/debug/http/" + traceID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp2.StatusCode, http.StatusOK)
	}

	var single map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&single); err != nil {
		t.Fatal(err)
	}
	if single["id"] != traceID {
		t.Errorf("trace id = %v, want %v", single["id"], traceID)
	}
	if single["request_body"] != "request body" {
		t.Errorf("request_body = %v, want %q", single["request_body"], "request body")
	}
}

func TestDebugUmbrella_WithRunService(t *testing.T) {
	sessionMgr := session.NewManager(10, t.TempDir())
	sess, err := sessionMgr.Create("test-browser")
	if err != nil {
		t.Fatal(err)
	}
	sessionMgr.UpdateStatus(sess.ID, session.StatusRunning)

	rec := debug.NewRecorder(10)
	rec.Record(sess.ID, "p1", "POST", "/v1/chat", []byte("req"), []byte("resp"), 200, time.Second, "", nil)

	runSvc := runner.NewRunService(runner.RunServiceDeps{
		UISessionMgr: sessionMgr,
	})

	server := newTestServerWithRunService(t, t.TempDir(), sessionMgr, rec, runSvc)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	// Check top-level structure
	for _, k := range []string{"version", "up_since", "runtime", "sessions", "http_traces", "config_summary"} {
		if _, ok := body[k]; !ok {
			t.Errorf("umbrella response missing key %q", k)
		}
	}

	// Check sessions list has 1 running session with run info
	sessions, ok := body["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %#v", sessions)
	}
	s := sessions[0].(map[string]any)
	if s["status"] != "running" {
		t.Errorf("session status = %v, want running", s["status"])
	}
	run, ok := s["run"].(map[string]any)
	if !ok {
		t.Fatal("session missing 'run' field")
	}
	if run["status"] != "running" {
		t.Errorf("run.status = %v, want running", run["status"])
	}

	// Check runtime has expected fields
	runtime, ok := body["runtime"].(map[string]any)
	if !ok {
		t.Fatal("umbrella missing 'runtime'")
	}
	for _, k := range []string{"version", "up_since", "active_run_count", "session_count", "recorded_http_traces", "config_summary"} {
		if _, ok := runtime[k]; !ok {
			t.Errorf("runtime missing key %q", k)
		}
	}

	// Check http_traces
	httpTraces, ok := body["http_traces"].(map[string]any)
	if !ok {
		t.Fatal("umbrella missing 'http_traces'")
	}
	traces, ok := httpTraces["traces"].([]any)
	if !ok || len(traces) != 1 {
		t.Errorf("expected 1 trace, got %#v", traces)
	}
	inFlight, ok := httpTraces["in_flight"].([]any)
	if !ok {
		t.Fatal("http_traces missing 'in_flight'")
	}
	if len(inFlight) != 0 {
		t.Errorf("expected 0 in_flight traces, got %d", len(inFlight))
	}
}

func TestDebugUmbrella_NoRecorder(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/debug")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}

	httpTraces, ok := body["http_traces"].(map[string]any)
	if !ok {
		t.Fatal("umbrella missing 'http_traces'")
	}
	traces, ok := httpTraces["traces"].([]any)
	if !ok {
		t.Fatal("http_traces missing 'traces'")
	}
	if traces == nil || len(traces) != 0 {
		t.Errorf("expected empty traces list, got %#v", traces)
	}
}

func TestDebugConfig_WithSavedConfigFile(t *testing.T) {
	t.Skip("TODO: need to write config file and reference it from test server")
}

func TestDebugRuntime_WithActiveSessions(t *testing.T) {
	// This test requires an active run, which is complex to set up.
	// The basic runtime test with RunService (but no active runs)
	// is covered by TestDebugRuntime_WithRunService.
	t.Skip("Full active run test requires complex setup")
}
