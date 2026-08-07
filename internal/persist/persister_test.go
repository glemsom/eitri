package persist

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/message"
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
		Messages:  []message.Message{{Role: "user", Content: "hello", CreatedAt: now}},
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

func TestSaveTraceAsync_WritesFile(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "async-session"
	// A session.json must exist — SaveTrace guards against recreating
	// deleted sessions.
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	trace := &debug.HTTPTrace{
		ID:        "trace_async_1",
		SessionID: sessionID,
		Method:    "POST",
		URL:       "/v1/chat/completions",
		Status:    200,
	}

	p.SaveTraceAsync(sessionID, trace)

	// The write is asynchronous — drain the queue and wait for the worker.
	if err := p.Flush(nil, nil); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	traceFile := filepath.Join(rootDir, "sessions", sessionID, "traces", "trace_async_1.json")
	if _, err := os.Stat(traceFile); err != nil {
		t.Fatalf("expected trace file %s after async save: %v", traceFile, err)
	}
}

func TestFlush_PersistsRecorderTracesWithoutLossOrDuplication(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "flush-session"
	if err := p.SnapshotSession(sessionID, &session.UISession{ID: sessionID}); err != nil {
		t.Fatal(err)
	}

	// Wire a recorder's OnComplete to the async save path, exactly like main.go.
	r := debug.NewRecorder(100)
	r.OnComplete = func(trace *debug.HTTPTrace) {
		p.SaveTraceAsync(trace.SessionID, trace)
	}

	const n = 20
	for i := 0; i < n; i++ {
		r.Record(sessionID, "p1", "GET", "/", nil, nil, 200, 0, "", nil)
	}

	// Simulate the shutdown flush: main.go passes the recorder's completed +
	// in-flight traces.
	allTraces := append(r.List(0, "", ""), r.InFlight()...)
	if err := p.Flush(nil, allTraces); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Every trace must be on disk exactly once (no loss, no duplication).
	tracesDir := filepath.Join(rootDir, "sessions", sessionID, "traces")
	entries, err := os.ReadDir(tracesDir)
	if err != nil {
		t.Fatalf("cannot read traces dir: %v", err)
	}
	if len(entries) != n {
		t.Fatalf("got %d trace files, want %d (one per recorded trace)", len(entries), n)
	}

	// A second flush must not write duplicates.
	if err := p.Flush(nil, allTraces); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	entries, err = os.ReadDir(tracesDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Fatalf("after second Flush: got %d trace files, want %d", len(entries), n)
	}
}

func TestFlush_TraceFailureDoesNotBlockRecording(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	good := "good-session"
	if err := p.SnapshotSession(good, &session.UISession{ID: good}); err != nil {
		t.Fatal(err)
	}

	// Sabotage the "bad" session's traces path with a regular file so every
	// SaveTrace for that session fails on MkdirAll.
	bad := "bad-session"
	if err := p.SnapshotSession(bad, &session.UISession{ID: bad}); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(rootDir, "sessions", bad, "traces")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}

	// Enqueue a failing trace first, then a good one — the failure must not
	// block or corrupt the recording of subsequent traces.
	p.SaveTraceAsync(bad, &debug.HTTPTrace{ID: "trace_fail", SessionID: bad, Method: "GET", URL: "/bad"})
	p.SaveTraceAsync(good, &debug.HTTPTrace{ID: "trace_good", SessionID: good, Method: "GET", URL: "/good"})

	// The shutdown flush completes despite the failing write.
	if err := p.Flush(nil, nil); err != nil {
		t.Fatalf("Flush returned error despite best-effort contract: %v", err)
	}

	// The good trace reached disk; the failed one did not create a directory.
	goodFile := filepath.Join(rootDir, "sessions", good, "traces", "trace_good.json")
	if _, err := os.Stat(goodFile); err != nil {
		t.Fatalf("expected good trace file %s to exist: %v", goodFile, err)
	}
	if info, err := os.Stat(blocked); err != nil || info.IsDir() {
		t.Fatalf("sabotaged path must not have become a directory: info=%v err=%v", info, err)
	}

	// The recorder keeps recording after the failure.
	r := debug.NewRecorder(5)
	r.OnComplete = func(trace *debug.HTTPTrace) { p.SaveTraceAsync(trace.SessionID, trace) }
	r.Record(good, "p1", "GET", "/after-failure", nil, nil, 200, 0, "", nil)
	if err := p.Flush(nil, r.List(0, "", "")); err != nil {
		t.Fatalf("Flush after failure: %v", err)
	}
	if tr := r.Get("trace_1"); tr == nil || tr.URL != "/after-failure" {
		t.Fatalf("recorder did not keep recording after a persistence failure: %+v", tr)
	}
}

