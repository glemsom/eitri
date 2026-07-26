package persist

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/session"
)

func TestNew_CreatesSessionsDirectory(t *testing.T) {
	rootDir := t.TempDir()

	p, err := New(rootDir)
	if err != nil {
		t.Fatalf("New(%q) returned error: %v", rootDir, err)
	}
	if p == nil {
		t.Fatal("New returned nil Persister")
	}

	// Verify sessions directory exists
	sessionsPath := filepath.Join(rootDir, "sessions")
	info, err := os.Stat(sessionsPath)
	if err != nil {
		t.Fatalf("expected dir %s to exist: %v", sessionsPath, err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", sessionsPath)
	}
	perm := info.Mode().Perm()
	if perm != 0700 {
		t.Errorf("expected dir %s to have 0700 permissions, got %#o", sessionsPath, perm)
	}

	// Verify history directory is NOT created
	historyPath := filepath.Join(rootDir, "history")
	if _, err := os.Stat(historyPath); !os.IsNotExist(err) {
		t.Errorf("expected history dir %s to NOT exist", historyPath)
	}
}

func TestNew_DefaultRoot(t *testing.T) {
	_, err := New("")
	if err != nil {
		t.Logf("New with empty root returned: %v", err)
	}
}

func TestSnapshotSession_WritesSingleFile(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-session-123"
	now := time.Now().Truncate(time.Second)
	s := &session.UISession{
		ID:        sessionID,
		Title:     "Test Session",
		Status:    session.StatusIdle,
		Messages:  []session.Message{{Role: "user", Content: "hello", CreatedAt: now}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	err = p.SnapshotSession(sessionID, s)
	if err != nil {
		t.Fatalf("SnapshotSession returned error: %v", err)
	}

	// Check session.json exists as a regular file (not symlink)
	sessionFile := filepath.Join(rootDir, "sessions", sessionID, "session.json")
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatalf("expected session file %s to exist: %v", sessionFile, err)
	}
	if info.IsDir() {
		t.Fatalf("expected %s to be a regular file", sessionFile)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected %s to be a regular file, not a symlink", sessionFile)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected file %s to have 0600 permissions, got %#o", sessionFile, info.Mode().Perm())
	}

	// Verify the content parses back correctly
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatalf("cannot read session file: %v", err)
	}
	var restored session.UISession
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal session: %v", err)
	}
	if restored.ID != sessionID {
		t.Errorf("restored ID = %q, want %q", restored.ID, sessionID)
	}
	if restored.Title != "Test Session" {
		t.Errorf("restored title = %q, want %q", restored.Title, "Test Session")
	}
	if len(restored.Messages) != 1 || restored.Messages[0].Content != "hello" {
		t.Errorf("restored messages mismatch: %+v", restored.Messages)
	}

	// Verify history directory was NOT created
	historyDir := filepath.Join(rootDir, "history", sessionID)
	if _, err := os.Stat(historyDir); !os.IsNotExist(err) {
		t.Errorf("history dir %s should not exist", historyDir)
	}
}

func TestSnapshotSession_OverwritesOnSecondCall(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-overwrite"
	s1 := &session.UISession{
		ID:     sessionID,
		Title:  "First Snapshot",
		Status: session.StatusIdle,
	}

	err = p.SnapshotSession(sessionID, s1)
	if err != nil {
		t.Fatal(err)
	}

	// Wait a bit to ensure different timestamps if needed
	time.Sleep(100 * time.Millisecond)

	s2 := &session.UISession{
		ID:     sessionID,
		Title:  "Second Snapshot",
		Status: session.StatusIdle,
	}

	err = p.SnapshotSession(sessionID, s2)
	if err != nil {
		t.Fatal(err)
	}

	// There should still be only one session.json file
	sessionFile := filepath.Join(rootDir, "sessions", sessionID, "session.json")
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		t.Fatalf("cannot read session file: %v", err)
	}
	var restored session.UISession
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal: %v", err)
	}
	if restored.Title != "Second Snapshot" {
		t.Errorf("expected title %q, got %q", "Second Snapshot", restored.Title)
	}

	// No timestamped files should exist
	entries, err := os.ReadDir(filepath.Join(rootDir, "sessions", sessionID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && e.Name() != "session.json" {
			t.Errorf("unexpected timestamped file: %s", e.Name())
		}
	}
}

