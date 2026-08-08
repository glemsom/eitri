package runner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/timeline"
	"github.com/glemsom/eitri/internal/tokenizer"
)

// TestRunExit_SeamDispatch verifies the single terminal seam (issue #1238)
// shared by the UI, batch, and sub-agent transports: runExit classifies the
// finished run, dispatches exactly the per-reason transport work for the
// classified termination (the single per-reason exit switch — no transport
// hand-rolls its own), and writes the terminal snapshot + timeline. It runs
// as a direct unit test — no full run is launched — so exit-path behaviour is
// testable without an async service run (ADR-0028/0029).
func TestRunExit_SeamDispatch(t *testing.T) {
	tests := []struct {
		name    string
		runErr  error
		ctx     context.Context
		status  uisession.Status
		reason  timeline.TerminationReason
		handler string // the single exitWork handler the seam must dispatch
	}{
		{
			name:    "completed",
			runErr:  nil,
			ctx:     context.Background(),
			status:  uisession.StatusIdle,
			reason:  timeline.TerminationCompleted,
			handler: "completed",
		},
		{
			name:    "cancelled",
			runErr:  context.Canceled,
			ctx:     context.Background(),
			status:  uisession.StatusIdle,
			reason:  timeline.TerminationCancelled,
			handler: "cancelled",
		},
		{
			name:    "max turns",
			runErr:  &loop.MaxTurnsExceededError{Limit: 3},
			ctx:     context.Background(),
			status:  uisession.StatusIdle,
			reason:  timeline.TerminationMaxTurns,
			handler: "maxTurns",
		},
		{
			name:    "error",
			runErr:  errors.New("boom"),
			ctx:     context.Background(),
			status:  uisession.StatusError,
			reason:  timeline.TerminationError,
			handler: "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			persister, rec := newSubAgentPersistWiring(t)
			svc := NewRunService(RunServiceDeps{
				DebugRecorder: rec,
				Persister:     persister,
			})

			id := "run-exit-" + tt.handler
			req := &litellm.Request{
				Model: "test-model",
				Messages: []litellm.Message{
					{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a test."}}},
					{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}},
				},
			}
			c := &runCompleter{
				svc:          svc,
				historyMgr:   loop.NewRequestHistoryManager(req),
				id:           id,
				runID:        "plumbed-" + tt.handler,
				title:        "task",
				systemPrompt: "You are a test.",
				workspace:    "/tmp/ws",
				startedAt:    time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
				cfg:          RunConfig{ProviderID: "opencode_go", ModelName: "test-model"},
			}
			// Initial running snapshot so the trail exists before the terminal write.
			c.persist(uisession.StatusRunning)

			// The transport's per-reason work, recording which handler fired.
			var ran []string
			work := &exitWork{
				completed: func(exitOutcome) { ran = append(ran, "completed") },
				cancelled: func(exitOutcome) { ran = append(ran, "cancelled") },
				maxTurns:  func(exitOutcome) { ran = append(ran, "maxTurns") },
				error:     func(exitOutcome) { ran = append(ran, "error") },
			}

			sseState := runstate.New()
			w := runstate.NewWriter(sseState)
			w.Done("msg_1", tokenizer.EstimateUsage("hi", nil, "test-model"))

			c.runExit(sseState, tt.runErr, tt.ctx, work)

			// Exactly the classified handler ran — the single exit switch.
			if len(ran) != 1 || ran[0] != tt.handler {
				t.Errorf("exitWork handlers ran = %v, want exactly [%s]", ran, tt.handler)
			}

			// Terminal snapshot status from the shared taxonomy (ADR-0029).
			sess := loadSubAgentSessionSnapshot(t, persister, id)
			if sess.Status != tt.status {
				t.Errorf("terminal snapshot Status = %q, want %q", sess.Status, tt.status)
			}

			// Timeline termination reason from the shared taxonomy.
			tl := loadSubAgentTimeline(t, persister, id)
			if tl.Termination == nil || tl.Termination.Reason != tt.reason {
				t.Errorf("timeline Termination = %+v, want reason %q", tl.Termination, tt.reason)
			}
		})
	}
}

