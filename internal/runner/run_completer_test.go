package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
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
		svc:        svc,
		historyMgr: loop.NewSessionHistoryManager(historyMgr, sess.ID),
		id:         sess.ID,
		cfg:        RunConfig{},
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

// TestRunService_SyncRunResultToUISession verifies the persister-less run-end
// UI sync (issue #1217): when no disk persister is attached, the per-turn
// run-completer live-sync cannot run, so the run-end append is the sole path
// that puts the streamed reply into the UI conversation the browser renders.
func TestRunService_SyncRunResultToUISession(t *testing.T) {
	t.Run("appends assistant reply when conversation has only the user message", func(t *testing.T) {
		svc, uiMgr := newRunServiceForTest(t)
		sess, err := uiMgr.Create("browser-1")
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		uiMgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "test"})

		svc.syncRunResultToUISession(sess.ID, "Flow:\n\n```mermaid\ngraph TD;\nA-->B;\n```", "")

		convo := uiMgr.GetConversationShared(sess.ID)
		if convo == nil || len(convo.Messages) != 2 {
			t.Fatalf("UI conversation = %+v, want 2 messages (user + assistant)", convo)
		}
		last := convo.Messages[1]
		if last.Role != "assistant" || last.Content != "Flow:\n\n```mermaid\ngraph TD;\nA-->B;\n```" {
			t.Errorf("last message = %+v, want assistant with the streamed reply", last)
		}
	})

	t.Run("updates placeholder created by tool execution without losing UI-only fields", func(t *testing.T) {
		svc, uiMgr := newRunServiceForTest(t)
		sess, err := uiMgr.Create("browser-1")
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		uiMgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "Show components"})
		// Tool execution created an empty assistant placeholder carrying
		// UI-only fields (quick replies + a component).
		if err := uiMgr.SetQuickReplies(sess.ID, []string{"yes", "no"}); err != nil {
			t.Fatalf("SetQuickReplies: %v", err)
		}
		if err := uiMgr.AppendComponent(sess.ID, message.ComponentData{Name: "MermaidDiagram", Data: map[string]any{"code": "graph TD; A-->B;"}}); err != nil {
			t.Fatalf("AppendComponent: %v", err)
		}

		svc.syncRunResultToUISession(sess.ID, "done", "")

		convo := uiMgr.GetConversationShared(sess.ID)
		if convo == nil || len(convo.Messages) != 2 {
			t.Fatalf("UI conversation = %+v, want 2 messages (no duplicate)", convo)
		}
		last := convo.Messages[1]
		if last.Content != "done" {
			t.Errorf("placeholder content = %q, want %q (updated in place)", last.Content, "done")
		}
		if len(last.QuickReplies) != 2 || last.QuickReplies[0] != "yes" || last.QuickReplies[1] != "no" {
			t.Errorf("QuickReplies = %v, want [yes no] (preserved)", last.QuickReplies)
		}
		if len(last.Components) != 1 || last.Components[0].Name != "MermaidDiagram" {
			t.Errorf("Components = %+v, want [MermaidDiagram] (preserved)", last.Components)
		}
	})

	t.Run("does not duplicate a reply the per-turn sync already added", func(t *testing.T) {
		svc, uiMgr := newRunServiceForTest(t)
		sess, err := uiMgr.Create("browser-1")
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		uiMgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "test"})
		uiMgr.AppendMessage(sess.ID, message.Message{Role: "assistant", Content: "final answer"})

		// The accumulated SSE buffer is all turns' text; the final reply is its
		// suffix — appending again must be a no-op (issue #1203).
		svc.syncRunResultToUISession(sess.ID, "first turn\n\nfinal answer", "")

		convo := uiMgr.GetConversationShared(sess.ID)
		if convo == nil || len(convo.Messages) != 2 {
			t.Fatalf("UI conversation = %+v, want 2 messages (no duplicate)", convo)
		}
	})
}

// TestRunService_StartRun_PopulatesUIConversationWithoutPersister is the
// end-to-end regression test for the streaming-markdown final-render browser
// failures (issue #1217): a run started against a persister-less RunService —
// the exact configuration browser E2E test servers use — must put its final
// assistant reply into the in-memory UI conversation. Without the run-end
// sync, the browser's final-render POST reads an empty conversation and the
// tests fail with "assertion never passed".
func TestRunService_StartRun_PopulatesUIConversationWithoutPersister(t *testing.T) {
	const reply = "Flow:\n\n```mermaid\ngraph TD;\nA-->B;\n```"

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{"role":"assistant","content":""},"index":0}]}`, "\n\n")
			flusher, _ := w.(http.Flusher)
			if flusher != nil {
				flusher.Flush()
			}
			fmt.Fprintf(w, "data: "+`{"choices":[{"delta":{"content":%q},"index":0}]}`+"\n\n", reply)
			if flusher != nil {
				flusher.Flush()
			}
			fmt.Fprint(w, "data: ", `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`, "\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer llm.Close()

	svc, uiMgr := newRunServiceForTest(t)
	cfg := RunConfig{
		ProviderID: "opencode_go",
		BaseURL:    llm.URL,
		APIKey:     "test-key",
		ModelName:  "test-model",
		Workspace:  t.TempDir(),
		MaxTurns:   5,
	}

	sess, err := uiMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	if _, err := svc.StartRun(context.Background(), sess.ID, "test", cfg); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	// Wait for the run to complete (the retained state is removed after the
	// 5s retention window; poll the live state's Done channel first).
	deadline := time.Now().Add(10 * time.Second)
	for {
		state := svc.get(sess.ID)
		if state == nil {
			break // already past retention — conversation must be populated
		}
		select {
		case <-state.Done:
			goto done
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not complete within deadline")
		}
	}
done:

	convo := uiMgr.GetConversationShared(sess.ID)
	if convo == nil {
		t.Fatal("UI conversation is nil after run completion")
	}
	var sawAssistant bool
	for _, m := range convo.Messages {
		if m.Role == "assistant" && m.Content == reply {
			sawAssistant = true
		}
	}
	if !sawAssistant {
		t.Errorf("UI conversation missing the run's assistant reply; got %d messages: %+v",
			len(convo.Messages), convo.Messages)
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