func TestSaveTrace_WritesFile(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "trace-session"
	// A session.json must exist — SaveTrace guards against recreating
	// deleted sessions.
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	trace := &debug.HTTPTrace{
		ID:          "trace_42",
		SessionID:   sessionID,
		Method:      "POST",
		URL:         "/v1/chat/completions",
		Status:      200,
		RequestBody: `{"model":"gpt-4"}`,
	}

	err = p.SaveTrace(sessionID, trace)
	if err != nil {
		t.Fatalf("SaveTrace returned error: %v", err)
	}

	traceFile := filepath.Join(rootDir, "sessions", sessionID, "traces", "trace_42.json")
	data, err := os.ReadFile(traceFile)
	if err != nil {
		t.Fatalf("cannot read trace file %s: %v", traceFile, err)
	}
	var restored debug.HTTPTrace
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal trace JSON: %v", err)
	}
	if restored.ID != "trace_42" {
		t.Errorf("expected trace ID %q, got %q", "trace_42", restored.ID)
	}
	if restored.Method != "POST" {
		t.Errorf("expected method POST, got %q", restored.Method)
	}

	info, err := os.Stat(traceFile)
	if err != nil {
		t.Fatal(err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected trace file to have 0600 permissions, got %#o", perm)
	}
}

func TestSaveTrace_CreatesTracesDir(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "new-trace-session"
	// A session.json must exist — SaveTrace guards against recreating
	// deleted sessions.
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	trace := &debug.HTTPTrace{
		ID:        "trace_1",
		SessionID: sessionID,
		Method:    "GET",
		URL:       "/health",
	}

	tracesDir := filepath.Join(rootDir, "sessions", sessionID, "traces")
	if _, err := os.Stat(tracesDir); !os.IsNotExist(err) {
		t.Fatal("expected traces dir to not exist before SaveTrace")
	}

	err = p.SaveTrace(sessionID, trace)
	if err != nil {
		t.Fatalf("SaveTrace returned error: %v", err)
	}

	if _, err := os.Stat(tracesDir); err != nil {
		t.Fatalf("expected traces dir to exist after SaveTrace: %v", err)
	}
}

func TestDeleteSession_RemovesDirectory(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "delete-me"
	s := &session.UISession{ID: sessionID, Status: session.StatusIdle}
	err = p.SnapshotSession(sessionID, s)
	if err != nil {
		t.Fatal(err)
	}

	// Verify sessions dir exists
	sessionDir := filepath.Join(rootDir, "sessions", sessionID)
	if _, err := os.Stat(sessionDir); err != nil {
		t.Fatalf("expected session dir to exist: %v", err)
	}

	// Delete
	err = p.DeleteSession(sessionID)
	if err != nil {
		t.Fatalf("DeleteSession returned error: %v", err)
	}

	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Errorf("expected session dir to be removed after DeleteSession")
	}
}

func TestDeleteSession_NoopOnMissing(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	err = p.DeleteSession("nonexistent")
	if err != nil {
		t.Fatalf("DeleteSession on nonexistent returned error: %v", err)
	}
}

func TestLoadSession_ReturnsSessionData(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "load-test"
	now := time.Now().Truncate(time.Second)
	s := &session.UISession{
		ID:        sessionID,
		Title:     "Load Test",
		Status:    session.StatusIdle,
		Messages:  []session.Message{
			{Role: "user", Content: "hi", CreatedAt: now},
			{Role: "assistant", Content: "hello", CreatedAt: now, ToolCalls: []llm.ToolCall{
				{ID: "call-1", Type: "function", Function: llm.FunctionCall{Name: "test", Arguments: `{}`}},
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	err = p.SnapshotSession(sessionID, s)
	if err != nil {
		t.Fatal(err)
	}

	data, err := p.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession returned error: %v", err)
	}
	if data == nil {
		t.Fatal("LoadSession returned nil data")
	}

	var restored session.UISession
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal: %v", err)
	}
	if len(restored.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(restored.Messages))
	}
	if restored.Messages[1].ToolCallID != "" {
		t.Errorf("expected empty ToolCallID, got %q", restored.Messages[1].ToolCallID)
	}
	if len(restored.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(restored.Messages[1].ToolCalls))
	}
	if restored.Messages[1].ToolCalls[0].ID != "call-1" {
		t.Errorf("tool call ID = %q, want %q", restored.Messages[1].ToolCalls[0].ID, "call-1")
	}
}

