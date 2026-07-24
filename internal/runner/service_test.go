package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"

	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

func newRunServiceForTest(t *testing.T) (*RunService, *uisession.Manager) {
	t.Helper()

	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)

	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: historyMgr,
	})
	return svc, uiSessionMgr
}

func TestStartRun_InjectsRepoInstructions(t *testing.T) {
	dir := t.TempDir()
	content := "# My Repo\n\nBe specific."
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  dir,
	}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	hist := svc.historySessionMgr.History("session-1")
	if len(hist) < 1 {
		t.Fatal("no history entries")
	}
	sysPrompt := hist[0].Content
	if !strings.Contains(sysPrompt, "<repository_instructions>") {
		t.Fatalf("system prompt should contain <repository_instructions> tags, got:\n%s", sysPrompt)
	}
	if !strings.Contains(sysPrompt, content) {
		t.Fatalf("system prompt should contain AGENTS.md content %q, got:\n%s", content, sysPrompt)
	}
}

func TestRunService_HistoryPreservedViaDeps(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
	}

	// Create and populate a session
	svc.historySessionMgr.Create("test-session")
	svc.historySessionMgr.SetSystemPrompt("test-session", "You are Eitri.")
	svc.historySessionMgr.AppendUser("test-session", "Hi, my name is Glenn")
	svc.historySessionMgr.AppendAssistant("test-session", "Hello Glenn!", nil)

	hist1 := svc.historySessionMgr.History("test-session")
	if len(hist1) != 3 {
		t.Fatalf("History length after first run = %d, want 3 (sys + user + asst)", len(hist1))
	}

	// StartRun with explicit config — must not replace historySessionMgr
	_, err := svc.StartRun(context.Background(), "test-session", "another message", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("test-session")

	// History should still be preserved
	if svc.historySessionMgr == nil {
		t.Fatal("historySessionMgr is nil after StartRun")
	}
	hist2 := svc.historySessionMgr.History("test-session")
	if len(hist2) < 3 {
		t.Fatalf("History length after StartRun = %d, want >= 3 (preserved). "+
			"Bug: StartRun replaced historySessionMgr", len(hist2))
	}
	// Verify the history order is preserved
	// After StartRun: system prompt + user(Hi) + asst(Hello Glenn!) + user(another message)
	// The original messages should still be there (StartRun appends, doesn't replace)
	if len(hist2) != 4 {
		t.Fatalf("History length after StartRun = %d, want 4 (sys+user+asst+user)", len(hist2))
	}
	if hist2[1].Content != "Hi, my name is Glenn" {
		t.Errorf("First user message changed: got %q", hist2[1].Content)
	}
	if hist2[2].Content != "Hello Glenn!" {
		t.Errorf("Assistant message changed: got %q", hist2[2].Content)
	}
	if hist2[3].Content != "another message" {
		t.Errorf("Second user message = %q, want 'another message'", hist2[3].Content)
	}
}

func TestRunService_StartRun_RejectsDuplicateActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	_, err = svc.StartRun(context.Background(), "session-1", "hello again", cfg)
	if err == nil {
		t.Fatal("expected error for duplicate run, got nil")
	}
}

func TestRunService_Subscribe_ReplaysHistoryForLateJoiners(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	state := svc.ActiveRun("session-1")
	if state == nil {
		t.Fatal("no active run after StartRun")
	}

	state.SSE.Broadcast(runstate.SSEEvent{Type: "token", Content: "hello world"})

	_, ch, ok := svc.Subscribe("session-1")
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	var found bool
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before receiving event")
			}
			if evt.Type == "token" && evt.Content == "hello world" {
				found = true
				continue
			}
			if evt.Type == "done" || evt.Type == "error" {
				goto check
			}
		case <-deadline:
			goto check
		}
	}
check:
	if !found {
		t.Fatal("late subscriber did not receive broadcast event from history")
	}
}

func TestRunService_Cancel_StopsRunAndBroadcastsDone(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	_, ch, ok := svc.Subscribe("session-1")
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	canceled := svc.Cancel("session-1")
	if !canceled {
		t.Fatal("Cancel returned false")
	}

	// Verify: done event is received AND channel is then closed
	deadline := time.After(2 * time.Second)
	gotDone := false
loop:
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				break loop
			}
			if evt.Type == "done" {
				gotDone = true
				continue
			}
		case <-deadline:
			t.Fatal("timed out waiting for channel close")
		}
	}
	if !gotDone {
		t.Fatal("channel closed without receiving done event")
	}

	// Verify: new Subscribe returns ok=false after cancel
	if _, _, ok := svc.Subscribe("session-1"); ok {
		t.Fatal("Subscribe returned ok=true after cancel, expected false")
	}
}

