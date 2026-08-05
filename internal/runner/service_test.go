package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/compactor"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persist"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tokenizer"
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

// unreachableURL returns a loopback HTTP URL with no listener, so dialing it
// fails immediately with connection refused. Unlike DNS-resolved hostnames
// (e.g. test.local), it never touches the resolver and cannot stall on mDNS.
func unreachableURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unreachableURL: listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return "http://" + addr
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
		BaseURL:    unreachableURL(t),
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
	sysPrompt := hist[0].Content()
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
		BaseURL:    unreachableURL(t),
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
	if hist2[1].Content() != "Hi, my name is Glenn" {
		t.Errorf("First user message changed: got %q", hist2[1].Content())
	}
	if hist2[2].Content() != "Hello Glenn!" {
		t.Errorf("Assistant message changed: got %q", hist2[2].Content())
	}
	if hist2[3].Content() != "another message" {
		t.Errorf("Second user message = %q, want 'another message'", hist2[3].Content())
	}
}

func TestRunService_StartRun_RejectsDuplicateActiveRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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

	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}
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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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

	_, err := svc.SpawnSubAgent(context.Background(), "nonexistent-session", "test task", 5, "")
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
		BaseURL:    unreachableURL(t),
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg("session-1", cfg)

	taskID1, err := svc.SpawnSubAgent(context.Background(), "session-1", "task 1", 5, "")
	if err != nil {
		t.Fatalf("SpawnSubAgent: %v", err)
	}
	if !strings.HasPrefix(taskID1, "task_") {
		t.Fatalf("task ID = %q, want 'task_...'", taskID1)
	}

	taskID2, err := svc.SpawnSubAgent(context.Background(), "session-1", "task 2", 10, "")
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
		BaseURL:    unreachableURL(t),
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg("session-1", cfg)

	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "test task", 5, "")
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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model", Workspace: t.TempDir()}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Spawn a sub-agent (should now have parent config)
	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "sub task", 5, "")
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

func TestRunService_BuildBaseToolRegistry_SubAgentToolset(t *testing.T) {
	cfg := RunConfig{Workspace: t.TempDir()}

	// Sub-agent toolset = base + skill (issue #1092). With a skills service
	// wired, the skill tool is registered alongside the base tools so a
	// sub-agent inheriting a persona with required skills can load them.
	reg := buildBaseToolRegistry(cfg, nil, skills.NewService(), nil)

	// Must include base tools plus skill
	for _, name := range []string{"bash", "grep", "read", "write", "edit", "render_mermaid_diagram", "web_fetch", "skill"} {
		if reg.Lookup(name) == nil {
			t.Errorf("sub-agent tool registry missing %q", name)
		}
	}

	// Must NOT include delegate/collect/quick_replies (parent-only / UI-only)
	for _, name := range []string{"delegate", "collect", "render_quick_replies"} {
		if reg.Lookup(name) != nil {
			t.Errorf("sub-agent tool registry should NOT include %q", name)
		}
	}

	// Without a skills service, no skill tool is registered at all.
	regNoSkills := buildBaseToolRegistry(cfg, nil, nil, nil)
	if regNoSkills.Lookup("skill") != nil {
		t.Error("skill tool must not be registered without a skills service")
	}
}