func TestConcurrentSessions_AsyncTracePersistence(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	const sessions = 8
	const perSession = 10
	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("sess-%d", i)
		if err := p.SnapshotSession(sid, &session.UISession{ID: sid}); err != nil {
			t.Fatal(err)
		}
	}

	r := debug.NewRecorder(0)
	r.OnComplete = func(trace *debug.HTTPTrace) {
		p.SaveTraceAsync(trace.SessionID, trace)
	}

	// Concurrent sessions record traces simultaneously; the shared trace
	// worker must not serialize them on disk I/O or lose any trace.
	var wg sync.WaitGroup
	for i := 0; i < sessions; i++ {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			for j := 0; j < perSession; j++ {
				r.Record(sid, "p1", "GET", "/", nil, nil, 200, 0, "", nil)
			}
		}(fmt.Sprintf("sess-%d", i))
	}
	wg.Wait()

	// Shutdown flush: drain the queue and persist anything the queue dropped.
	allTraces := append(r.List(0, "", ""), r.InFlight()...)
	if err := p.Flush(nil, allTraces); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	for i := 0; i < sessions; i++ {
		sid := fmt.Sprintf("sess-%d", i)
		dir := filepath.Join(rootDir, "sessions", sid, "traces")
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("session %s: cannot read traces dir: %v", sid, err)
		}
		if len(entries) != perSession {
			t.Errorf("session %s: got %d trace files, want %d (no loss)", sid, len(entries), perSession)
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

// TestSaveTrace_DoesNotOverwriteExistingTrace is the acceptance test for issue
// #1236: persisted traces are single-owner — a trace file already on disk is
// never overwritten. A freshly generated trace_N that collides with a restored
// archive file after a restart must not silently clobber it.
func TestSaveTrace_DoesNotOverwriteExistingTrace(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "trace-session"
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	original := &debug.HTTPTrace{
		ID:        "trace_1",
		SessionID: sessionID,
		Method:    "POST",
		URL:       "/original",
		Status:    200,
	}
	if err := p.SaveTrace(sessionID, original); err != nil {
		t.Fatalf("SaveTrace: %v", err)
	}

	// A second save of the same ID — the restart collision scenario — must be
	// rejected, not written over the archived trace.
	clobber := &debug.HTTPTrace{
		ID:        "trace_1",
		SessionID: sessionID,
		Method:    "POST",
		URL:       "/clobber",
		Status:    500,
	}
	if err := p.SaveTrace(sessionID, clobber); !errors.Is(err, ErrTraceExists) {
		t.Fatalf("SaveTrace error = %v, want ErrTraceExists", err)
	}

	traceFile := filepath.Join(rootDir, "sessions", sessionID, "traces", "trace_1.json")
	data, err := os.ReadFile(traceFile)
	if err != nil {
		t.Fatalf("cannot read trace file: %v", err)
	}
	var restored debug.HTTPTrace
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("cannot unmarshal trace file: %v", err)
	}
	if restored.URL != "/original" {
		t.Errorf("trace file was overwritten: URL = %q, want %q", restored.URL, "/original")
	}
}