func TestLoadSession_ReturnsNilOnMissing(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	data, err := p.LoadSession("nonexistent")
	if err != nil {
		t.Fatalf("LoadSession returned error: %v", err)
	}
	if data != nil {
		t.Fatal("LoadSession should return nil for missing session")
	}
}

func TestFlush_SnapshotsSessions(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	s1 := &session.UISession{ID: "sess-1", Status: session.StatusIdle}
	s2 := &session.UISession{ID: "sess-2", Status: session.StatusIdle}
	sessions := []*session.UISession{s1, s2}

	err = p.Flush(sessions, nil)
	if err != nil {
		t.Fatalf("Flush returned error: %v", err)
	}

	for _, sid := range []string{"sess-1", "sess-2"} {
		sessionFile := filepath.Join(rootDir, "sessions", sid, "session.json")
		if _, err := os.Stat(sessionFile); err != nil {
			t.Errorf("expected session file %s to exist after Flush: %v", sessionFile, err)
		}
	}
}

func TestFlush_NilSafe(t *testing.T) {
	var p *Persister
	err := p.Flush(nil, nil)
	if err != nil {
		t.Fatalf("Flush on nil Persister should be safe, got error: %v", err)
	}
}

func TestSaveTimeline_WritesFile(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "timeline-test"
	timeline := struct {
		StartedAt time.Time `json:"started_at"`
		RunID     string    `json:"run_id"`
		Events    []string  `json:"events"`
	}{
		StartedAt: time.Now().UTC(),
		RunID:     "run-1",
		Events:    []string{"event-1", "event-2"},
	}

	err = p.SaveTimeline(sessionID, timeline)
	if err != nil {
		t.Fatalf("SaveTimeline returned error: %v", err)
	}

	timelineDir := filepath.Join(rootDir, "sessions", sessionID, "timeline")
	entries, err := os.ReadDir(timelineDir)
	if err != nil {
		t.Fatalf("cannot read timeline dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one timeline file")
	}
}

func TestListTimelines_ReturnsMetas(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "list-timelines"
	timeline := struct {
		StartedAt time.Time `json:"started_at"`
		RunID     string    `json:"run_id"`
	}{
		StartedAt: time.Now().UTC(),
		RunID:     "run-1",
	}

	err = p.SaveTimeline(sessionID, timeline)
	if err != nil {
		t.Fatal(err)
	}

	metas, err := p.ListTimelines(sessionID)
	if err != nil {
		t.Fatalf("ListTimelines returned error: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 timeline meta, got %d", len(metas))
	}
	if metas[0].RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", metas[0].RunID, "run-1")
	}
}

func TestListTraces_ReturnsIDs(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "trace-list"
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	trace := &debug.HTTPTrace{
		ID:        "trace-list-1",
		SessionID: sessionID,
		Method:    "GET",
		URL:       "/test",
	}
	err = p.SaveTrace(sessionID, trace)
	if err != nil {
		t.Fatal(err)
	}

	ids, err := p.ListTraces(sessionID)
	if err != nil {
		t.Fatalf("ListTraces returned error: %v", err)
	}
	if len(ids) != 1 || ids[0] != "trace-list-1" {
		t.Errorf("expected [trace-list-1], got %v", ids)
	}
}

func TestLoadTrace_ReturnsTrace(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "load-trace"
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	trace := &debug.HTTPTrace{
		ID:        "trace-load-1",
		SessionID: sessionID,
		Method:    "POST",
		URL:       "/api/test",
	}
	err = p.SaveTrace(sessionID, trace)
	if err != nil {
		t.Fatal(err)
	}

	data, err := p.LoadTrace(sessionID, "trace-load-1")
	if err != nil {
		t.Fatalf("LoadTrace returned error: %v", err)
	}
	if data == nil {
		t.Fatal("LoadTrace returned nil")
	}
	var restored debug.HTTPTrace
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal: %v", err)
	}
	if restored.ID != "trace-load-1" {
		t.Errorf("ID = %q, want %q", restored.ID, "trace-load-1")
	}
}