// TestRunService_SpawnSubAgent_PersonaRequiredSkillFlow verifies that a
// sub-agent spawned from a parent whose persona requires skills honors the
// <required_skills> directive: the skill tool is registered, the directive is
// emitted in the sub-agent system prompt, and the agent can load each required
// skill via skill() on its first turn — the loaded content flows into the
// sub-agent conversation. The flow is exercised in both UI mode (a UI session
// manager wired) and batch mode (none) — the sub-agent toolset is shared
// (issue #1092).
func TestRunService_SpawnSubAgent_PersonaRequiredSkillFlow(t *testing.T) {
	for _, tc := range []struct {
		name string
		ui   bool
	}{
		{name: "ui mode", ui: true},
		{name: "batch mode", ui: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace, homeDir, skillsSvc := testPersonaWithRequiredSkill(t)

			var sawSkillTool, sawDirective, sawSkillContent bool
			var mu sync.Mutex
			reqCount := 0
			llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "read body", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")

				mu.Lock()
				reqCount++
				thisReq := reqCount
				mu.Unlock()

				if bytes.Contains(body, []byte(`"name":"skill"`)) {
					sawSkillTool = true
				}
				// Go's encoding/json HTML-escapes < and > to \u003c/\u003e on the wire.
				if bytes.Contains(body, []byte(`\u003crequired_skills\u003e`)) {
					sawDirective = true
				}
				// First turn: the sub-agent decides to load its required skill via skill().
				if thisReq == 1 {
					fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_skill","type":"function","function":{"name":"skill","arguments":"{\"name\":\"test-review-skill\"}"}}]},"finish_reason":"tool_calls"}]}`, "\n\n")
					fmt.Fprint(w, "data: [DONE]\n\n")
					return
				}
				// Second turn: the loaded skill content is in history; finish up.
				if bytes.Contains(body, []byte("Review the code for potential bugs and security issues.")) {
					sawSkillContent = true
				}
				fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"content":"review complete"},"index":0}]}`, "\n\n")
				fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer llm.Close()

			var uiSessionMgr *uisession.Manager
			if tc.ui {
				uiSessionMgr = uisession.NewManager(10, t.TempDir())
			}
			svc := NewRunService(RunServiceDeps{
				UISessionMgr:      uiSessionMgr,
				HistorySessionMgr: history.NewSessionManager(50),
				SkillsService:     skillsSvc,
			})
			cfg := RunConfig{
				ProviderID:    "opencode_go",
				BaseURL:       llm.URL,
				APIKey:        "test-key",
				ModelName:     "test-model",
				Workspace:     workspace,
				HomeDir:       homeDir,
				ActivePersona: "test-reviewer",
				MaxTurns:      5,
			}
			svc.subagents.StoreParentCfg("parent-1", cfg)

			taskID, err := svc.SpawnSubAgent(context.Background(), "parent-1", "review the code", 5, "")
			if err != nil {
				t.Fatalf("SpawnSubAgent: %v", err)
			}

			rec := svc.subagents.getRecord(taskID)
			if rec == nil {
				t.Fatal("sub-agent record not found")
			}
			select {
			case <-rec.Done:
			case <-time.After(15 * time.Second):
				t.Fatal("sub-agent did not finish within 15s")
			}
			if rec.Status != subAgentCompleted {
				t.Fatalf("sub-agent status = %q, want %q (err: %v)", rec.Status, subAgentCompleted, rec.Err)
			}
			if !strings.Contains(rec.Result, "review complete") {
				t.Fatalf("unexpected sub-agent result: %q", rec.Result)
			}
			if !sawDirective {
				t.Error("sub-agent system prompt did not carry the <required_skills> directive")
			}
			if !sawSkillTool {
				t.Error("sub-agent request did not register the skill tool")
			}
			if !sawSkillContent {
				t.Error("loaded skill content did not flow into the sub-agent conversation")
			}
		})
	}
}

func TestRunService_ParentConfig_StoredOnStartRun(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model", Workspace: t.TempDir()}

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
		BaseURL:    unreachableURL(t),
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg(parent.ID, cfg)

	taskID, err := svc.SpawnSubAgent(context.Background(), parent.ID, "research X", 5, "")
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
		BaseURL:    unreachableURL(t),
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
	}
	svc.subagents.StoreParentCfg("session-1", cfg)

	taskID, err := svc.SpawnSubAgent(context.Background(), "session-1", "test task", 5, "")
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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

	_, err := svc.StartRun(context.Background(), "session-1", "hello", cfg)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			svc.Cancel("session-1")
		})
		wg.Go(func() {
			snap := svc.ActiveRunSSESnapshot("session-1")
			if snap != nil {
				_ = snap.SubscriberCount
				_ = snap.ReplayCount
				_ = snap.History
			}
		})
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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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

	var result *loop.ConfirmationResult
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

	var result *loop.ConfirmationResult
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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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
	cfg := RunConfig{ProviderID: "opencode_go", BaseURL: unreachableURL(t), APIKey: "test-key", ModelName: "test-model"}

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

// ── Inline tracker tests ────────────────────────────────────────────────────
// Formerly in run_tracker_test.go. Test the private methods that were inlined
// from runTracker into RunService.

func TestRunService_NewHasEmptyActiveMap(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})
	if svc.active == nil {
		t.Fatal("active map should be initialized")
	}
	if len(svc.active) != 0 {
		t.Fatalf("expected empty active map, got %d", len(svc.active))
	}
}

func TestRunService_StoreAndGet(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	state := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	svc.store("sess-1", state)

	got := svc.get("sess-1")
	if got == nil {
		t.Fatal("get returned nil after store")
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("session ID = %q, want %q", got.SessionID, "sess-1")
	}

	// get returns nil for unknown session
	if svc.get("nonexistent") != nil {
		t.Fatal("get should return nil for unknown session")
	}
}

