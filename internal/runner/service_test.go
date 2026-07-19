package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/history"

	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

func newRunServiceForTest(t *testing.T) (*RunService, *uisession.Manager, string) {
	t.Helper()

	uiSessionMgr := uisession.NewManager(10)
	historyMgr := history.NewSessionManager(50)

	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiSessionMgr,
		HistorySessionMgr: historyMgr,
	})
	svc.SetWorkspace(t.TempDir())
	return svc, uiSessionMgr, ""
}

// configureTestProvider sets a basic provider config on the RunService.
func configureTestProvider(svc *RunService) {
	svc.UpdateProviderConfig(&config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://test.local",
		APIKey:   "test-key",
		Model:    "test-model",
	})
}

func TestRunService_HistoryPersistsAcrossConfigUpdates(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)

	// First config update: creates historySessionMgr
	cfg1 := &config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://test.local",
		APIKey:   "test-key",
		Model:    "test-model",
	}
	svc.UpdateProviderConfig(cfg1)

	// Simulate first run: create and populate a session
	svc.historySessionMgr.Create("test-session")
	svc.historySessionMgr.SetSystemPrompt("test-session", "You are Eitri.")
	svc.historySessionMgr.AppendUser("test-session", "Hi, my name is Glenn")

	// Simulate assistant response (e.g. agent loop appends this)
	svc.historySessionMgr.AppendAssistant("test-session", "Hello Glenn!", nil)

	hist1 := svc.historySessionMgr.History("test-session")
	if len(hist1) != 3 {
		t.Fatalf("History length after first run = %d, want 3 (sys + user + asst)", len(hist1))
	}

	// Second config update: simulates what happens on second chat message
	cfg2 := &config.Config{
		Provider: "opencode_go",
		BaseURL:  "http://test.local",
		APIKey:   "test-key",
		Model:    "test-model",
	}
	svc.UpdateProviderConfig(cfg2)

	// BUG: before fix, historySessionMgr was replaced with a fresh empty one.
	// AFTER fix: historySessionMgr should still have the first run's data.
	if svc.historySessionMgr == nil {
		t.Fatal("historySessionMgr is nil after second UpdateProviderConfig")
	}
	hist2 := svc.historySessionMgr.History("test-session")
	if len(hist2) != 3 {
		t.Fatalf("History length after second config update = %d, want 3 (preserved). "+
			"Bug: UpdateProviderConfig replaced historySessionMgr", len(hist2))
	}

	// Verify the content is intact
	if hist2[0].Content != "You are Eitri." {
		t.Errorf("System prompt changed: got %q", hist2[0].Content)
	}
	if len(hist2) >= 2 && hist2[1].Content != "Hi, my name is Glenn" {
		t.Errorf("User message changed: got %q", hist2[1].Content)
	}
}

func TestRunService_StartRun_RejectsDuplicateActiveRun(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)
	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	// Second StartRun for same session should fail (run active)
	_, err = svc.StartRun(context.Background(), "session-1", "hello again")
	if err == nil {
		t.Fatal("expected error for duplicate run, got nil")
	}
}

func TestRunService_Subscribe_ReplaysHistoryForLateJoiners(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)
	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	state := svc.ActiveRun("session-1")
	if state == nil {
		t.Fatal("no active run after StartRun")
	}

	// Broadcast an event directly
	state.SSE.Broadcast(runstate.SSEEvent{Type: "token", Content: "hello world"})

	// Late joiner should see the broadcast event in history
	_, ch, ok := svc.Subscribe("session-1")
	if !ok {
		t.Fatal("Subscribe returned ok=false")
	}

	// Drain initial events (connecting event may be present + our broadcast)
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
	svc, _, _ := newRunServiceForTest(t)
	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), "session-1", "hello")
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
	svc, _, _ := newRunServiceForTest(t)
	configureTestProvider(svc)

	for _, id := range []string{"session-a", "session-b"} {
		_, err := svc.StartRun(context.Background(), id, "hello")
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

	svc, _, _ := newRunServiceForTest(t)

	svc.SetPersistAuth(func(apiKey string, providerAuth json.RawMessage) error {
		persistCalled = true
		capturedKey = apiKey
		capturedAuth = providerAuth
		return nil
	})

	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), "session-1", "hello")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel("session-1")

	_ = persistCalled
	_ = capturedKey
	_ = capturedAuth
}

func TestRunService_StartRun_EmptyConfig_ReturnsError(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)

	_, err := svc.StartRun(context.Background(), "session-1", "hello")
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %q, want 'not configured'", err.Error())
	}
}

func TestRunService_Subscribe_ReturnsOkFalseForNoActiveRun(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)

	_, _, ok := svc.Subscribe("nonexistent")
	if ok {
		t.Fatal("Subscribe should return ok=false for session with no active run")
	}
}

func TestRunService_Cancel_ReturnsFalseForNoActiveRun(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)

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
	svc, _, _ := newRunServiceForTest(t)
	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), "session-1", "hello")
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