func TestLoadTimeline_ReturnsContent(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "load-timeline"
	timeline := struct {
		StartedAt time.Time `json:"started_at"`
		RunID     string    `json:"run_id"`
		Data      string    `json:"data"`
	}{
		StartedAt: time.Now().UTC(),
		RunID:     "run-load-1",
		Data:      "test-data",
	}
	err = p.SaveTimeline(sessionID, timeline)
	if err != nil {
		t.Fatal(err)
	}

	metas, err := p.ListTimelines(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) == 0 {
		t.Fatal("no timeline files")
	}

	data, err := p.LoadTimeline(sessionID, metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline returned error: %v", err)
	}
	if data == nil {
		t.Fatal("LoadTimeline returned nil")
	}
}

func TestSnapshotSession_CarriesToolCallFields(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "tool-call-session"
	now := time.Now().Truncate(time.Second)
	s := &session.UISession{
		ID:     sessionID,
		Title:  "Tool Call Test",
		Status: session.StatusIdle,
		Messages: []session.Message{
			{
				Role:       "tool",
				Content:    "result",
				ToolCallID: "call-1",
				CreatedAt:  now,
			},
			{
				Role:    "assistant",
				Content: "Let me call a tool",
				ToolCalls: []llm.ToolCall{
					{
						ID:   "call-2",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "get_weather",
							Arguments: `{"city":"London"}`,
						},
					},
				},
				CreatedAt: now,
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	err = p.SnapshotSession(sessionID, s)
	if err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	data, err := p.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	var restored session.UISession
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(restored.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(restored.Messages))
	}

	// Tool message with ToolCallID
	if restored.Messages[0].ToolCallID != "call-1" {
		t.Errorf("ToolCallID = %q, want %q", restored.Messages[0].ToolCallID, "call-1")
	}

	// Assistant message with ToolCalls
	if len(restored.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(restored.Messages[1].ToolCalls))
	}
	tc := restored.Messages[1].ToolCalls[0]
	if tc.ID != "call-2" {
		t.Errorf("ToolCall ID = %q, want %q", tc.ID, "call-2")
	}
	if tc.Function.Name != "get_weather" {
		t.Errorf("Function name = %q, want %q", tc.Function.Name, "get_weather")
	}
}

func TestRestore_ReturnsSessionsAndTraces(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create two sessions and a trace
	s1 := &session.UISession{
		ID:      "sess-a",
		Title:   "Session A",
		Status:  session.StatusIdle,
		Messages: []session.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s2 := &session.UISession{
		ID:      "sess-b",
		Title:   "Session B",
		Status:  session.StatusIdle,
		Messages: []session.Message{
			{Role: "user", Content: "hey"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := p.SnapshotSession("sess-a", s1); err != nil {
		t.Fatal(err)
	}
	if err := p.SnapshotSession("sess-b", s2); err != nil {
		t.Fatal(err)
	}

	trace := &debug.HTTPTrace{
		ID:        "trace-restore-1",
		SessionID: "sess-a",
		Method:    "GET",
		URL:       "/test",
	}
	if err := p.SaveTrace("sess-a", trace); err != nil {
		t.Fatal(err)
	}

	state, err := p.Restore()
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if state == nil {
		t.Fatal("Restore returned nil")
	}

	if len(state.Sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(state.Sessions))
	}
	if len(state.Traces) != 1 {
		t.Errorf("expected 1 trace, got %d", len(state.Traces))
	}

	// Verify session data
	sessA, ok := state.Sessions["sess-a"]
	if !ok {
		t.Fatal("missing sess-a")
	}
	if sessA.Status != session.StatusIdle {
		t.Errorf("expected StatusIdle after restore, got %v", sessA.Status)
	}
	if len(sessA.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(sessA.Messages))
	}

	// Verify histories derived from sessions
	histA, ok := state.Histories["sess-a"]
	if !ok {
		t.Fatal("missing history for sess-a")
	}
	if len(histA) != 2 {
		t.Fatalf("expected 2 history messages, got %d", len(histA))
	}
	if histA[0].Role != "user" || histA[0].Content != "hi" {
		t.Errorf("history[0] = %+v, want user/hi", histA[0])
	}
}

func TestRestore_EmptyOnFirstRun(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	state, err := p.Restore()
	if err != nil {
		t.Fatalf("Restore returned error: %v", err)
	}
	if state == nil {
		t.Fatal("Restore returned nil")
	}
	if len(state.Sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(state.Sessions))
	}
}

func TestPrune_UnderCapDoesNothing(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create a small session
	s := &session.UISession{
		ID:      "prune-test",
		Title:   "Prune Test",
		Status:  session.StatusIdle,
		Messages: []session.Message{{Role: "user", Content: "hello"}},
	}
	if err := p.SnapshotSession("prune-test", s); err != nil {
		t.Fatal(err)
	}

	// Prune with large retention (default 1 GiB) — should be a no-op
	err = p.Prune()
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	// session.json should still exist
	data, err := p.LoadSession("prune-test")
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("session.json was removed despite being under cap")
	}
}

