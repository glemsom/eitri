package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/persist"
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

		outcome := classifyRunExit(nil, context.Background())
		c.terminal(runstate.New(), outcome.Status, outcome.Termination)
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

		outcome := classifyRunExit(errors.New("boom"), context.Background())
		c.terminal(runstate.New(), outcome.Status, outcome.Termination)
		sess := loadSubAgentSessionSnapshot(t, persister, id)
		if sess.Status != uisession.StatusError {
			t.Errorf("error terminal snapshot Status = %q, want %q", sess.Status, uisession.StatusError)
		}
	})
}

// TestRunCompleter_UISnapshotSourceFidelity verifies the UI-transport
// snapshot source (ADR-0028): persist live-syncs the run's live conversation
// into the UI session, then snapshots the UI session facade via CopySession,
// preserving the full fidelity (ActiveSkills, ClosedAt, RenderedMessageIDs)
// that the history-derived facade omits — while still stripping the leading
// system message (stored in SystemPrompt only).
func TestRunCompleter_UISnapshotSourceFidelity(t *testing.T) {
	uiMgr := uisession.NewManager(10, t.TempDir())
	historyMgr := history.NewSessionManager(50)
	p, err := persist.New(t.TempDir())
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	svc := NewRunService(RunServiceDeps{
		UISessionMgr:      uiMgr,
		HistorySessionMgr: historyMgr,
		Persister:         p,
	})

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	// Fidelity carried on the UI session but absent from the history-derived
	// facade: the system prompt, active skills, a closed-at timestamp, and
	// rendered message IDs (the latter stored in a 10-slot ring buffer).
	uiMgr.SetSystemPrompt(sess.ID, "You are Eitri.")
	uiMgr.ActivateSkill(sess.ID, "write")
	closedAt := time.Now().Add(-time.Hour)
	uiMgr.SetClosedAt(sess.ID, &closedAt)
	uiMgr.AddRenderedMessageID(sess.ID, "msg_1")
	uiMgr.AddRenderedMessageID(sess.ID, "msg_2")

	// Populate the run's live history as the agent loop would across turns.
	historyMgr.Create(sess.ID)
	historyMgr.SetSystemPrompt(sess.ID, "You are Eitri.")
	historyMgr.AppendUser(sess.ID, "Do the work")
	historyMgr.AppendAssistant(sess.ID, "Mid-run reply.", nil)

	// UI-mode run-completer: the same per-turn path batch and sub-agent runs
	// use, wired with the UI snapshot source.
	c := &runCompleter{
		svc:          svc,
		historyMgr:   loop.NewSessionHistoryManager(historyMgr, sess.ID),
		id:           sess.ID,
		cfg:          RunConfig{},
	}
	c.snapshotSource = c.uiSnapshotSource

	c.OnTurnComplete(context.Background(), sess.ID)

	// The UI conversation must reflect the live history (what makes long runs
	// visible during execution), with the system message stripped.
	convo := uiMgr.GetConversationShared(sess.ID)
	if convo == nil || len(convo.Messages) != 2 {
		t.Fatalf("UI conversation = %+v, want 2 messages (user + assistant)", convo)
	}
	if convo.Messages[0].Role != "user" || convo.Messages[0].Content != "Do the work" {
		t.Errorf("UI conversation first message = %+v, want user 'Do the work'", convo.Messages[0])
	}

	snap := loadSubAgentSessionSnapshot(t, p, sess.ID)
	assertNoSystemMessage(t, snap)
	if snap.SystemPrompt != "You are Eitri." {
		t.Errorf("snapshot SystemPrompt = %q, want %q", snap.SystemPrompt, "You are Eitri.")
	}
	// CopySession fidelity preserved in the snapshot.
	if got := snap.ActiveSkills; len(got) != 1 || got[0] != "write" {
		t.Errorf("snapshot ActiveSkills = %v, want [write]", got)
	}
	if snap.ClosedAt == nil || !snap.ClosedAt.Equal(closedAt) {
		t.Errorf("snapshot ClosedAt = %v, want %v", snap.ClosedAt, closedAt)
	}
	rendered := snap.RenderedMessageIDs
	if len(rendered) < 2 || rendered[0] != "msg_1" || rendered[1] != "msg_2" {
		t.Errorf("snapshot RenderedMessageIDs = %v, want ring buffer starting [msg_1 msg_2]", rendered)
	}
	if len(snap.Messages) != 2 || snap.Messages[0].Content != "Do the work" || snap.Messages[1].Content != "Mid-run reply." {
		t.Errorf("snapshot Messages = %+v, want live history (user + assistant)", snap.Messages)
	}
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