func TestRunService_CancelAll_StopsAllRuns(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	for _, id := range []string{"session-a", "session-b"} {
		_, err := svc.StartRun(context.Background(), id, "hello", cfg)
		if err != nil {
			t.Fatalf("StartRun(%s): %v", id, err)
		}
	}

	svc.CancelAll()

	if svc.ActiveRun("session-a") != nil {
		t.Fatal("session-a still active after CancelAll")
	}
	if svc.ActiveRun("session-b") != nil {
		t.Fatal("session-b still active after CancelAll")
	}
}

func TestRunService_AuthCallback(t *testing.T) {
	var capturedKey string
	var capturedAuth json.RawMessage
	persistCalled := false

	svc, _ := newRunServiceForTest(t)

	svc.SetPersistAuth(func(apiKey string, providerAuth json.RawMessage) error {
		persistCalled = true
		capturedKey = apiKey
		capturedAuth = providerAuth
		return nil
	})

	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}
	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	_ = persistCalled
	_ = capturedKey
	_ = capturedAuth
}

func TestRunService_StartRun_EmptyConfig_ReturnsError(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q, want 'not configured'", err.Error())
	}
}

func TestRunService_Subscribe_ReturnsOkFalseForNoActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	_, _, ok := svc.Subscribe("nonexistent")
	if ok {
		t.Fatal("Subscribe should return ok=false for session with no active run")
	}
}

func TestRunService_Cancel_ReturnsFalseForNoActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	if svc.Cancel("nonexistent") {
		t.Fatal("Cancel should return false for session with no active run")
	}
}

func TestRunService_MaxTurnsMessage(t *testing.T) {
	msg := runstate.MaxTurnsMessage(1)
	if !strings.Contains(msg, "max turns") {
		t.Fatalf("max turns message = %q, want max turns mention", msg)
	}
	if !strings.Contains(msg, "1") {
		t.Fatalf("max turns message = %q, want limit number", msg)
	}
}

func TestRunService_NotifySessionClosed_BroadcastsClosedEvent(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	_, ch, ok := svc.Subscribe("session-1")
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	svc.NotifySessionClosed("session-1", "Session closed")

	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if evt.Type == "closed" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for closed event")
		}
	}
}

// ── Sub-agent tests ─────────────────────────────────────────────────────────

func TestRunService_SpawnSubAgent_NoParentConfig(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	_, err := svc.SpawnSubAgent(context.Background(), "nonexistent-session", "test task", 5)
	if err == nil {
		t.Fatal("expected error for missing parent config")
	}
	if !strings.Contains(err.Error(), "no parent run config found") {
		t.Fatalf("error = %q, want 'no parent run config found'", err.Error())
	}
}

func TestRunService_SpawnSubAgent_ReturnsUniqueIDs(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Store a parent config
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg("session-1", cfg)


	taskID1, err := svc.SpawnSubAgent(context.Background(), "session-1", "task 1", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	if !strings.HasPrefix(taskID1, "task_") {
		t.Fatalf("task ID = %q, want 'task_...'", taskID1)
	}

	taskID2, err := svc.SpawnSubAgent(context.Background(), "session-1", "task 2", 10)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	if taskID1 == taskID2 {
		t.Fatal("expected unique task IDs")
	}
	// Verify records are stored
	rec1 := svc.subagents.getRecord(taskID1)
	rec2 := svc.subagents.getRecord(taskID2)

	if rec1 == nil {
		t.Fatal("taskID1 not found in subAgents")
	}
	if rec2 == nil {
		t.Fatal("taskID2 not found in subAgents")
}
}

func TestRunService_CollectSubAgents_Empty(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	results, err := svc.CollectSubAgents(context.Background(), nil)
	if err != nil {
		t.Fatalf("CollectSubAgents: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %v", results)
	}

	results, err = svc.CollectSubAgents(context.Background(), []string{})
	if err != nil {
		t.Fatalf("CollectSubAgents: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %v", results)
	}
}

func TestRunService_CollectSubAgents_UnknownID(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	_, err := svc.CollectSubAgents(context.Background(), []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown task ID")
	}
	if !strings.Contains(err.Error(), "unknown task_id") {
		t.Fatalf("error = %q, want 'unknown task_id'", err.Error())
	}
}

func TestRunService_CancelSubAgents_CancelsInFlight(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg("session-1", cfg)


	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "test task", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}

	// Cancel sub-agents for this session
	svc.CancelSubAgents("session-1")

	// Wait briefly for the cancellation to propagate
	time.Sleep(100 * time.Millisecond)

	// Collect should show cancelled status
	results, err := svc.CollectSubAgents(context.Background(), []string{taskID})
	if err != nil {
		t.Fatalf("CollectSubAgents: %v", err)
	}
	result, ok := results[taskID]
	if !ok {
		t.Fatalf("task %s not found in results", taskID)
	}
	if result.Status != "cancelled" {
		t.Fatalf("status = %q, want %q", result.Status, "cancelled")
	}
}