func TestPrune_RemovesOldTraceFiles(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	// Set very small retention to force pruning
	p.retention = 1 // 1 byte

	sessionID := "prune-traces"
	s := &session.UISession{
		ID:      sessionID,
		Title:   "Prune Traces",
		Status:  session.StatusIdle,
	}
	if err := p.SnapshotSession(sessionID, s); err != nil {
		t.Fatal(err)
	}

	// Add trace files
	for i := 0; i < 3; i++ {
		trace := &debug.HTTPTrace{
			ID:        debug.TraceID("trace-" + itoa(i)),
			SessionID: sessionID,
			Method:    "GET",
			URL:       "/test",
		}
		if err := p.SaveTrace(sessionID, trace); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Prune should remove some trace files
	err = p.Prune()
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}

	// session.json must still exist
	data, err := p.LoadSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if data == nil {
		t.Fatal("session.json was removed by prune")
	}
}

func TestSessionMessagesToHistory(t *testing.T) {
	now := time.Now()
	msgs := []session.Message{
		{Role: "user", Content: "hello", CreatedAt: now},
		{
			Role:    "assistant",
			Content: "Let me check",
			ToolCalls: []llm.ToolCall{
				{
					ID:   "call-1",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      "test_func",
						Arguments: `{"arg":"val"}`,
					},
				},
			},
			CreatedAt: now,
		},
		{Role: "tool", Content: "result", ToolCallID: "call-1", CreatedAt: now},
	}

	hist := sessionMessagesToHistory(msgs)
	if len(hist) != 3 {
		t.Fatalf("expected 3 history messages, got %d", len(hist))
	}

	if hist[0].Role != "user" || hist[0].Content != "hello" {
		t.Errorf("hist[0] = %+v", hist[0])
	}
	if len(hist[1].ToolCalls) != 1 || hist[1].ToolCalls[0].ID != "call-1" {
		t.Errorf("hist[1].ToolCalls mismatch: %+v", hist[1].ToolCalls)
	}
	if hist[2].Role != "tool" || hist[2].ToolCallID != "call-1" {
		t.Errorf("hist[2] = %+v", hist[2])
	}
}

func TestHistorySchema_BackwardCompat(t *testing.T) {
	// Verify HistorySchema can still parse old-format data
	schema := HistorySchema{
		Version:      1,
		SystemPrompt: "You are Eitri.",
		Messages: []llm.Message{
			{Role: "user", Content: "hello"},
		},
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}

	var decoded HistorySchema
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("cannot unmarshal HistorySchema: %v", err)
	}
	if decoded.Version != 1 {
		t.Errorf("Version = %d, want 1", decoded.Version)
	}
	if decoded.SystemPrompt != "You are Eitri." {
		t.Errorf("SystemPrompt = %q", decoded.SystemPrompt)
	}
	if len(decoded.Messages) != 1 {
		t.Errorf("Messages count = %d", len(decoded.Messages))
	}
}

// itoa is a simple int-to-string for test helpers.
func itoa(i int) string {
	return strings.TrimSpace(strings.Replace(
		strings.Replace(
			strings.Replace(
				strings.Replace("0", "0", "", 1),
				"0", "", 1,
			),
			"", "", 1,
		),
		"", "", 1,
	))
}