// TestRunExit_NilExitWork verifies the batch transport's exit shape: a
// transport with no per-reason work (batch runs have none — they only persist)
// passes a nil exitWork, and the seam still writes the terminal snapshot and
// timeline on every exit path.
func TestRunExit_NilExitWork(t *testing.T) {
	persister, rec := newSubAgentPersistWiring(t)
	svc := NewRunService(RunServiceDeps{
		DebugRecorder: rec,
		Persister:     persister,
	})

	id := "run-exit-nil"
	req := &litellm.Request{
		Model: "test-model",
		Messages: []litellm.Message{
			{Role: litellm.RoleSystem, Blocks: []litellm.Block{litellm.TextBlock{Text: "You are a test."}}},
			{Role: litellm.RoleUser, Blocks: []litellm.Block{litellm.TextBlock{Text: "hello"}}},
		},
	}
	c := &runCompleter{
		svc:          svc,
		historyMgr:   loop.NewRequestHistoryManager(req),
		id:           id,
		runID:        "plumbed-nil",
		title:        "task",
		systemPrompt: "You are a test.",
		workspace:    "/tmp/ws",
		startedAt:    time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
		cfg:          RunConfig{ProviderID: "opencode_go", ModelName: "test-model"},
	}

	sseState := runstate.New()
	c.runExit(sseState, errors.New("boom"), context.Background(), nil)

	sess := loadSubAgentSessionSnapshot(t, persister, id)
	if sess.Status != uisession.StatusError {
		t.Errorf("terminal snapshot Status = %q, want %q", sess.Status, uisession.StatusError)
	}
	tl := loadSubAgentTimeline(t, persister, id)
	if tl.Termination == nil || tl.Termination.Reason != timeline.TerminationError {
		t.Errorf("timeline Termination = %+v, want reason %q", tl.Termination, timeline.TerminationError)
	}
}