func TestRunService_Cancel_CancelsSubAgents(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model", Workspace: t.TempDir()}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Spawn a sub-agent (should now have parent config)
	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "sub task", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}

	// Cancel the parent run — should cascade to sub-agents
	svc.Cancel("session-1")

	rec := svc.subagents.getRecord(taskID)
	if rec == nil {
		t.Fatal("sub-agent record not found after cancel")
	}
	// Wait for sub-agent goroutine to finish
	<-rec.Done
	if rec.Status != subAgentCancelled {
		t.Fatalf("sub-agent status = %q, want %q", rec.Status, subAgentCancelled)
	}
}

func TestRunService_BuildBaseToolRegistry_ExcludesDelegateCollect(t *testing.T) {
	cfg := RunConfig{Workspace: t.TempDir()}
	reg := buildBaseToolRegistry(cfg, nil, nil, nil)

	// Must include basic tools
	for _, name := range []string{"bash", "glob", "grep", "read", "write", "edit", "render_mermaid_diagram", "web_fetch"} {
		if reg.Lookup(name) == nil {
			t.Errorf("base tool registry missing %q", name)
		}
	}

	// Must NOT include delegate/collect/quick_replies/skill
	for _, name := range []string{"delegate", "collect", "render_quick_replies", "skill"} {
		if reg.Lookup(name) != nil {
			t.Errorf("base tool registry should NOT include %q", name)
		}
	}
}

func TestRunService_ParentConfig_StoredOnStartRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model", Workspace: t.TempDir()}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	stored, exists := svc.subagents.GetParentCfg("session-1")


	if !exists {
		t.Fatal("parent config not stored")
	}
	if stored.ModelName != "test-model" {
		t.Fatalf("stored model = %q, want %q", stored.ModelName, "test-model")
	}
}

func TestRunService_SpawnSubAgent_CreatesChildSession(t *testing.T) {
	svc, uiMgr := newRunServiceForTest(t)

	// Create a parent session
	parent, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create parent: %v", err)
	}

	// Store parent config
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg(parent.ID, cfg)


	taskID, err := svc.SpawnSubAgent(context.Background(), parent.ID, "research X", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}

	rec := svc.subagents.getRecord(taskID)
	if rec == nil {
		t.Fatal("sub-agent record not found")
	}
	if rec.ChildSessionID == "" {
		t.Fatal("expected child session ID to be set")
	}

	child := uiMgr.Get(rec.ChildSessionID)
	if child == nil {
		t.Fatal("child session not found in UI manager")
	}
	if child.ParentID != parent.ID {
		t.Errorf("child ParentID = %q, want %q", child.ParentID, parent.ID)
	}
	if child.BrowserID != "browser-1" {
		t.Errorf("child BrowserID = %q, want %q", child.BrowserID, "browser-1")
	}
	if child.Status != uisession.StatusRunning {
		t.Errorf("child Status = %q, want %q", child.Status, uisession.StatusRunning)
	}
}

func TestRunService_SpawnSubAgent_NoUIManager_NoChildSession(t *testing.T) {
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      nil,
		HistorySessionMgr: nil,
	})

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    "http://test.local",
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg("session-1", cfg)


	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "test task", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}

	rec := svc.subagents.getRecord(taskID)
	if rec == nil {
		t.Fatal("sub-agent record not found")
	}
	if rec.ChildSessionID != "" {
		t.Error("expected no child session ID when uiSessionMgr is nil")
	}
}

func TestRunService_ActiveRunSSESnapshot_NoDataRaceWithCancel(t *testing.T) {
	t.Parallel()

	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Cancel("session-1")
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			snap := svc.ActiveRunSSESnapshot("session-1")
			if snap != nil {
				_ = snap.SubscriberCount
				_ = snap.ReplayCount
				_ = snap.History
			}
		}()
	}
	wg.Wait()
}