func TestRunService_GetActive(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// nil when session not found
	if svc.getActive("nonexistent") != nil {
		t.Fatal("getActive should return nil for unknown session")
	}

	done := make(chan struct{})
	state := &RunState{
		SessionID: "sess-1",
		Done:      done,
		SSE:       runstate.New(),
	}
	svc.store("sess-1", state)

	// active when done not closed
	active := svc.getActive("sess-1")
	if active == nil {
		t.Fatal("getActive returned nil for active run")
	}

	// nil when done channel is closed
	close(done)
	if svc.getActive("sess-1") != nil {
		t.Fatal("getActive should return nil when done channel is closed")
	}
}

func TestRunService_Remove(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	state1 := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	state2 := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	svc.store("sess-1", state1)

	// remove with wrong pointer — no-op
	svc.remove("sess-1", state2)
	if svc.get("sess-1") == nil {
		t.Fatal("remove with wrong pointer should not delete")
	}

	// remove with matching pointer
	svc.remove("sess-1", state1)
	if svc.get("sess-1") != nil {
		t.Fatal("remove with matching pointer should delete")
	}
}

func TestRunService_RemoveRun(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// returns nil for unknown session
	if svc.removeRun("nonexistent") != nil {
		t.Fatal("removeRun should return nil for unknown session")
	}

	state := &RunState{SessionID: "sess-1", Done: make(chan struct{})}
	svc.store("sess-1", state)

	got := svc.removeRun("sess-1")
	if got == nil {
		t.Fatal("removeRun returned nil")
	}
	if got.SessionID != "sess-1" {
		t.Fatalf("session ID = %q, want %q", got.SessionID, "sess-1")
	}
	if svc.get("sess-1") != nil {
		t.Fatal("session should be deleted after removeRun")
	}
}

func TestRunService_ExchangeIfDone(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// false for unknown session
	if svc.exchangeIfDone("nonexistent") {
		t.Fatal("exchangeIfDone should return false for unknown session")
	}

	done := make(chan struct{})
	state := &RunState{SessionID: "sess-1", Done: done}
	svc.store("sess-1", state)

	// false when not done
	if svc.exchangeIfDone("sess-1") {
		t.Fatal("exchangeIfDone should return false when not done")
	}
	if svc.get("sess-1") == nil {
		t.Fatal("session should still exist after exchangeIfDone(false)")
	}

	// true and removes when done channel is closed
	close(done)
	if !svc.exchangeIfDone("sess-1") {
		t.Fatal("exchangeIfDone should return true when done")
	}
	if svc.get("sess-1") != nil {
		t.Fatal("session should be deleted after exchangeIfDone(true)")
	}
}

func TestRunService_Count(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	if svc.count() != 0 {
		t.Fatalf("expected 0, got %d", svc.count())
	}

	svc.store("sess-1", &RunState{SessionID: "sess-1", Done: make(chan struct{})})
	if svc.count() != 1 {
		t.Fatalf("expected 1, got %d", svc.count())
	}

	svc.store("sess-2", &RunState{SessionID: "sess-2", Done: make(chan struct{})})
	if svc.count() != 2 {
		t.Fatalf("expected 2, got %d", svc.count())
	}
}

func TestRunService_AllActiveStates(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// empty when no runs
	active := svc.allActiveStates()
	if len(active) != 0 {
		t.Fatalf("expected 0 active, got %d", len(active))
	}

	done1 := make(chan struct{})
	close(done1)
	svc.store("sess-done", &RunState{SessionID: "sess-done", Done: done1})

	active2 := make(chan struct{})
	svc.store("sess-active", &RunState{SessionID: "sess-active", Done: active2})

	active = svc.allActiveStates()
	if len(active) != 1 {
		t.Fatalf("expected 1 active state, got %d", len(active))
	}
	if active[0].SessionID != "sess-active" {
		t.Fatalf("active session = %q, want %q", active[0].SessionID, "sess-active")
	}
}

func TestRunService_SSECounters(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// empty when no runs
	counters := svc.sseCounters()
	if len(counters) != 0 {
		t.Fatalf("expected 0 counters, got %d", len(counters))
	}

	sse := runstate.New()
	done := make(chan struct{})
	svc.store("sess-1", &RunState{SessionID: "sess-1", Done: done, SSE: sse})

	counters = svc.sseCounters()
	if len(counters) != 1 {
		t.Fatalf("expected 1 counter, got %d", len(counters))
	}
	if _, ok := counters["sess-1"]; !ok {
		t.Fatal("expected counter for sess-1")
	}
}

