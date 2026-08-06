package runner

import (
	"context"
	"testing"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

// TestRunCompleter_SharedBehavior verifies the unified run-completer (issue
// #1107) serves both conversation-source kinds through the loop.HistoryManager
// seam: a session-manager-backed source (batch/UI) and a request-based source
// (sub-agent). OnTurnComplete persists a running-status snapshot with the
// leading system message stripped, and terminal writes the final-status
// snapshot plus a timeline under the run's ID.
func TestRunCompleter_SharedBehavior(t *testing.T) {
	persister, rec := newSubAgentPersistWiring(t)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})
	cfg := compactRunConfig(unreachableURL(t)) // ContextWindowTokens 0: compaction is a no-op

	startedAt := time.Now()
	base := runCompleter{
		svc:          svc,
		title:        "task",
		systemPrompt: "You are a test.",
		workspace:    "/tmp/ws",
		startedAt:    startedAt,
		cfg:          cfg,
	}

	t.Run("session-manager source (batch)", func(t *testing.T) {
		id := "sess-batch"
		sessionMgr := history.NewSessionManager(50)
		sessionMgr.Create(id)
		sessionMgr.SetSystemPrompt(id, "You are a test.")
		sessionMgr.AppendUser(id, "hello")
		sessionMgr.AppendAssistant(id, "hi there", nil)
		c := base
		c.id = id
		c.historyMgr = loop.NewSessionHistoryManager(sessionMgr, id)

		c.OnTurnComplete(context.Background(), id)

		sess := loadSubAgentSessionSnapshot(t, persister, id)
		if sess.Status != uisession.StatusRunning {
			t.Errorf("session-manager snapshot Status = %q, want %q", sess.Status, uisession.StatusRunning)
		}
		assertNoSystemMessage(t, sess)
		if len(sess.Messages) != 2 {
			t.Errorf("session-manager snapshot has %d messages, want 2 (user + assistant)", len(sess.Messages))
		}
		if sess.Messages[0].Role != "user" || sess.Messages[0].Content != "hello" {
			t.Errorf("session-manager snapshot first message = %+v, want user 'hello'", sess.Messages[0])
		}

		c.terminal(runstate.New(), runCompleterTerminalStatus(runstate.TerminationCompleted), &runstate.TimelineTermination{Reason: runstate.TerminationCompleted})
		sess2 := loadSubAgentSessionSnapshot(t, persister, id)
		if sess2.Status != uisession.StatusIdle {
			t.Errorf("session-manager terminal snapshot Status = %q, want %q", sess2.Status, uisession.StatusIdle)
		}
	})

	t.Run("request source (sub-agent)", func(t *testing.T) {
		id := "sess-req"
		req := &litellm.Request{
			Model: "test-model",
			Messages: []litellm.Message{
				{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a test."}}},
				{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: "run whoami"}}},
				{Role: litellm.Role("assistant"), Blocks: []litellm.Block{litellm.TextBlock{Text: "doing"}}},
			},
		}
		c := base
		c.id = id
		c.parentID = "parent-1"
		c.historyMgr = loop.NewRequestHistoryManager(req)

		c.OnTurnComplete(context.Background(), id)

		sess := loadSubAgentSessionSnapshot(t, persister, id)
		if sess.Status != uisession.StatusRunning {
			t.Errorf("request snapshot Status = %q, want %q", sess.Status, uisession.StatusRunning)
		}
		if sess.ParentID != "parent-1" {
			t.Errorf("request snapshot ParentID = %q, want %q", sess.ParentID, "parent-1")
		}
		assertNoSystemMessage(t, sess)
		if len(sess.Messages) != 2 {
			t.Errorf("request snapshot has %d messages, want 2 (user + assistant)", len(sess.Messages))
		}
	})
	t.Run("terminal error mapping", func(t *testing.T) {
		id := "sess-err"
		req := &litellm.Request{
			Model: "test-model",
			Messages: []litellm.Message{
				{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a test."}}},
				{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: "run whoami"}}},
			},
		}
		c := base
		c.id = id
		c.historyMgr = loop.NewRequestHistoryManager(req)

		c.terminal(runstate.New(), runCompleterTerminalStatus(runstate.TerminationError), &runstate.TimelineTermination{Reason: runstate.TerminationError})
		sess := loadSubAgentSessionSnapshot(t, persister, id)
		if sess.Status != uisession.StatusError {
			t.Errorf("error terminal snapshot Status = %q, want %q", sess.Status, uisession.StatusError)
		}
	})
}

// assertNoSystemMessage fails the test if the snapshot carries any message
// with the system role (the system prompt must live in SystemPrompt only).
func assertNoSystemMessage(t *testing.T, sess *uisession.UISession) {
	t.Helper()
	for _, m := range sess.Messages {
		if m.Role == "system" {
			t.Errorf("snapshot contains a system message; the system prompt must be stored in SystemPrompt only")
		}
	}
	if sess.SystemPrompt == "" {
		t.Errorf("snapshot SystemPrompt is empty")
	}
}