// ── Unsubscribe with no active run ───────────────────────────────────────────

func TestRunService_Unsubscribe_NoActiveRunIsNoop(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Should not panic when unsubscribing from a non-existent session
	svc.Unsubscribe("nonexistent", 42)
}

// ── ActiveRunCount ───────────────────────────────────────────────────────────

func TestRunService_ActiveRunCount_InitialZero(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	if got := svc.ActiveRunCount(); got != 0 {
		t.Fatalf("ActiveRunCount = %d, want 0", got)
	}
}

func TestRunService_ActiveRunCount_IncrementsAfterStartRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	if got := svc.ActiveRunCount(); got != 1 {
		t.Fatalf("ActiveRunCount = %d, want 1", got)
	}
}

func TestRunService_ActiveRunCount_DecrementsAfterCancel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	svc.Cancel("session-1")

	if got := svc.ActiveRunCount(); got != 0 {
		t.Fatalf("ActiveRunCount after Cancel = %d, want 0", got)
	}
}

// ── CloseSession ─────────────────────────────────────────────────────────────

func TestRunService_CloseSession_CancelsRunAndClosesHistory(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	// Create a history session
	svc.historySessionMgr.Create("session-1")

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Verify run is active
	if svc.ActiveRun("session-1") == nil {
		t.Fatal("expected active run before CloseSession")
	}

	err = svc.CloseSession("session-1")
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	// Verify run is cancelled
	if svc.ActiveRun("session-1") != nil {
		t.Fatal("run still active after CloseSession")
	}

	// Verify history session is closed
	if svc.historySessionMgr.History("session-1") != nil {
		t.Fatal("expected history session to be closed")
	}
}

func TestRunService_CloseSession_NoActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Create a history session but no active run
	svc.historySessionMgr.Create("session-1")

	err := svc.CloseSession("session-1")
	if err != nil {
		t.Fatalf("CloseSession with no active run: %v", err)
	}

	// History should still be closed
	if svc.historySessionMgr.History("session-1") != nil {
		t.Fatal("expected history session to be closed")
	}
}

func TestRunService_CloseSession_NilHistoryManager(t *testing.T) {
	// Use a service with nil history manager
	svc := NewRunService(RunServiceDeps{
		HistorySessionMgr: nil,
	})
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	err = svc.CloseSession("session-1")
	if err != nil {
		t.Fatalf("CloseSession with nil history manager: %v", err)
	}

	if svc.ActiveRun("session-1") != nil {
		t.Fatal("run still active after CloseSession with nil history manager")
	}
}

// ── ResolveConfirmation / HasPendingConfirmation ─────────────────────────────

func TestRunService_HasPendingConfirmation_ReturnsFalseForNoPending(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	if svc.HasPendingConfirmation("session-1") {
		t.Fatal("HasPendingConfirmation should be false for session with no pending confirmation")
	}
}

func TestRunService_ResolveConfirmation_ReturnsFalseForNoPending(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	if svc.ResolveConfirmation("session-1", "/tmp/test", true) {
		t.Fatal("ResolveConfirmation should return false for session with no pending confirmation")
	}
}

func TestRunService_ResolveConfirmation_ResolvesPending(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Set up a pending confirmation by calling confirmPath in a goroutine
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result *ConfirmationResult
	var confirmErr error
	done := make(chan struct{})

	go func() {
		result, confirmErr = svc.confirmPath(ctx, "session-1", "/tmp/test", "Allow access?")
		close(done)
	}()

	// Give the goroutine time to register the confirmation
	time.Sleep(50 * time.Millisecond)

	if !svc.HasPendingConfirmation("session-1") {
		t.Fatal("HasPendingConfirmation should be true after confirmPath called")
	}

	resolved := svc.ResolveConfirmation("session-1", "/tmp/test", true)
	if !resolved {
		t.Fatal("ResolveConfirmation should return true for pending confirmation")
	}

	<-done

	if confirmErr != nil {
		t.Fatalf("confirmPath error: %v", confirmErr)
	}
	if result == nil {
		t.Fatal("confirmPath result is nil")
	}
	if result.Path != "/tmp/test" {
		t.Errorf("result.Path = %q, want %q", result.Path, "/tmp/test")
	}
	if !result.Approved {
		t.Errorf("result.Approved = false, want true")
	}
}