// TestRunExit_UIExitWork verifies the UI transport's per-reason exit work
// directly (issue #1238): the handlers built by uiExitWork — the same wiring
// startRunWithConfig feeds to the single run-end seam — broadcast the terminal
// session status, append the streamed reply on completion/cancellation
// (persister-less run-end sync, issue #1217), and close the SSE stream with
// the reason's error event. This exercises all four UI exit paths as direct
// unit tests instead of full-service async runs.
func TestRunExit_UIExitWork(t *testing.T) {
	tests := []struct {
		name         string
		runErr       error
		status       uisession.Status
		wantAppend   bool // run-end sync appends the streamed reply
		wantSSEErr   string
		wantNoSSEErr bool
	}{
		{
			name:       "completed",
			runErr:     nil,
			status:     uisession.StatusIdle,
			wantAppend: true,
		},
		{
			name:       "cancelled",
			runErr:     context.Canceled,
			status:     uisession.StatusIdle,
			wantAppend: true,
			wantSSEErr: "Run cancelled",
		},
		{
			name:         "max turns",
			runErr:       &loop.MaxTurnsExceededError{Limit: 3},
			status:       uisession.StatusIdle,
			wantNoSSEErr: true, // the loop already broadcast the max-turns error (issue #1233)
		},
		{
			name:       "error",
			runErr:     errors.New("boom"),
			status:     uisession.StatusError,
			wantSSEErr: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, uiMgr := newRunServiceForTest(t)
			sess, err := uiMgr.Create("browser-1")
			if err != nil {
				t.Fatalf("Create session: %v", err)
			}
			uiMgr.AppendMessage(sess.ID, message.Message{Role: "user", Content: "hello"})

			id, ch := svc.SubscribeBrowser("browser-1")
			defer svc.UnsubscribeBrowser("browser-1", id)

			sseState := runstate.New()
			w := runstate.NewWriter(sseState)
			w.Token("the reply")

			// Wire the UI-mode run-completer the way startRunWithConfig does
			// (terminal snapshot source = plain CopySession), then run the full
			// seam: per-reason work → terminal snapshot + timeline →
			// afterTerminal session_status broadcast.
			completer := &runCompleter{
				svc:        svc,
				historyMgr: loop.NewRequestHistoryManager(&litellm.Request{Model: "test-model"}),
				id:         sess.ID,
				cfg:        RunConfig{},
			}
			completer.snapshotSource = completer.uiSnapshotSource
			completer.terminalSnapshotSource = func(uisession.Status) *uisession.UISession {
				if svc.uiSessionMgr == nil {
					return nil
				}
				return svc.uiSessionMgr.CopySession(sess.ID)
			}

			completer.runExit(sseState, tt.runErr, context.Background(), svc.uiExitWork(sess.ID, sseState, w, tt.runErr))

			// Terminal status broadcast to browser subscribers.
			var gotStatus string
			select {
			case evt := <-ch:
				if evt.Type != "session_status" {
					t.Fatalf("event type = %q, want session_status", evt.Type)
				}
				data, _ := evt.Data.(map[string]any)
				gotStatus, _ = data["status"].(string)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for session_status broadcast")
			}
			if gotStatus != string(tt.status) {
				t.Errorf("broadcast status = %q, want %q", gotStatus, tt.status)
			}

			// In-memory status updated.
			if got := uiMgr.Get(sess.ID); got == nil || got.Status != tt.status {
				t.Errorf("in-memory status = %+v, want %q", got, tt.status)
			}

			// Persister-less run-end sync appends the streamed reply on
			// completion / cancellation (issue #1217).
			convo := uiMgr.GetConversationShared(sess.ID)
			if convo == nil {
				t.Fatal("UI conversation is nil")
			}
			if tt.wantAppend {
				if len(convo.Messages) != 2 || convo.Messages[1].Role != "assistant" || convo.Messages[1].Content != "the reply" {
					t.Errorf("UI conversation = %+v, want [user, assistant 'the reply']", convo.Messages)
				}
			} else if len(convo.Messages) != 1 {
				t.Errorf("UI conversation = %+v, want just the user message (no run-end append)", convo.Messages)
			}

			// SSE stream close: the reason's error event (except max-turns,
			// which the loop already closed with its own error — issue #1233).
			history := sseState.History()
			var lastErr string
			for _, evt := range history {
				if evt.Type == "error" {
					lastErr = evt.Message
				}
			}
			if tt.wantSSEErr != "" && lastErr != tt.wantSSEErr {
				t.Errorf("last SSE error = %q, want %q (history: %+v)", lastErr, tt.wantSSEErr, history)
			}
			if tt.wantNoSSEErr && lastErr != "" {
				t.Errorf("SSE history contains an error event the max-turns path must not add: %+v", history)
			}
		})
	}
}

// TestSubAgentExitWork verifies the sub-agent transport's per-reason exit work
// directly (issue #1238): the exit switch lives in exactly one place
// (exitWork.run) and the sub-agent's handlers — built by subagentExitWork the
// same way SpawnSubAgent wires them — set the record's terminal status and
// error for each termination reason. This covers the sub-agent max-turns exit
// path, which previously had no direct coverage, without launching a run.
func TestSubAgentExitWork(t *testing.T) {
	runErr := errors.New("boom")
	tests := []struct {
		name   string
		runErr error
		status subAgentStatus
		hasErr bool
	}{
		{"completed", nil, subAgentCompleted, false},
		{"cancelled", context.Canceled, subAgentCancelled, false},
		{"max turns", &loop.MaxTurnsExceededError{Limit: 3}, subAgentError, true},
		{"error", runErr, subAgentError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &subAgentRecord{TaskID: "task-1"}
			work := subagentExitWork(record, tt.runErr)
			outcome := classifyRunExit(tt.runErr, context.Background())

			work.run(outcome)

			if record.Status != tt.status {
				t.Errorf("record Status = %q, want %q", record.Status, tt.status)
			}
			if tt.hasErr && record.Err != tt.runErr {
				t.Errorf("record.Err = %v, want %v", record.Err, tt.runErr)
			}
			if !tt.hasErr && record.Err != nil {
				t.Errorf("record.Err = %v, want nil", record.Err)
			}
		})
	}
}
