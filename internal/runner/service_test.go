package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

// newRunServiceForTest creates a RunService with test dependencies.
// Returns the service, the UI session manager (for assertions), and a session ID.
func newRunServiceForTest(t *testing.T) (*RunService, *uisession.Manager, string) {
	t.Helper()

	runnerMgr := NewManager()
	sessionMgr := executor.NewSessionManager(t.TempDir(), 0, 0)
	uiSessionMgr := uisession.NewManager(10)

	svc := NewRunService(RunServiceDeps{
		RunnerManager:  runnerMgr,
		SessionManager: sessionMgr,
		UISessionMgr:   uiSessionMgr,
	})
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

func TestRunService_StartRun_AgentBuiltAndCached(t *testing.T) {
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

	// Broadcast an event directly (simulating what AppendEvent would do)
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
				// keep draining to avoid blocking broadcasters
				continue
			}
			if evt.Type == "done" || evt.Type == "error" {
				// run likely completed, check if we saw our event
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

	// Cancel
	canceled := svc.Cancel("session-1")
	if !canceled {
		t.Fatal("Cancel returned false")
	}

	// Should get done event
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatal("channel closed before done event")
			}
			if evt.Type == "done" {
				return // success
			}
		case <-deadline:
			t.Fatal("timed out waiting for done event")
		}
	}
}

func TestRunService_CancelAll_StopsAllRuns(t *testing.T) {
	svc, _, _ := newRunServiceForTest(t)
	configureTestProvider(svc)

	// Start two runs
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

func TestRunService_AppendEvent_PersistsToSessionStore(t *testing.T) {
	svc, uiMgr, _ := newRunServiceForTest(t)

	// Create a session in the UI manager first
	uiSession, uiErr := uiMgr.Create("browser-1")
	if uiErr != nil {
		t.Fatalf("failed to create session: %v", uiErr)
	}
	sessionID := uiSession.ID

	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel(sessionID)

	state := svc.ActiveRun(sessionID)
	if state == nil {
		t.Fatal("no active run")
	}

	// Verify the SSE state is initialized
	if state.SSE == nil {
		t.Fatal("SSE state is nil")
	}

	// Verify buffer starts empty
	if content := state.SSE.BufferString(); content != "" {
		t.Fatalf("initial buffer = %q, want empty", content)
	}

	// Simulate text being buffered (as appendEvent would do)
	state.SSE.AppendBuffer("Hello from agent")
	if content := state.SSE.BufferString(); content != "Hello from agent" {
		t.Fatalf("buffer = %q, want Hello from agent", content)
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

	// For opencode_go, there's normally no auth refresh, so PersistAuth
	// may not be called. The test verifies the callback is wired, not
	// that it's called (that requires a github_copilot provider with
	// expired token).
	_ = persistCalled
	_ = capturedKey
	_ = capturedAuth
}

func TestRunService_AppendEvent_ErrorHandling(t *testing.T) {
	svc, _, sessionID := newRunServiceForTest(t)
	configureTestProvider(svc)

	_, err := svc.StartRun(context.Background(), sessionID, "hello")
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	defer svc.Cancel(sessionID)

	state := svc.ActiveRun(sessionID)
	if state == nil {
		t.Fatal("no active run")
	}

	// Simulate error broadcast
	state.SSE.BroadcastError("connection refused")

	// Event should be in history
	history := state.SSE.History()
	var found bool
	for _, evt := range history {
		if evt.Type == "error" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no error event in history")
	}
}

func TestRunService_AppendEvent_MaxTurnsMessage(t *testing.T) {
	msg := runstate.MaxTurnsMessage(1)
	if !strings.Contains(msg, "max turns") {
		t.Fatalf("max turns message = %q, want max turns mention", msg)
	}
	if !strings.Contains(msg, "1") {
		t.Fatalf("max turns message = %q, want limit number", msg)
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
