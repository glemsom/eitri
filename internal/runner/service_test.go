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

	"github.com/glemsom/eitri/internal/history"

	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

func newRunServiceForTest(t *testing.T) (*RunService, *uisession.Manager) {
	t.Helper()

	uiSessionMgr := uisession.NewManager(10)
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
	svc.parentCfgMu.Lock()
	svc.parentCfgs["session-1"] = cfg
	svc.parentCfgMu.Unlock()

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
	svc.subMu.Lock()
	_, exists1 := svc.subAgents[taskID1]
	_, exists2 := svc.subAgents[taskID2]
	svc.subMu.Unlock()

	if !exists1 {
		t.Fatal("taskID1 not found in subAgents")
	}
	if !exists2 {
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
	svc.parentCfgMu.Lock()
	svc.parentCfgs["session-1"] = cfg
	svc.parentCfgMu.Unlock()

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

	// Sub-agent should be cancelled
	svc.subMu.Lock()
	rec, exists := svc.subAgents[taskID]
	svc.subMu.Unlock()
	if !exists {
		t.Fatal("sub-agent record not found after cancel")
	}
	// Wait briefly for goroutine to process cancellation
	time.Sleep(100 * time.Millisecond)
	svc.subMu.Lock()
	status := rec.Status
	svc.subMu.Unlock()
	if status != subAgentCancelled {
		t.Fatalf("sub-agent status = %q, want %q", status, subAgentCancelled)
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

	svc.parentCfgMu.Lock()
	stored, exists := svc.parentCfgs["session-1"]
	svc.parentCfgMu.Unlock()

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
	svc.parentCfgMu.Lock()
	svc.parentCfgs[parent.ID] = cfg
	svc.parentCfgMu.Unlock()

	taskID, err := svc.SpawnSubAgent(context.Background(), parent.ID, "research X", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}

	svc.subMu.Lock()
	rec, exists := svc.subAgents[taskID]
	svc.subMu.Unlock()
	if !exists {
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
	svc.parentCfgMu.Lock()
	svc.parentCfgs["session-1"] = cfg
	svc.parentCfgMu.Unlock()

	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "test task", 5)
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}

	svc.subMu.Lock()
	rec, exists := svc.subAgents[taskID]
	svc.subMu.Unlock()
	if !exists {
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