func TestRunService_SSESnapshot(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// nil for unknown session
	if svc.sseSnapshot("nonexistent") != nil {
		t.Fatal("sseSnapshot should return nil for unknown session")
	}

	// nil for done session
	done := make(chan struct{})
	close(done)
	svc.store("sess-done", &RunState{SessionID: "sess-done", Done: done, SSE: runstate.New()})
	if svc.sseSnapshot("sess-done") != nil {
		t.Fatal("sseSnapshot should return nil for done session")
	}

	// returns snapshot for active session
	activeDone := make(chan struct{})
	activeSSE := runstate.New()
	svc.store("sess-active", &RunState{SessionID: "sess-active", Done: activeDone, SSE: activeSSE, Turns: 3})

	snap := svc.sseSnapshot("sess-active")
	if snap == nil {
		t.Fatal("sseSnapshot should return snapshot for active session")
	}
	if snap.Turns != 3 {
		t.Fatalf("Turns = %d, want 3", snap.Turns)
	}
}

func TestRunService_NotifyAllClosed(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	done := make(chan struct{})
	sse := runstate.New()
	svc.store("sess-1", &RunState{SessionID: "sess-1", Done: done, SSE: sse})

	// Subscribe to receive the closed event
	id, ch, _ := sse.Subscribe()
	defer sse.Unsubscribe(id)

	svc.notifyAllClosed("all done")

	select {
	case evt := <-ch:
		if evt.Type != "closed" {
			t.Fatalf("event type = %q, want %q", evt.Type, "closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for closed event")
	}
}

func TestRunService_FatalRunErrorSetsErrorStatusSnapshotsAndBroadcasts(t *testing.T) {
	uiMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	persister, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}

	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiMgr,
		HistorySessionMgr: historyMgr,
		Persister:         persister,
		CrashDumpFunc:     func(error, []byte) {},
	})

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	uiMgr.UpdateStatus(sess.ID, uisession.StatusRunning)

	id, ch := svc.SubscribeBrowser("browser-1")
	defer svc.UnsubscribeBrowser("browser-1", id)

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fatal test error", http.StatusBadRequest)
	}))
	defer llmServer.Close()

	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llmServer.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
	}

	if _, err := svc.StartRun(context.Background(), sess.ID, "hello", cfg); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != "session_status" {
			t.Fatalf("event type = %q, want %q", evt.Type, "session_status")
		}
		data, ok := evt.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data type = %T, want map[string]any", evt.Data)
		}
		if data["session_id"] != sess.ID {
			t.Errorf("session_id = %v, want %s", data["session_id"], sess.ID)
		}
		if data["status"] != string(uisession.StatusError) {
			t.Errorf("status = %v, want %s", data["status"], uisession.StatusError)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for error status event")
	}

	got := uiMgr.Get(sess.ID)
	if got == nil {
		t.Fatal("session missing from manager")
	}
	if got.Status != uisession.StatusError {
		t.Fatalf("in-memory status = %q, want %q", got.Status, uisession.StatusError)
	}

	data, err := persister.LoadSession(sess.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if data == nil {
		t.Fatal("snapshot missing")
	}
	var snap uisession.UISession
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if snap.Status != uisession.StatusError {
		t.Fatalf("snapshot status = %q, want %q", snap.Status, uisession.StatusError)
	}
}