func TestRunService_ResolveConfirmation_Denied(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result *ConfirmationResult
	var confirmErr error
	done := make(chan struct{})

	go func() {
		result, confirmErr = svc.confirmPath(ctx, "session-2", "/tmp/denied", "Allow?")
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	resolved := svc.ResolveConfirmation("session-2", "/tmp/denied", false)
	if !resolved {
		t.Fatal("ResolveConfirmation should return true")
	}

	<-done

	if confirmErr != nil {
		t.Fatalf("confirmPath error: %v", confirmErr)
	}
	if result == nil {
		t.Fatal("confirmPath result is nil")
	}
	if result.Path != "/tmp/denied" {
		t.Errorf("result.Path = %q, want %q", result.Path, "/tmp/denied")
	}
	if result.Approved {
		t.Errorf("result.Approved = true, want false")
	}
}

// ── BroadcastToBrowser / BrowserSubscribersCount ─────────────────────────────

func TestRunService_BroadcastToBrowser_DeliversToSubscribers(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	id, ch := svc.SubscribeBrowser("browser-1")
	defer svc.UnsubscribeBrowser("browser-1", id)

	evt := BrowserEvent{Type: "test-event", Data: "hello"}
	svc.BroadcastToBrowser("browser-1", evt)

	select {
	case received := <-ch:
		if received.Type != "test-event" {
			t.Errorf("event type = %q, want %q", received.Type, "test-event")
		}
		data, ok := received.Data.(string)
		if !ok || data != "hello" {
			t.Errorf("event data = %v, want %q", received.Data, "hello")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for browser event")
	}
}

func TestRunService_BrowserSubscribersCount_InitialZero(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	if got := svc.BrowserSubscribersCount("browser-1"); got != 0 {
		t.Fatalf("BrowserSubscribersCount = %d, want 0", got)
	}
}

func TestRunService_BrowserSubscribersCount_AfterSubscribe(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	id, _ := svc.SubscribeBrowser("browser-1")
	defer svc.UnsubscribeBrowser("browser-1", id)

	if got := svc.BrowserSubscribersCount("browser-1"); got != 1 {
		t.Fatalf("BrowserSubscribersCount = %d, want 1", got)
	}
}

func TestRunService_BrowserSubscribersCount_AfterUnsubscribe(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	id, _ := svc.SubscribeBrowser("browser-1")
	svc.UnsubscribeBrowser("browser-1", id)

	if got := svc.BrowserSubscribersCount("browser-1"); got != 0 {
		t.Fatalf("BrowserSubscribersCount after unsubscribe = %d, want 0", got)
	}
}

// ── LastBatchConversationContext / setBatchConversationContext ────────────────

func TestRunService_LastBatchConversationContext_NilInitially(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	if ctx := svc.LastBatchConversationContext(); ctx != nil {
		t.Fatal("LastBatchConversationContext should be nil initially")
	}
}

func TestRunService_SetAndGetBatchConversationContext(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	svc.setBatchConversationContext(&debug.ConversationContext{
		LastUserMessage:      "user msg",
		LastAssistantMessage: "assistant msg",
		TurnNumber:           5,
	})

	ctx := svc.LastBatchConversationContext()
	if ctx == nil {
		t.Fatal("LastBatchConversationContext should not be nil after set")
	}
	if ctx.LastUserMessage != "user msg" {
		t.Errorf("LastUserMessage = %q, want %q", ctx.LastUserMessage, "user msg")
	}
	if ctx.LastAssistantMessage != "assistant msg" {
		t.Errorf("LastAssistantMessage = %q, want %q", ctx.LastAssistantMessage, "assistant msg")
	}
	if ctx.TurnNumber != 5 {
		t.Errorf("TurnNumber = %d, want %d", ctx.TurnNumber, 5)
	}
}

func TestRunService_LastBatchConversationContext_Overwrite(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	svc.setBatchConversationContext(&debug.ConversationContext{
		LastUserMessage: "first",
		TurnNumber:      1,
	})
	svc.setBatchConversationContext(&debug.ConversationContext{
		LastUserMessage: "second",
		TurnNumber:      2,
	})

	ctx := svc.LastBatchConversationContext()
	if ctx.LastUserMessage != "second" {
		t.Errorf("LastUserMessage = %q, want %q", ctx.LastUserMessage, "second")
	}
	if ctx.TurnNumber != 2 {
		t.Errorf("TurnNumber = %d, want %d", ctx.TurnNumber, 2)
	}
}

// ── CompletedRunRetentionMs ──────────────────────────────────────────────────

func TestRunService_CompletedRunRetentionMs_ReturnsPositive(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	retention := svc.CompletedRunRetentionMs()
	if retention <= 0 {
		t.Fatalf("CompletedRunRetentionMs = %d, want positive", retention)
	}
	// The constant is 5 seconds = 5000ms
	if retention != 5000 {
		t.Errorf("CompletedRunRetentionMs = %d, want 5000", retention)
	}
}

// ── ActiveRunSSESnapshot nil for no active run ────────────────────────────────

func TestRunService_ActiveRunSSESnapshot_ReturnsNilForNoActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	snap := svc.ActiveRunSSESnapshot("nonexistent")
	if snap != nil {
		t.Fatal("ActiveRunSSESnapshot should be nil for session with no active run")
	}
}

func TestRunService_ActiveRunSSESnapshot_ReturnsSnapshotForActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	snap := svc.ActiveRunSSESnapshot("session-1")
	if snap == nil {
		t.Fatal("ActiveRunSSESnapshot should not be nil for active run")
	}
	if snap.Turns != 0 {
		t.Errorf("snap.Turns = %d, want 0", snap.Turns)
	}
}

