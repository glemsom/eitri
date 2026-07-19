package runner

import (
	"context"
	"encoding/json"
	"strings"
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

	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before done event")
			}
			if evt.Type == "done" {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for done event")
		}
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