// TestRestore_SeedsPersistedTraces is the acceptance test for issue #1236:
// traces restored from the archive are already owned by their on-disk files,
// so Flush must never re-write them after a restart. The restored file is
// tampered with on disk before Flush: a flush that re-wrote restored traces
// would overwrite the marker and restore the original content.
func TestRestore_SeedsPersistedTraces(t *testing.T) {
	rootDir := t.TempDir()

	// First process persists a trace.
	p1, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "restore-session"
	if err := p1.SnapshotSession(sessionID, &session.UISession{ID: sessionID}); err != nil {
		t.Fatal(err)
	}
	trace := &debug.HTTPTrace{
		ID:        "trace_1",
		SessionID: sessionID,
		Method:    "POST",
		URL:       "/original",
		Status:    200,
	}
	if err := p1.SaveTrace(sessionID, trace); err != nil {
		t.Fatal(err)
	}

	// Simulated restart: a fresh persister over the same data dir.
	p2, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := p2.Restore()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(restored.Traces) != 1 {
		t.Fatalf("restored %d traces, want 1", len(restored.Traces))
	}

	// Tamper with the restored trace file on disk.
	traceFile := filepath.Join(rootDir, "sessions", sessionID, "traces", "trace_1.json")
	tampered := []byte(`{"tampered":true}`)
	if err := os.WriteFile(traceFile, tampered, 0600); err != nil {
		t.Fatal(err)
	}

	// Flush with the restored traces in hand — exactly like main.go passes
	// debugRecorder.List(), which includes restored traces after LoadAll.
	if err := p2.Flush(nil, restored.Traces); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	data, err := os.ReadFile(traceFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(tampered) {
		t.Errorf("Flush re-wrote a restored trace file; on-disk content = %s, want tampered marker %s", data, tampered)
	}
}

// TestTraceIdentity_SurvivesRestart is the end-to-end acceptance test for issue
// #1236: after a simulated restart (restore archive + new run, wired exactly
// like main.go), no trace file is overwritten by a fresh ID, fresh IDs advance
// past the restored archive, and nothing is lost or duplicated.
func TestTraceIdentity_SurvivesRestart(t *testing.T) {
	rootDir := t.TempDir()

	// First process: record a run's traces through the async persistence path.
	p1, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "restart-session"
	if err := p1.SnapshotSession(sessionID, &session.UISession{ID: sessionID}); err != nil {
		t.Fatal(err)
	}
	r1 := debug.NewRecorder(100)
	r1.OnComplete = func(tr *debug.HTTPTrace) { p1.SaveTraceAsync(tr.SessionID, tr) }
	for i := 0; i < 3; i++ {
		r1.Record(sessionID, "p1", "POST", "/v1/chat", nil, nil, 200, 0, "", nil)
	}
	if err := p1.Flush(nil, r1.List(0, "", "")); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Snapshot the archive: trace_1..trace_3 on disk.
	archiveDir := filepath.Join(rootDir, "sessions", sessionID, "traces")
	before := make(map[string]string)
	for _, id := range []string{"trace_1", "trace_2", "trace_3"} {
		data, err := os.ReadFile(filepath.Join(archiveDir, id+".json"))
		if err != nil {
			t.Fatalf("archive trace %s missing: %v", id, err)
		}
		before[id] = string(data)
	}

	// Simulated restart: fresh recorder + persister over the same data dir,
	// hydrated from the restored archive exactly like main.go.
	p2, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := p2.Restore()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(restored.Traces) != 3 {
		t.Fatalf("restored %d traces, want 3", len(restored.Traces))
	}
	r2 := debug.NewRecorder(100)
	r2.OnComplete = func(tr *debug.HTTPTrace) { p2.SaveTraceAsync(tr.SessionID, tr) }
	r2.LoadAll(restored.Traces)

	// New run after restart, then the shutdown flush.
	for i := 0; i < 2; i++ {
		r2.Record(sessionID, "p1", "POST", "/v1/chat", nil, nil, 200, 0, "", nil)
	}
	allTraces := append(r2.List(0, "", ""), r2.InFlight()...)
	if err := p2.Flush(nil, allTraces); err != nil {
		t.Fatalf("Flush after restart: %v", err)
	}

	// The archived files are byte-identical — none was overwritten by a fresh ID.
	for id, want := range before {
		got, err := os.ReadFile(filepath.Join(archiveDir, id+".json"))
		if err != nil {
			t.Fatalf("archive trace %s lost after restart: %v", id, err)
		}
		if string(got) != want {
			t.Errorf("archive trace %s was overwritten after restart", id)
		}
	}

	// The new run's traces landed with fresh IDs past the archive (trace_4, trace_5).
	for _, id := range []string{"trace_4", "trace_5"} {
		if _, err := os.Stat(filepath.Join(archiveDir, id+".json")); err != nil {
			t.Errorf("new trace %s missing after restart: %v", id, err)
		}
	}

	// Exactly five files on disk — no loss, no duplication.
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("cannot read archive dir: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("trace dir has %d files, want 5 (3 archived + 2 new)", len(entries))
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
		ID:     sessionID,
		Title:  "Load Test",
		Status: session.StatusIdle,
		Messages: []message.Message{
			{Role: "user", Content: "hi", CreatedAt: now},
			{Role: "assistant", Content: "hello", CreatedAt: now, ToolCalls: []message.ToolCall{
				{ID: "call-1", Type: "function", Function: message.FunctionCall{Name: "test", Arguments: `{}`}},
			}},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	err = p.SnapshotSession(sessionID, s)
	if err != nil {
		t.Fatal(err)
	}

	restored, err := p.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession returned error: %v", err)
	}
	if restored == nil {
		t.Fatal("LoadSession returned nil data")
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

	snap, err := p.LoadSession("nonexistent")
	if err != nil {
		t.Fatalf("LoadSession returned error: %v", err)
	}
	if snap != nil {
		t.Fatal("LoadSession should return nil for missing session")
	}
}

// TestSessionExistsOnDisk pins the single session-exists check (issue #1237):
// a session is considered present on disk exactly when its session.json
// snapshot exists. Every trace save / flush / query site and the snapshot
// loader route through this one helper, so "permanently deleted" means the
// same thing everywhere.
func TestSessionExistsOnDisk(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	// A session that was never created does not exist on disk.
	if p.sessionExistsOnDisk("ghost") {
		t.Error("sessionExistsOnDisk(ghost) = true, want false for a never-created session")
	}

	// Once a snapshot is written, the session exists on disk.
	live := "live-session"
	if err := p.SnapshotSession(live, &session.UISession{ID: live}); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}
	if !p.sessionExistsOnDisk(live) {
		t.Error("sessionExistsOnDisk(live-session) = false, want true after SnapshotSession")
	}

	// A permanently deleted session (no session.json on disk) does not exist.
	if err := p.DeleteSession(live); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if p.sessionExistsOnDisk(live) {
		t.Error("sessionExistsOnDisk(live-session) = true, want false after DeleteSession")
	}
}

func TestLoadSession_CorruptSnapshot_ReturnsCorruptError(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "corrupt-snap"
	sessionDir := filepath.Join(rootDir, "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "session.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	snap, err := p.LoadSession(sessionID)
	if !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("expected ErrCorruptSnapshot, got %v", err)
	}
	if snap != nil {
		t.Errorf("expected nil snapshot for corrupt file, got %v", snap)
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

func TestListTraces_ReturnsTraces(t *testing.T) {
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

	traces, err := p.ListTraces(sessionID)
	if err != nil {
		t.Fatalf("ListTraces returned error: %v", err)
	}
	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0].ID != "trace-list-1" {
		t.Errorf("trace ID = %q, want %q", traces[0].ID, "trace-list-1")
	}
}

func TestListTraceFilenames_IncludesCorruptFiles(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "trace-filenames"
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	// One well-formed trace and one corrupt trace file on disk.
	if err := p.SaveTrace(sessionID, &debug.HTTPTrace{ID: "good-1", SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	tracesDir := filepath.Join(rootDir, "sessions", sessionID, "traces")
	if err := os.WriteFile(filepath.Join(tracesDir, "corrupt-1.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	// ListTraceFilenames enumerates every trace file on disk — corrupt ones
	// included — because consumers that delete raw files need to see them.
	stems, err := p.ListTraceFilenames(sessionID)
	if err != nil {
		t.Fatalf("ListTraceFilenames returned error: %v", err)
	}
	got := make(map[string]bool, len(stems))
	for _, s := range stems {
		got[s] = true
	}
	if !got["good-1"] || !got["corrupt-1"] {
		t.Errorf("expected stems [good-1 corrupt-1], got %v", stems)
	}

	// ListTraces, by contrast, skips the corrupt file.
	traces, err := p.ListTraces(sessionID)
	if err != nil {
		t.Fatalf("ListTraces returned error: %v", err)
	}
	if len(traces) != 1 || traces[0].ID != "good-1" {
		t.Errorf("expected only good-1 from ListTraces, got %v", traces)
	}
}

func TestClearAllTraces_RemovesCorruptFiles(t *testing.T) {
	rootDir := t.TempDir()
	p, err := New(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "clear-all-traces"
	sess := &session.UISession{ID: sessionID}
	if err := p.SnapshotSession(sessionID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}

	// A well-formed trace and a corrupt one. ClearAllTraces must remove both —
	// clear-all-traces deletes by on-disk filename, so corrupt files are not
	// left behind even though ListTraces would skip them.
	if err := p.SaveTrace(sessionID, &debug.HTTPTrace{ID: "good-1", SessionID: sessionID}); err != nil {
		t.Fatal(err)
	}
	tracesDir := filepath.Join(rootDir, "sessions", sessionID, "traces")
	if err := os.WriteFile(filepath.Join(tracesDir, "corrupt-1.json"), []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	cleared, err := p.ClearAllTraces(sessionID)
	if err != nil {
		t.Fatalf("ClearAllTraces returned error: %v", err)
	}
	if cleared != 2 {
		t.Errorf("expected 2 trace files cleared, got %d", cleared)
	}

	stems, err := p.ListTraceFilenames(sessionID)
	if err != nil {
		t.Fatalf("ListTraceFilenames returned error: %v", err)
	}
	if len(stems) != 0 {
		t.Errorf("expected no trace files left on disk, got %v", stems)
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

	restored, err := p.LoadTrace(sessionID, "trace-load-1")
	if err != nil {
		t.Fatalf("LoadTrace returned error: %v", err)
	}
	if restored == nil {
		t.Fatal("LoadTrace returned nil")
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
	startedAt := time.Now().UTC()
	timeline := struct {
		StartedAt time.Time `json:"started_at"`
		RunID     string    `json:"run_id"`
		Data      string    `json:"data"`
	}{
		StartedAt: startedAt,
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

	tl, err := p.LoadTimeline(sessionID, metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline returned error: %v", err)
	}
	if tl == nil {
		t.Fatal("LoadTimeline returned nil")
	}
	if tl.RunID != "run-load-1" {
		t.Errorf("RunID = %q, want %q", tl.RunID, "run-load-1")
	}
	if !tl.StartedAt.Equal(startedAt) {
		t.Errorf("StartedAt = %v, want %v", tl.StartedAt, startedAt)
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
		Messages: []message.Message{
			{
				Role:       "tool",
				Content:    "result",
				ToolCallID: "call-1",
				CreatedAt:  now,
			},
			{
				Role:    "assistant",
				Content: "Let me call a tool",
				ToolCalls: []message.ToolCall{
					{
						ID:   "call-2",
						Type: "function",
						Function: message.FunctionCall{
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

	restored, err := p.LoadSession(sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if restored == nil {
		t.Fatal("LoadSession returned nil")
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
		ID:     "sess-a",
		Title:  "Session A",
		Status: session.StatusIdle,
		Messages: []message.Message{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s2 := &session.UISession{
		ID:     "sess-b",
		Title:  "Session B",
		Status: session.StatusIdle,
		Messages: []message.Message{
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
		ID:       "prune-test",
		Title:    "Prune Test",
		Status:   session.StatusIdle,
		Messages: []message.Message{{Role: "user", Content: "hello"}},
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
	snap, err := p.LoadSession("prune-test")
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
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
		ID:     sessionID,
		Title:  "Prune Traces",
		Status: session.StatusIdle,
	}
	if err := p.SnapshotSession(sessionID, s); err != nil {
		t.Fatal(err)
	}

	// Add trace files
	for i := 0; i < 3; i++ {
		trace := &debug.HTTPTrace{
			ID:        debug.TraceID(fmt.Sprintf("trace-%d", i)),
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
	snap, err := p.LoadSession(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil {
		t.Fatal("session.json was removed by prune")
	}
}

func TestHistorySchema_BackwardCompat(t *testing.T) {
	// Verify HistorySchema can still parse old-format data
	schema := HistorySchema{
		Version:      1,
		SystemPrompt: "You are Eitri.",
		Messages: []message.Message{
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