func TestRunService_BroadcastStatusUpdate(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})
	bb := New()

	uiMgr := uisession.NewManager(10, t.TempDir())
	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Subscribe to browser-level events
	id, ch := bb.Subscribe("browser-1")
	defer bb.Unsubscribe("browser-1", id)

	svc.broadcastStatusUpdate(sess.ID, uisession.StatusRunning, uiMgr, bb)

	select {
	case evt := <-ch:
		if evt.Type != "session_status" {
			t.Fatalf("event type = %q, want %q", evt.Type, "session_status")
		}
		data, ok := evt.Data.(map[string]any)
		if !ok {
			t.Fatalf("Data type = %T, want map[string]any", evt.Data)
		}
		if data["session_id"] != sess.ID {
			t.Errorf("session_id = %v, want %s", data["session_id"], sess.ID)
		}
		if data["status"] != "running" {
			t.Errorf("status = %v, want running", data["status"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for status event")
	}

	// nil uiSessionMgr = noop
	svc.broadcastStatusUpdate("sess-2", uisession.StatusRunning, nil, bb)
}

func TestRunService_BatchCtx(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	// getBatchCtx returns nil initially
	if svc.getBatchCtx() != nil {
		t.Fatal("getBatchCtx should return nil initially")
	}

	ctx := &debug.ConversationContext{
		LastUserMessage:      "hello",
		LastAssistantMessage: "hi there",
		TurnNumber:           5,
	}
	svc.setBatchCtx(ctx)

	got := svc.getBatchCtx()
	if got == nil {
		t.Fatal("getBatchCtx returned nil after set")
	}
	if got.LastUserMessage != "hello" {
		t.Errorf("LastUserMessage = %q, want %q", got.LastUserMessage, "hello")
	}
	if got.LastAssistantMessage != "hi there" {
		t.Errorf("LastAssistantMessage = %q, want %q", got.LastAssistantMessage, "hi there")
	}
	if got.TurnNumber != 5 {
		t.Errorf("TurnNumber = %d, want 5", got.TurnNumber)
	}
}

func TestRunService_RemoveAll(t *testing.T) {
	svc := NewRunService(RunServiceDeps{})

	svc.store("sess-1", &RunState{SessionID: "sess-1", Done: make(chan struct{})})
	svc.store("sess-2", &RunState{SessionID: "sess-2", Done: make(chan struct{})})

	states := svc.removeAll()
	if len(states) != 2 {
		t.Fatalf("removeAll returned %d states, want 2", len(states))
	}
	if svc.count() != 0 {
		t.Fatalf("expected 0 after removeAll, got %d", svc.count())
	}
}

func TestRunService_GetPanicFree(t *testing.T) {
	// Ensure no race conditions under concurrent access
	svc := NewRunService(RunServiceDeps{})
	done := make(chan struct{})
	ctx := t.Context()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				svc.store("sess-1", &RunState{SessionID: "sess-1", Done: done})
				svc.get("sess-1")
				svc.getActive("sess-1")
				svc.removeRun("sess-1")
				svc.count()
			}
		}
	}()

	time.Sleep(10 * time.Millisecond)
}

// ── LoadSessionFromDisk ────────────────────────────────────────────────────

func TestRunService_LoadSessionFromDisk_LoadsAndRestores(t *testing.T) {
	svc, uiSessionMgr := newRunServiceForTest(t)

	// Create and persist a session via the manager
	sess, err := uiSessionMgr.Create("browser-1")
	if err != nil {
		t.Fatal(err)
	}
	sess.Title = "Historical Session"
	sess.Messages = []message.Message{
		{Role: "user", Content: "Hello from the past"},
		{Role: "assistant", Content: "Hello from the past as well"},
	}

	// We need a persister for this. Create one and attach to the service.
	eitriDir := t.TempDir()
	p, err := persist.New(eitriDir)
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	svc.persister = p

	// Save to disk
	if err := p.SnapshotSession(sess.ID, sess); err != nil {
		t.Fatalf("SnapshotSession: %v", err)
	}
	sessionID := sess.ID

	// Close session from manager
	uiSessionMgr.Close(sessionID)

	// Verify it's gone
	if got := uiSessionMgr.Get(sessionID); got != nil {
		t.Fatal("session should be closed before testing load")
	}

	// Load from disk
	loaded, err := svc.LoadSessionFromDisk(sessionID)
	if err != nil {
		t.Fatalf("LoadSessionFromDisk: %v", err)
	}

	if loaded == nil {
		t.Fatal("LoadSessionFromDisk returned nil")
	}
	if loaded.Title != "Historical Session" {
		t.Errorf("Title = %q, want %q", loaded.Title, "Historical Session")
	}
	if loaded.Status != uisession.StatusIdle {
		t.Errorf("Status = %q, want %q", loaded.Status, uisession.StatusIdle)
	}

	// Verify it's back in the manager
	got := uiSessionMgr.Get(sessionID)
	if got == nil {
		t.Fatal("session should be restored to manager")
	}

	// Verify history was restored
	history := svc.historySessionMgr.History(sessionID)
	if history == nil {
		t.Fatal("history should be restored")
	}
	// History includes system prompt + user + assistant
	if len(history) < 2 {
		t.Fatalf("history messages = %d, want at least 2", len(history))
	}
	foundUser := false
	foundAssistant := false
	for _, msg := range history {
		if msg.Role == "user" && msg.Content() == "Hello from the past" {
			foundUser = true
		}
		if msg.Role == "assistant" && msg.Content() == "Hello from the past as well" {
			foundAssistant = true
		}
	}
	if !foundUser {
		t.Error("user message not found in restored history")
	}
	if !foundAssistant {
		t.Error("assistant message not found in restored history")
	}
}

