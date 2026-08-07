package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/timeline"
	uisession "github.com/glemsom/eitri/internal/session"
)

// newSubAgentPersistWiring creates a persister and a debug recorder whose
// OnComplete callback saves traces into that persister — mirroring how
// cmd/eitri/main.go wires trace persistence in both server and batch modes.
func newSubAgentPersistWiring(t *testing.T) (*persist.Persister, *debug.Recorder) {
	t.Helper()
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	rec := debug.NewRecorder(20)
	rec.OnComplete = func(trace *debug.HTTPTrace) {
		p.SaveTraceAsync(trace.SessionID, trace)
	}
	return p, rec
}

// waitForSubAgentDone blocks until the sub-agent goroutine for taskID has
// finished (its Done channel is closed), failing the test after a timeout.
func waitForSubAgentDone(t *testing.T, svc *RunService, taskID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec := svc.subagents.getRecord(taskID)
		if rec != nil {
			select {
			case <-rec.Done:
				return
			default:
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("sub-agent %s did not finish within timeout", taskID)
}

// loadSubAgentSessionSnapshot reads and parses the persisted child session
// snapshot for a sub-agent task ID.
func loadSubAgentSessionSnapshot(t *testing.T, p *persist.Persister, taskID string) *uisession.UISession {
	t.Helper()
	sess, err := p.LoadSession(taskID)
	if err != nil {
		t.Fatalf("LoadSession(%s): %v", taskID, err)
	}
	if sess == nil {
		t.Fatalf("no session snapshot found for sub-agent %s", taskID)
	}
	return sess
}

// loadSubAgentTimeline reads the single persisted timeline for a sub-agent
// task ID and returns it, failing if not exactly one timeline exists.
func loadSubAgentTimeline(t *testing.T, p *persist.Persister, taskID string) *timeline.Timeline {
	t.Helper()
	metas, err := p.ListTimelines(taskID)
	if err != nil {
		t.Fatalf("ListTimelines(%s): %v", taskID, err)
	}
	if len(metas) != 1 {
		t.Fatalf("got %d timeline(s) for sub-agent %s, want 1", len(metas), taskID)
	}
	tl, err := p.LoadTimeline(taskID, metas[0].Filename)
	if err != nil {
		t.Fatalf("LoadTimeline(%s): %v", taskID, err)
	}
	if tl == nil {
		t.Fatalf("LoadTimeline(%s) returned nil", taskID)
	}
	return tl
}

// TestSubAgentPersistsChildSession verifies that a completed sub-agent run
// (batch mode — no UI session manager) writes a child session snapshot with
// parent linkage and a task-derived title, plus its HTTP traces and timeline,
// all under ~/.eitri/sessions/<taskID>/ (issue #1041).
func TestSubAgentPersistsChildSession(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"sub-agent done"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	persister, rec := newSubAgentPersistWiring(t)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}
	svc.subagents.StoreParentCfg("parent-1", cfg)

	taskID, err := svc.SpawnSubAgent(context.Background(), "parent-1", "run whoami", 5, "")
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	waitForSubAgentDone(t, svc, taskID)

	// ── session.json: parent linkage, task-derived title, terminal idle ──
	sess := loadSubAgentSessionSnapshot(t, persister, taskID)
	if sess.ID != taskID {
		t.Errorf("snapshot ID = %q, want %q", sess.ID, taskID)
	}
	if sess.ParentID != "parent-1" {
		t.Errorf("snapshot ParentID = %q, want %q (parent linkage)", sess.ParentID, "parent-1")
	}
	if want := uisession.TitlePreview("run whoami"); sess.Title != want {
		t.Errorf("snapshot Title = %q, want %q (derived from task)", sess.Title, want)
	}
	if sess.Status != uisession.StatusIdle {
		t.Errorf("snapshot Status = %q, want %q", sess.Status, uisession.StatusIdle)
	}
	if sess.Workspace != cfg.Workspace {
		t.Errorf("snapshot Workspace = %q, want %q", sess.Workspace, cfg.Workspace)
	}
	if sess.SystemPrompt == "" {
		t.Error("snapshot SystemPrompt is empty")
	}

	// Messages: system prompt must be stored separately, not in the list;
	// the conversation must contain the task (user) and the answer (assistant).
	foundTask, foundAnswer := false, false
	for _, m := range sess.Messages {
		if m.Role == "system" {
			t.Error("system message must not appear in snapshot Messages (stored in SystemPrompt)")
		}
		if m.Role == "user" && strings.Contains(m.Content, "run whoami") {
			foundTask = true
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "sub-agent done") {
			foundAnswer = true
		}
	}
	if !foundTask {
		t.Error("snapshot Messages do not contain the delegated task as a user message")
	}
	if !foundAnswer {
		t.Error("snapshot Messages do not contain the sub-agent answer as an assistant message")
	}

	// ── traces: land under the child session's traces/ (previously dropped) ──
	_ = persister.Flush(nil, nil) // drain the async trace queue synchronously
	traces, err := persister.ListTraces(taskID)
	if err != nil {
		t.Fatalf("ListTraces(%s): %v", taskID, err)
	}
	if len(traces) == 0 {
		t.Fatalf("no HTTP traces persisted for sub-agent session %s", taskID)
	}

	// ── timeline: terminal state with correct termination reason ──
	tl := loadSubAgentTimeline(t, persister, taskID)
	if tl.SessionID != taskID {
		t.Errorf("timeline SessionID = %q, want %q", tl.SessionID, taskID)
	}
	if tl.Provider.Model != "test-model" || tl.Provider.ProviderID != "opencode_go" {
		t.Errorf("timeline Provider = %+v, want test-model/opencode_go", tl.Provider)
	}
	if tl.Termination == nil || tl.Termination.Reason != timeline.TerminationCompleted {
		t.Errorf("timeline Termination = %+v, want reason %q", tl.Termination, timeline.TerminationCompleted)
	}
}

// TestSubAgentPersistsChildSession_Error verifies that a failing sub-agent run
// writes a terminal snapshot with error status and a timeline with the error
// termination reason (issue #1041).
func TestSubAgentPersistsChildSession_Error(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"rate limit exceeded"}}`)
	}))
	defer llm.Close()

	persister, rec := newSubAgentPersistWiring(t)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
		// RetryPolicy zero value disables retries: single attempt, no sleeps.
	}
	svc.subagents.StoreParentCfg("parent-1", cfg)

	taskID, err := svc.SpawnSubAgent(context.Background(), "parent-1", "run whoami", 5, "")
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	waitForSubAgentDone(t, svc, taskID)

	rec2 := svc.subagents.getRecord(taskID)
	if rec2 == nil {
		t.Fatalf("sub-agent record for %s not found", taskID)
	}
	if rec2.Status != subAgentError {
		t.Errorf("record Status = %q, want %q", rec2.Status, subAgentError)
	}

	sess := loadSubAgentSessionSnapshot(t, persister, taskID)
	if sess.Status != uisession.StatusError {
		t.Errorf("terminal snapshot Status = %q, want %q", sess.Status, uisession.StatusError)
	}

	tl := loadSubAgentTimeline(t, persister, taskID)
	if tl.Termination == nil || tl.Termination.Reason != timeline.TerminationError {
		t.Errorf("timeline Termination = %+v, want reason %q", tl.Termination, timeline.TerminationError)
	}
}

// TestSubAgentPersistsChildSession_Cancelled verifies that cancelling a
// sub-agent run writes a terminal idle snapshot and a timeline with the
// cancelled termination reason (issue #1041).
func TestSubAgentPersistsChildSession_Cancelled(t *testing.T) {
	llmStarted := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedOnce.Do(func() { close(llmStarted) })
		// Block until the request is cancelled or the test releases us — the
		// run never produces output. (Cancelling the client request with an
		// unread body does not tear down the server-side connection promptly,
		// so the handler must be releasable for deterministic cleanup.)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		llm.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	persister, rec := newSubAgentPersistWiring(t)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}
	svc.subagents.StoreParentCfg("parent-1", cfg)

	taskID, err := svc.SpawnSubAgent(ctx, "parent-1", "run whoami", 5, "")
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	<-llmStarted // wait until the sub-agent's first LLM call is in flight
	cancel()     // cancel the run context → RunAgent returns context.Canceled

	waitForSubAgentDone(t, svc, taskID)

	rec2 := svc.subagents.getRecord(taskID)
	if rec2 == nil {
		t.Fatalf("sub-agent record for %s not found", taskID)
	}
	if rec2.Status != subAgentCancelled {
		t.Errorf("record Status = %q, want %q", rec2.Status, subAgentCancelled)
	}

	sess := loadSubAgentSessionSnapshot(t, persister, taskID)
	if sess.Status != uisession.StatusIdle {
		t.Errorf("terminal snapshot Status = %q, want %q (matches UI cancelled exit)", sess.Status, uisession.StatusIdle)
	}

	tl := loadSubAgentTimeline(t, persister, taskID)
	if tl.Termination == nil || tl.Termination.Reason != timeline.TerminationCancelled {
		t.Errorf("timeline Termination = %+v, want reason %q", tl.Termination, timeline.TerminationCancelled)
	}
}

// TestSubAgentPersistsChildSession_UIMode verifies that in UI mode the new
// persistence coexists with the existing sidebar child session: the child
// still appears nested under the parent in the UI manager (unchanged), and
// the persisted snapshot carries parent linkage plus the parent's browser ID.
func TestSubAgentPersistsChildSession_UIMode(t *testing.T) {
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"sub-agent done"},"index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer llm.Close()

	persister, rec := newSubAgentPersistWiring(t)
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	parent, err := uiSessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("uiSessionMgr.Create: %v", err)
	}
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}
	svc.subagents.StoreParentCfg(parent.ID, cfg)

	taskID, err := svc.SpawnSubAgent(context.Background(), parent.ID, "run whoami", 5, "")
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	waitForSubAgentDone(t, svc, taskID)

	// Existing UI behaviour unchanged: a child session is still nested under
	// the parent in the sidebar session manager.
	children := uiSessionMgr.ChildrenOf(parent.ID)
	if len(children) != 1 {
		t.Fatalf("got %d UI child session(s), want 1", len(children))
	}
	if children[0].ParentID != parent.ID {
		t.Errorf("UI child ParentID = %q, want %q", children[0].ParentID, parent.ID)
	}

	// New persistence: the child snapshot carries parent linkage + browser ID.
	sess := loadSubAgentSessionSnapshot(t, persister, taskID)
	if sess.ParentID != parent.ID {
		t.Errorf("snapshot ParentID = %q, want %q (parent linkage)", sess.ParentID, parent.ID)
	}
	if sess.BrowserID != "browser-1" {
		t.Errorf("snapshot BrowserID = %q, want %q", sess.BrowserID, "browser-1")
	}
	if sess.Title != uisession.TitlePreview("run whoami") {
		t.Errorf("snapshot Title = %q, want %q", sess.Title, uisession.TitlePreview("run whoami"))
	}
}

// TestBatchRun_SubAgentPersistence is the batch-mode end-to-end check: a
// parent batch run delegates a task, the sub-agent runs to completion, and
// its full artifact set (session snapshot + traces + timeline) lands under
// ~/.eitri/sessions/<taskID>/ while the parent run is unaffected (issue #1041).
func TestBatchRun_SubAgentPersistence(t *testing.T) {
	var mu sync.Mutex
	var parentReqs, subAgentReqs int

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		isSubAgent := bytes.Contains(body, []byte("You are performing the following task"))

		parentNum := 0
		if isSubAgent {
			mu.Lock()
			subAgentReqs++
			mu.Unlock()
		} else {
			mu.Lock()
			parentReqs++
			parentNum = parentReqs
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")

		switch {
		case isSubAgent:
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"sub-agent done"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case parentNum == 1:
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"delegate","arguments":"{\"task\":\"run whoami\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		default:
			// Parent turn 2: give the sub-agent a window to fire its request,
			// then wrap up the batch run.
			time.Sleep(200 * time.Millisecond)
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"delegation complete"},"index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer llm.Close()

	persister, rec := newSubAgentPersistWiring(t)
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: history.NewSessionManager(50),
		DebugRecorder:     rec,
		Persister:         persister,
	})
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}

	var buf bytes.Buffer
	content, err := svc.BatchRun(context.Background(), "delegate this task", cfg, &buf)
	if err != nil {
		t.Fatalf("batch run failed: %v", err)
	}
	if !strings.Contains(content, "delegation complete") {
		t.Fatalf("unexpected batch content: %q", content)
	}

	// Wait for the sub-agent's own run to finish (it may still be in flight
	// when BatchRun returns — the parent wraps up without collecting).
	taskID := findSpawnedSubAgentTaskID(t, svc)
	waitForSubAgentDone(t, svc, taskID)

	// The sub-agent session must be persisted with parent linkage to the
	// batch session (whose ID is internal; only the linkage is asserted).
	sess := loadSubAgentSessionSnapshot(t, persister, taskID)
	if sess.ParentID == "" {
		t.Error("sub-agent snapshot ParentID is empty, want batch session linkage")
	}
	if sess.Title != uisession.TitlePreview("run whoami") {
		t.Errorf("snapshot Title = %q, want %q", sess.Title, uisession.TitlePreview("run whoami"))
	}
	if sess.Status != uisession.StatusIdle {
		t.Errorf("snapshot Status = %q, want %q", sess.Status, uisession.StatusIdle)
	}

	// Traces must land under the child session's traces/ directory.
	_ = persister.Flush(nil, nil)
	traces, err := persister.ListTraces(taskID)
	if err != nil {
		t.Fatalf("ListTraces(%s): %v", taskID, err)
	}
	if len(traces) == 0 {
		t.Fatalf("no HTTP traces persisted for batch-mode sub-agent %s", taskID)
	}

	// Timeline with completed termination.
	tl := loadSubAgentTimeline(t, persister, taskID)
	if tl.Termination == nil || tl.Termination.Reason != timeline.TerminationCompleted {
		t.Errorf("timeline Termination = %+v, want reason %q", tl.Termination, timeline.TerminationCompleted)
	}
}

// findSpawnedSubAgentTaskID returns the task ID of the sub-agent spawned by
// the most recent run, scanning the sub-agent store for the record whose
// parent session is set.
func findSpawnedSubAgentTaskID(t *testing.T, svc *RunService) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		svc.subagents.mu.Lock()
		for taskID, rec := range svc.subagents.agents {
			if rec != nil && rec.SessionID != "" {
				svc.subagents.mu.Unlock()
				return taskID
			}
		}
		svc.subagents.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no sub-agent record found after batch run")
	return ""
}