func TestRunService_ActiveRunSSESnapshot_ReturnsNilAfterCancel(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	svc.Cancel("session-1")

	snap := svc.ActiveRunSSESnapshot("session-1")
	if snap != nil {
		t.Fatal("ActiveRunSSESnapshot should be nil after cancel")
	}
}

// ── NotifyAllStreamsClosed ───────────────────────────────────────────────────

func TestRunService_NotifyAllStreamsClosed_BroadcastsToAllActive(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: "http://test.local", APIKey: "test-key", ModelName: "test-model"}

	for _, id := range []string{"session-a", "session-b"} {
		_, err := svc.StartRun(context.Background(), id, "hello", cfg)
		if err != nil {
			t.Fatalf("StartRun(%s): %v", id, err)
		}
		defer svc.Cancel(id)
	}

	// Subscribe to both sessions
	_, chA, okA := svc.Subscribe("session-a")
	if !okA {
		t.Fatal("Subscribe session-a returned ok=false")
	}
	_, chB, okB := svc.Subscribe("session-b")
	if !okB {
		t.Fatal("Subscribe session-b returned ok=false")
	}

	svc.NotifyAllStreamsClosed("shutting down")

	checkClosed := func(t *testing.T, ch <-chan runstate.SSEEvent, name string) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if evt.Type == "closed" {
					return
				}
			case <-deadline:
				t.Fatalf("timed out waiting for closed event on %s", name)
			}
		}
	}

	checkClosed(t, chA, "session-a")
	checkClosed(t, chB, "session-b")
}

func TestRunService_NotifyAllStreamsClosed_NoActiveRunsIsNoop(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Should not panic when there are no active runs
	svc.NotifyAllStreamsClosed("shutting down")
}

// ── Setter methods ───────────────────────────────────────────────────────────

func TestRunService_SetSkillsService(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Setting to a new service should work
	svc.SetSkillsService(nil)
	if svc.skillsSvc != nil {
		t.Fatal("skillsSvc should be nil after SetSkillsService(nil)")
	}
}

func TestRunService_SetUISessionManager(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	newMgr := uisession.NewManager(5, t.TempDir())
	svc.SetUISessionManager(newMgr)

	if svc.uiSessionMgr != newMgr {
		t.Fatal("uiSessionMgr not updated after SetUISessionManager")
	}
}

func TestRunService_SetUISessionManager_Nil(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	svc.SetUISessionManager(nil)
	if svc.uiSessionMgr != nil {
		t.Fatal("uiSessionMgr should be nil after SetUISessionManager(nil)")
	}
}

func TestRunService_SetCrashDumpFunc(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	called := false
	svc.SetCrashDumpFunc(func(err error, stack []byte) {
		called = true
	})

	if svc.crashDumpFunc == nil {
		t.Fatal("crashDumpFunc should not be nil after SetCrashDumpFunc")
	}

	// Call the function to verify it works
	svc.crashDumpFunc(nil, nil)
	if !called {
		t.Fatal("crashDumpFunc was not called")
	}
}

func TestRunService_SetCrashDumpFunc_Nil(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	svc.SetCrashDumpFunc(nil)
	if svc.crashDumpFunc != nil {
		t.Fatal("crashDumpFunc should be nil after SetCrashDumpFunc(nil)")
	}
}