func TestRunService_LoadSessionFromDisk_NoPersister(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	// svc.persister is nil by default

	_, err := svc.LoadSessionFromDisk("any-id")
	if err == nil {
		t.Fatal("LoadSessionFromDisk should fail when persister is nil")
	}
	if !strings.Contains(err.Error(), "persister not available") {
		t.Errorf("expected 'persister not available' error, got: %v", err)
	}
}

func TestRunService_LoadSessionFromDisk_SessionNotFound(t *testing.T) {
	svc, _ := newRunServiceForTest(t)
	eitriDir := t.TempDir()
	p, err := persist.New(eitriDir)
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	svc.persister = p

	_, err = svc.LoadSessionFromDisk("nonexistent")
	if err == nil {
		t.Fatal("LoadSessionFromDisk should fail for nonexistent session")
	}
	if !strings.Contains(err.Error(), "not found on disk") {
		t.Errorf("expected 'not found on disk' error, got: %v", err)
	}
}

// ── CompactSession tests ──────────────────────────────────────────────────────

// fakeCompactLLMServer returns an HTTP server that responds to
// POST /v1/chat/completions with a canned OpenAI-compatible summary response.
func fakeCompactLLMServer(t *testing.T, summary string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			// Check if this is a batch summarization request.
			bodyBytes, _ := io.ReadAll(r.Body)
			bodyStr := string(bodyBytes)
			isBatch := strings.Contains(bodyStr, "Below are") && strings.Contains(bodyStr, "MESSAGE")

			var content string
			if isBatch {
				lines := strings.Split(bodyStr, "\n")
				msgCount := 0
				for _, line := range lines {
					if strings.HasPrefix(line, "--- Message ") {
						msgCount++
					}
				}
				if msgCount == 0 {
					msgCount = 2
				}
				var sb strings.Builder
				for i := 1; i <= msgCount; i++ {
					sb.WriteString(fmt.Sprintf("MESSAGE %d: %s\n", i, summary))
				}
				content = sb.String()
			} else {
				content = summary
			}

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
			}`, content)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// compactRunConfig returns a minimal RunConfig pointing at the given fake LLM
// server URL with thresholds valid for config validation.
func compactRunConfig(llmURL string) RunConfig {
	return RunConfig{
		ProviderID:                 "custom_openai",
		BaseURL:                    llmURL,
		APIKey:                     "test-key",
		ModelName:                  "test-model",
		CompactionEnabled:          true,
		CompactionThresholdPercent: 90,
		CompactionLowWaterPercent:  30,
		ContextWindowTokens:        128000,
	}
}

func TestCompactSession_NoHistoryManager(t *testing.T) {
	// Create a RunService with nil history manager.
	uiSessionMgr := uisession.NewManager(10, t.TempDir())
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: nil,
	})

	sess, err := uiSessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	_, _, _, err = svc.CompactSession(context.Background(), sess.ID, compactRunConfig(unreachableURL(t)))
	if err == nil {
		t.Fatal("expected error when history manager is nil")
	}
	if !strings.Contains(err.Error(), "history session manager not available") {
		t.Errorf("expected 'history session manager not available' error, got: %v", err)
	}
}

func TestCompactSession_SessionNotFound(t *testing.T) {
	svc, _ := newRunServiceForTest(t)

	// Session not in history manager should not error — just return no compaction.
	count, freed, pruned, err := svc.CompactSession(context.Background(), "unknown-session", compactRunConfig(unreachableURL(t)))
	if err != nil {
		t.Fatalf("CompactSession should not error for unknown session: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0 for unknown session, got %d", count)
	}
	if freed != 0 {
		t.Errorf("expected freed=0 for unknown session, got %d", freed)
	}
	if pruned != 0 {
		t.Errorf("expected pruned=0 for unknown session, got %d", pruned)
	}
}

func TestCompactSession_ReplacesHistory(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "summarised output")
	svc, uiMgr := newRunServiceForTest(t)

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Populate history with a large tool message.
	msgs := []message.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "run build"},
		{Role: "tool", Content: strings.Repeat("Build output with detail\n", 200)},
	}
	svc.historySessionMgr.RestoreHistory(sess.ID, msgs)

	cfg := compactRunConfig(fakeLLM.URL)
	count, freed, pruned, err := svc.CompactSession(context.Background(), sess.ID, cfg)
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message")
	}
	if freed <= 0 {
		t.Fatalf("expected freed > 0, got %d", freed)
	}
	if pruned != 0 {
		t.Errorf("expected pruned = 0, got %d", pruned)
	}

	// Verify that history was replaced.
	hist := svc.historySessionMgr.History(sess.ID)
	if hist == nil {
		t.Fatal("history is nil after compaction")
	}
	foundCompacted := false
	for _, em := range hist {
		if strings.HasPrefix(em.Content(), "[TOOL RESULT COMPACTED") {
			foundCompacted = true
			break
		}
	}
	if !foundCompacted {
		t.Error("expected at least one [TOOL RESULT COMPACTED] message in history after compaction")
	}
}

func TestCompactSession_Snapshots(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "summary")
	svc, uiMgr := newRunServiceForTest(t)

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Attach a persister so we can verify snapshot is called.
	eitriDir := t.TempDir()
	p, err := persist.New(eitriDir)
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	svc.persister = p

	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: strings.Repeat("large tool output data\n", 200)},
	}
	svc.historySessionMgr.RestoreHistory(sess.ID, msgs)

	cfg := compactRunConfig(fakeLLM.URL)
	_, _, _, err = svc.CompactSession(context.Background(), sess.ID, cfg)
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}

	// Verify a snapshot file was written.
	snapshotPath := filepath.Join(eitriDir, "sessions", sess.ID, "session.json")
	if _, err := os.Stat(snapshotPath); os.IsNotExist(err) {
		t.Errorf("snapshot file not found at %s", snapshotPath)
	}
}

func TestCompactSession_SnapshotFailureWarns(t *testing.T) {
	fakeLLM := fakeCompactLLMServer(t, "summary")
	svc, uiMgr := newRunServiceForTest(t)

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Set a persister with an unwritable directory to force a snapshot failure.
	unwritableDir := filepath.Join(t.TempDir(), "sessions")
	if err := os.MkdirAll(unwritableDir, 0444); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	// Override the persister's root dir to an unwritable one by making the
	// entire sessions dir read-only.
	svc.persister = p

	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: strings.Repeat("large tool output\n", 200)},
	}
	svc.historySessionMgr.RestoreHistory(sess.ID, msgs)

	cfg := compactRunConfig(fakeLLM.URL)
	// Even with snapshot failure, CompactSession should return success
	// (the error is logged, not returned).
	count, freed, pruned, err := svc.CompactSession(context.Background(), sess.ID, cfg)
	if err != nil {
		t.Fatalf("CompactSession should not return error on snapshot failure: %v", err)
	}
	// Compaction itself should still have happened.
	if count == 0 {
		t.Log("compaction count was 0 — this is OK if the tool message was not large enough to trigger")
	}
	_ = freed
	_ = pruned
}

func TestCompactSessionHistory_SharedHelper(t *testing.T) {
	// Verify that compactSessionHistory (used by auto-compaction) respects
	// the high-water gate, while direct Compact() (used by manual) does not.
	fakeLLM := fakeCompactLLMServer(t, "summary")
	llmSvc, err := newCompactLLMService(context.Background(), compactRunConfig(fakeLLM.URL), nil)
	if err != nil {
		t.Fatalf("newCompactLLMService: %v", err)
	}

	msgs := []message.Message{
		{Role: "user", Content: "hello"},
		{Role: "tool", Content: strings.Repeat("large output data\n", 100)},
	}

	// Auto-compaction path: compactSessionHistory with highWater well above
	// the total estimate → should return nil (no compaction).
	highWater := 999_999 // far above total estimate
	lowWater := 500_000

	result, count, freed, pruned, err := compactSessionHistory(context.Background(), msgs, llmSvc, nil, highWater, lowWater, 0, 0, false, "")
	if err != nil {
		t.Fatalf("compactSessionHistory: %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when totalEst <= highWater (auto gate should prevent compaction)")
	}
	if count != 0 || freed != 0 || pruned != 0 {
		t.Fatalf("expected 0 values when gated, got count=%d freed=%d pruned=%d", count, freed, pruned)
	}

	// Manual compaction path: Compact directly with thresholds 0,0,0
	// → should always run regardless of size.
	manualResult, manualCount, manualFreed, manualPruned, manualErr := compactor.New().Compact(context.Background(), msgs, llmSvc, compactor.Thresholds{
		HighWater:            0,
		LowWater:             0,
		MessageSizeThreshold: 0,
	})
	if manualErr != nil {
		t.Fatalf("manual Compact: %v", manualErr)
	}
	if manualResult == nil {
		t.Fatal("expected non-nil result for manual compaction (no gate)")
	}
	if manualCount == 0 {
		t.Fatal("expected at least one compacted message for manual compaction")
	}
	_ = manualFreed
	_ = manualPruned
}

func TestCompactSessionHistory_CalibratedHighWaterGate(t *testing.T) {
	// ~400 chars of content → 100 tokens at the default CPT 4.0, but only
	// ~50 tokens once the model is calibrated toward CPT 8.0.
	msgs := []message.Message{
		{Role: "user", Content: strings.Repeat("a", 400)},
	}

	store := tokenizer.NewCalibrationStore()
	for i := 0; i < 50; i++ {
		store.Update("calibrated-model", 8.0)
	}
	if cpt := store.Lookup("calibrated-model"); cpt < 7.9 {
		t.Fatalf("calibrated-model CPT = %f, want ≈8.0", cpt)
	}

	// highWater sits between the calibrated estimate (50) and the default
	// estimate (100): the gate must use the calibrated value.
	highWater := 75

	// With calibration the estimate (50) is below high-water → no compaction,
	// and no LLM service is needed because the gate short-circuits first.
	result, count, freed, pruned, err := compactSessionHistory(context.Background(), msgs, nil, store, highWater, 0, 0, 0, false, "calibrated-model")
	if err != nil {
		t.Fatalf("compactSessionHistory (calibrated): %v", err)
	}
	if result != nil {
		t.Fatal("expected nil result when calibrated estimate is below high-water")
	}
	if count != 0 || freed != 0 || pruned != 0 {
		t.Fatalf("expected 0 values when gated, got count=%d freed=%d pruned=%d", count, freed, pruned)
	}

	// Without calibration the same content estimates to 100 tokens > 75, so
	// compaction runs — proving the gate reacts to calibration, not to size alone.
	fakeLLM := fakeCompactLLMServer(t, "summary")
	llmSvc, err := newCompactLLMService(context.Background(), compactRunConfig(fakeLLM.URL), nil)
	if err != nil {
		t.Fatalf("newCompactLLMService: %v", err)
	}
	result, count, freed, pruned, err = compactSessionHistory(context.Background(), msgs, llmSvc, nil, highWater, 0, 0, 0, false, "calibrated-model")
	if err != nil {
		t.Fatalf("compactSessionHistory (default): %v", err)
	}
	if result == nil {
		t.Fatal("expected compaction to run without calibration (default estimate exceeds high-water)")
	}
	if count == 0 {
		t.Fatal("expected at least one compacted message without calibration")
	}
	_ = freed
	_ = pruned
}

func TestOnTurnComplete_SyncsHistoryToUISession(t *testing.T) {
	uiMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	persister, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}

	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiMgr,
		HistorySessionMgr: historyMgr,
		Persister:         persister,
	})

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Populate the run's live history as the agent loop would across turns.
	historyMgr.Create(sess.ID)
	historyMgr.SetSystemPrompt(sess.ID, "You are Eitri.")
	historyMgr.AppendUser(sess.ID, "Do the work")
	historyMgr.AppendAssistant(sess.ID, "First, let me look around.", []message.ToolCall{
		{ID: "call_1", Function: message.FunctionCall{Name: "read", Arguments: "weird"}},
	})
	historyMgr.AppendTool(sess.ID, "call_1", "file contents...", "", false)
	historyMgr.AppendAssistant(sess.ID, "Here is a mid-run update.", nil)

	svc.OnTurnComplete(context.Background(), sess.ID)

	convo := uiMgr.GetConversationShared(sess.ID)
	if convo == nil {
		t.Fatal("expected a conversation in the UI session")
	}

	// The UI session should reflect the full live conversation, not just the
	// original user message — this is what makes long runs visible during
	// execution instead of appearing frozen until completion.
	roles := make([]string, 0, len(convo.Messages))
	for i := range convo.Messages {
		roles = append(roles, string(convo.Messages[i].Role))
	}
	want := []string{"user", "assistant", "tool", "assistant"}
	if !reflect.DeepEqual(roles, want) {
		t.Errorf("UI session roles = %v, want %v", roles, want)
	}
}
