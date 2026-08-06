package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	runtimeDebug "runtime/debug"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persona"

	"github.com/glemsom/eitri/internal/compactor"
	"github.com/glemsom/eitri/internal/tokenizer"

	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
	"github.com/glemsom/eitri/internal/uixt"
)

// StartRun starts a new agent run for a session with an explicit RunConfig.
// Returns warnings about stale skills, and error if run fails.
func (s *RunService) StartRun(ctx context.Context, sessionID, userMessage string, cfg RunConfig) ([]string, error) {
	return s.startRunWithConfig(ctx, sessionID, userMessage, cfg)
}

func (s *RunService) startRunWithConfig(ctx context.Context, sessionID, userMessage string, cfg RunConfig) ([]string, error) {
	// The session ID names this run's on-disk review trail; guard it against
	// path traversal through the shared run-job ID validator (issue #1108).
	if err := validateRunJobID(sessionID); err != nil {
		return nil, fmt.Errorf("invalid session ID: %w", err)
	}

	if s.exchangeIfDone(sessionID) {
		// Previous run was done; clean slate
	}

	if s.get(sessionID) != nil {
		return nil, fmt.Errorf("session %s already has an active run", sessionID)
	}

	baseURL := cfg.BaseURL
	modelName := cfg.ModelName
	maxTurns := cfg.MaxTurns
	maxHistory := cfg.MaxHistory
	contextWindowTokens := cfg.ContextWindowTokens
	// Store parent config for sub-agent setup
	s.subagents.StoreParentCfg(sessionID, cfg)

	if baseURL == "" || modelName == "" {
		return nil, errors.New("provider not configured: set base_url and model in settings")
	}

	if cfg.ProviderID == "" {
		cfg.ProviderID = "opencode_go"
	}

	// Activate persona-injected skills on the session so they appear in the
	// skill indicator chips. Skills already active are silently skipped
	// (ActivateSkill deduplicates).
	if cfg.ActivePersona != "" && s.uiSessionMgr != nil && s.skillsSvc != nil {
		def, err := persona.LoadWithHome(cfg.Workspace, resolveHomeDir(s.homeDir), cfg.ActivePersona)
		if err == nil {
			for _, skillName := range def.RequiredSkills {
				if s.skillsSvc.Lookup(skillName) != nil {
					s.uiSessionMgr.ActivateSkill(sessionID, skillName)
				}
			}
		}
	}

	skillCtx := s.resolveSessionSkillContext(sessionID)

	// Shared run-preparation seam: identical tool registry, LLM request, and
	// system prompt contract as batch mode (issue #1091). render_quick_replies
	// is registered only because a UI session exists here.
	prep, err := s.prepareRun(ctx, cfg, runPrepOptions{
		sessionID:     sessionID,
		skillCtx:      skillCtx,
		uiSessionMgr:  s.uiSessionMgr,
		allowDelegate: true,
	})
	if err != nil {
		return nil, err
	}
	llmSvc := prep.llmSvc
	toolReg := prep.toolReg
	fullSystemPrompt := prep.systemPrompt
	req := prep.req

	// Store the system prompt on the UI session so it gets persisted
	// in session snapshots and displayed in reports.
	if s.uiSessionMgr != nil {
		s.uiSessionMgr.SetSystemPrompt(sessionID, fullSystemPrompt)
	}

	if s.historySessionMgr != nil {
		s.historySessionMgr.Create(sessionID)
		s.historySessionMgr.SetSystemPrompt(sessionID, fullSystemPrompt)
		// Repair a history left with a dangling assistant tool call from an
		// interrupted run. Appending a user message after an unresolved tool use
		// makes an invalid sequence ("user message follows unresolved tool use")
		// that the provider rejects. See RepairPendingToolUse.
		s.historySessionMgr.RepairPendingToolUse(sessionID)
		s.historySessionMgr.AppendUser(sessionID, userMessage)
	}

	maxTurnsVal := maxTurns
	if maxTurnsVal <= 0 {
		maxTurnsVal = 10
	}

	sseState := runstate.New()
	runCtx, cancel := context.WithCancel(ctx)
	runCtx = context.WithValue(runCtx, tool.SessionIDKey, sessionID)

	state := &RunState{
		SessionID: sessionID,
		Cancel:    cancel,
		StartedAt: time.Now(),
		Done:      make(chan struct{}),
		SSE:       sseState,
		RunCfg:    cfg,
	}
	// Generate the run ID once at run start so the persisted timeline, SSE
	// events, and HTTP traces all share the same identifier and turns can be
	// correlated to traces by ID (issue #988).
	state.RunID = runstate.GenerateRunID(sessionID, state.StartedAt)
	s.store(sessionID, state)

	go func() {
		defer func() {
			// Release browser allocator connections before retention
			// so CDP connections do not leak across runs.
			toolReg.EndSession(sessionID)
			// Mark the run done and schedule retention cleanup via a
			// timer rather than sleeping the goroutine. This avoids
			// parking a goroutine for the entire retention window in
			// batch workloads with many finished runs.
			state.finish()
			time.AfterFunc(completedRunRetention, func() {
				s.remove(sessionID, state)
				// Clean up parent config for sub-agent setup
				s.subagents.DeleteParentCfg(sessionID)
			})
		}()

		// snapshotAndBroadcastIdle persists the session with StatusIdle and fires
		// a browser event so the UI reflects the idle state. Defined below the
		// goroutine; must be called before snapshotSession on every exit path
		// that persists the terminal state.

		w := runstate.NewWriter(sseState)

		// Construct adapters from service dependencies.
		var historyMgr loop.HistoryManager
		if s.historySessionMgr != nil {
			historyMgr = loop.NewSessionHistoryManager(s.historySessionMgr, sessionID)
		} else {
			historyMgr = loop.NewRequestHistoryManager(req)
		}
		confirmer := loop.NewFuncConfirmer(s.confirmPath)

		err := loop.RunAgent(runCtx, loop.RunSpec{
			Client:     llmSvc,
			Request:    req,
			MaxTurns:   maxTurnsVal,
			MaxHistory: maxHistory,
			SSEWriter:  w,
			Tools:      toolReg,
		}, loop.RunOpts{
			HistoryMgr:       historyMgr,
			Confirmer:        confirmer,
			UISessionMgr:     s.uiSessionMgr,
			SessionID:        sessionID,
			RunID:            runstate.GenerateRunID(sessionID, state.StartedAt),
			ContextWindow:    contextWindowTokens,
			CrashDumpFunc:    s.crashDumpFunc,
			Turns:            &state.Turns,
			DebugLLMDir:      cfg.DebugLLMDir,
			TurnTimeout:      cfg.TurnTimeout,
			TurnCompleter:    s,
			CalibrationStore: s.calibrationStore,
			ModelName:        cfg.ModelName,
			RetryPolicy:      &cfg.RetryPolicy,
		})
		if err != nil {
			if runCtx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				content := sseState.BufferString()
				reasoningContent := sseState.ReasoningBufferString()
				if content != "" {
					s.appendToSession(sessionID, content, reasoningContent)
				}
				s.setSessionIdleAndSnapshot(sessionID)
				s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, &runstate.TimelineTermination{
					Reason:  runstate.TerminationCancelled,
					Message: "Run cancelled by user or context deadline exceeded",
				})
				w.Error("Run cancelled")
				return
			}

			var maxTurnsErr *loop.MaxTurnsExceededError
			if errors.As(err, &maxTurnsErr) {
				content := sseState.BufferString()
				limitMsg := uixt.MaxTurnsMessage(maxTurnsErr.Limit)
				if content == "" {
					sseState.AppendBuffer(limitMsg)
					content = limitMsg
				} else {
					sseState.AppendBuffer("\n\n" + limitMsg)
					content += "\n\n" + limitMsg
				}
				reasoningContent := sseState.ReasoningBufferString()
				w.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), tokenizer.EstimateUsage(content, s.calibrationStore, cfg.ModelName))
				s.appendToSession(sessionID, content, reasoningContent)
				s.setSessionIdleAndSnapshot(sessionID)
				s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, &runstate.TimelineTermination{
					Reason:  runstate.TerminationMaxTurns,
					Message: limitMsg,
				})
				return
			}

			// Fatal error not covered above — mark the session failed before
			// persisting diagnostics so UI and disk snapshots do not stay running.
			s.setSessionErrorAndSnapshot(sessionID)
			w.Error(err.Error())
			s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, &runstate.TimelineTermination{
				Reason:  runstate.TerminationError,
				Message: err.Error(),
			})
			if s.crashDumpFunc != nil {
				s.crashDumpFunc(err, runtimeDebug.Stack())
			}
			return
		}

		content := sseState.BufferString()
		reasoningContent := sseState.ReasoningBufferString()
		if content != "" {
			s.appendToSession(sessionID, content, reasoningContent)
		}
		s.setSessionIdleAndSnapshot(sessionID)
		s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, &runstate.TimelineTermination{
			Reason:  runstate.TerminationCompleted,
			Message: "",
		})
	}()

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", cfg.ProviderID), slog.String("model", modelName))

	return skillCtx.Warnings, nil
}

// persistRunTimeline builds and persists a condensed timeline for the run.
// It takes the run ID and start time explicitly rather than the UI RunState,
// so the same timeline-writing path is callable for headless (batch) runs,
// which have no RunState (issue #1038).
func (s *RunService) persistRunTimeline(sessionID, runID string, startedAt time.Time, sseState *runstate.State, cfg RunConfig, termination *runstate.TimelineTermination) {
	if s.persister == nil {
		return
	}

	events := sseState.CondensedEvents()
	now := time.Now()

	timeline := &runstate.Timeline{
		Version:   1,
		RunID:     runID,
		SessionID: sessionID,
		Provider: runstate.TimelineProvider{
			Model:      cfg.ModelName,
			ProviderID: cfg.ProviderID,
		},
		StartedAt:   startedAt,
		EndedAt:     now,
		Termination: termination,
		Events:      events,
	}

	if err := s.persister.SaveTimeline(sessionID, timeline); err != nil {
		slog.Warn("failed to persist timeline",
			slog.String("session_id", sessionID),
			slog.Any("error", err),
		)
	}
}

// setSessionIdleAndSnapshot sets the session status to idle, persists
// a snapshot to disk with the updated status, and broadcasts the status
// change to browser subscribers. Must be called on every exit path that
// persists the terminal state.
//
// This ensures the on-disk snapshot reflects the idle state (not "running")
// which would otherwise happen if snapshotSession runs before the deferred
// broadcastSessionStatusUpdate.
func (s *RunService) setSessionIdleAndSnapshot(sessionID string) {
	s.setSessionStatusAndSnapshot(sessionID, uisession.StatusIdle)
}

// setSessionErrorAndSnapshot sets the session status to error, persists
// a snapshot to disk with the updated status, and broadcasts the status
// change to browser subscribers.
func (s *RunService) setSessionErrorAndSnapshot(sessionID string) {
	s.setSessionStatusAndSnapshot(sessionID, uisession.StatusError)
}

func (s *RunService) setSessionStatusAndSnapshot(sessionID string, status uisession.Status) {
	if s.uiSessionMgr == nil {
		s.snapshotSession(sessionID)
		return
	}
	s.uiSessionMgr.UpdateStatus(sessionID, status)
	s.snapshotSession(sessionID)

	meta := s.uiSessionMgr.GetMetaShared(sessionID)
	if meta == nil || meta.BrowserID == "" {
		return
	}
	s.broadcast.Broadcast(meta.BrowserID, BrowserEvent{
		Type: "session_status",
		Data: map[string]any{
			"session_id": sessionID,
			"status":     string(status),
		},
	})
}

// appendToSession persists an assistant message to the UI session.
func (s *RunService) appendToSession(sessionID, content, reasoningContent string) {
	if s.uiSessionMgr == nil || content == "" {
		return
	}
	// If the last message is an empty assistant (created by AppendComponent),
	// update its content instead of creating a duplicate.
	// Shared read: only the last message is inspected; mutations go through the
	// manager's mutating methods below.
	convo := s.uiSessionMgr.GetConversationShared(sessionID)
	if convo != nil && len(convo.Messages) > 0 {
		last := convo.Messages[len(convo.Messages)-1]
		if last.Role == "assistant" && last.Content == "" {
			s.uiSessionMgr.UpdateLastAssistantContent(sessionID, content)
			if reasoningContent != "" {
				s.uiSessionMgr.SetLastReasoningContent(sessionID, reasoningContent)
			}
			return
		}
		// The final assistant message may already have been synced into the UI
		// session by OnTurnComplete's live-history sync. Avoid appending a
		// duplicate of the same content at run completion.
		if last.Role == "assistant" && last.Content == content {
			if reasoningContent != "" && last.ReasoningContent == "" {
				s.uiSessionMgr.SetLastReasoningContent(sessionID, reasoningContent)
			}
			return
		}
	}
	s.uiSessionMgr.AppendMessage(sessionID, message.Message{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoningContent,
		CreatedAt:        time.Now(),
	})
}

// snapshotSession persists the current UI session to disk.
func (s *RunService) snapshotSession(sessionID string) {
	if s.persister == nil || s.uiSessionMgr == nil {
		return
	}
	// Uses the explicit copy helper: the persister serializes the session to
	// JSON and must receive a detached UISession facade (meta + messages +
	// skills) rather than a shared reference to manager-owned state.
	sess := s.uiSessionMgr.CopySession(sessionID)
	if sess == nil {
		return
	}
	if err := s.persister.SnapshotSession(sessionID, sess); err != nil {
		slog.Warn("failed to snapshot session",
			slog.String("session_id", sessionID),
			slog.Any("error", err),
		)
	}
}

func (s *RunService) broadcastSessionStatusUpdate(sessionID string, status uisession.Status) {
	if s.uiSessionMgr == nil {
		return
	}
	s.uiSessionMgr.UpdateStatus(sessionID, status)

	meta := s.uiSessionMgr.GetMetaShared(sessionID)
	if meta == nil || meta.BrowserID == "" {
		return
	}
	s.broadcast.Broadcast(meta.BrowserID, BrowserEvent{
		Type: "session_status",
		Data: map[string]any{
			"session_id": sessionID,
			"status":     string(meta.Status),
		},
	})
}

// ── TurnCompleter implementation ────────────────────────────────────────────

// OnTurnComplete implements loop.TurnCompleter. It is called after each
// complete agent turn to persist snapshots and trigger auto-compaction.
func (s *RunService) OnTurnComplete(ctx context.Context, sessionID string) {
	if s.persister == nil || s.uiSessionMgr == nil {
		return
	}
	// Uses the explicit copy helper: the snapshot below serializes the full
	// session facade to disk and needs a detached copy (see snapshotSession).
	sess := s.uiSessionMgr.CopySession(sessionID)
	if sess == nil {
		return
	}

	historyMsgs := s.historySessionMgr.History(sessionID)
	if historyMsgs == nil {
		return
	}

	// Sync the run's live conversation (history manager) into the UI session
	// so the browser UI and snapshots show incremental progress during long
	// runs instead of appearing frozen on the original user message until the
	// run completes. Without this, a multi-turn run burns turns and writes
	// traces while the UI session stays at a single message. The system prompt
	// is stored separately on the UI session (SetSystemPrompt), so strip it
	// from the history copy below. If compaction later replaces the history
	// with a compacted version, its own sync below overrides this one.
	if s.historySessionMgr != nil {
		uiMsgs := make([]message.Message, 0, len(historyMsgs))
		for _, em := range historyMsgs {
			uiMsgs = append(uiMsgs, em.ToMessage())
		}
		if len(uiMsgs) > 0 && uiMsgs[0].Role == "system" {
			uiMsgs = uiMsgs[1:]
		}
		s.uiSessionMgr.ReplaceConversationMessages(sessionID, uiMsgs)
	}

	// Always snapshot after each turn.
	if err := s.persister.SnapshotSession(sessionID, sess); err != nil {
		slog.Warn("failed to snapshot session",
			slog.String("session_id", sessionID),
			slog.Any("error", err),
		)
	}
	// Auto-compaction: retrieve the run config from the active RunState and
	// run the shared compaction step (also used by batch parent runs — the
	// settings in ~/.eitri/config.json are honored identically in both modes).
	state := s.get(sessionID)
	if state == nil {
		return
	}
	cfg := state.RunCfg

	compactedMsgs, compactedCount, freedTokens, prunedToolCalls, compErr := s.autoCompactAfterTurn(ctx, loop.NewSessionHistoryManager(s.historySessionMgr, sessionID), cfg)
	if compErr != nil {
		slog.Warn("compaction failed, will retry on next turn",
			slog.String("session_id", sessionID),
			slog.Any("error", compErr),
		)
		// Broadcast warning toast
		if runState := s.get(sessionID); runState != nil {
			runState.SSE.Broadcast(runstate.SSEEvent{
				Type: "toast",
				Data: map[string]any{
					"level":   "warning",
					"message": "Compaction failed: " + compErr.Error(),
				},
			})
		}
		return
	}
	if compactedMsgs == nil {
		return
	}

	// Sync compacted messages to the UI session manager so snapshots reflect
	// the compacted state. Strip the system prompt (stored separately in UI session).
	if s.uiSessionMgr != nil {
		uiMsgs := compactedMsgs
		if len(uiMsgs) > 0 && uiMsgs[0].Role == "system" {
			uiMsgs = uiMsgs[1:]
		}
		s.uiSessionMgr.ReplaceConversationMessages(sessionID, uiMsgs)
	}

	// Snapshot the compacted history. Uses the explicit copy helper: the
	// persister serializes the full session facade and needs a detached copy.
	sessAfter := s.uiSessionMgr.CopySession(sessionID)
	if sessAfter != nil {
		if err := s.persister.SnapshotSession(sessionID, sessAfter); err != nil {
			slog.Warn("failed to snapshot compacted session",
				slog.String("session_id", sessionID),
				slog.Any("error", err),
			)
		}
	}

	// Broadcast compaction_complete event for UI toast.
	freedK := freedTokens / 1000
	message := fmt.Sprintf("Compacted %d messages — freed ~%dk tokens", compactedCount, freedK)
	if prunedToolCalls > 0 {
		message += fmt.Sprintf(". %d tool calls pruned", prunedToolCalls)
	}
	if runState := s.get(sessionID); runState != nil {
		runState.SSE.Broadcast(runstate.SSEEvent{
			Type: "compaction_complete",
			Data: map[string]any{
				"compacted_count":   compactedCount,
				"freed_tokens":      freedTokens,
				"pruned_tool_calls": prunedToolCalls,
				"message":           message,
			},
		})
	}
}

// compactSessionHistory runs the compactor on the given messages using the
// provided LLM service, gated by high-water and low-water thresholds.
// The high-water gate uses the CalibrationStore's per-model chars-per-token
// estimate when a store and model are provided, falling back to the default
// 4.0 ratio when the store is nil.
// Returns the compacted messages, count, freed tokens, pruned tool calls, and any error.
// Shared by auto-compaction (OnTurnComplete) and manual compaction (CompactSession).
func compactSessionHistory(ctx context.Context, messages []message.Message, client *litellm.Client, store *tokenizer.CalibrationStore, highWater, lowWater, messageSizeThreshold, toolCallRetentionTurns int, salienceEnabled bool, model string) ([]message.Message, int, int, int, error) {
	totalEst := compactor.MessagesTokenEstimate(messages, store, model)
	if totalEst <= highWater {
		return nil, 0, 0, 0, nil
	}
	return compactor.New().Compact(ctx, messages, client, compactor.Thresholds{
		HighWater:              highWater,
		LowWater:               lowWater,
		MessageSizeThreshold:   messageSizeThreshold,
		ToolCallRetentionTurns: toolCallRetentionTurns,
		SalienceEnabled:        salienceEnabled,
		Model:                  model,
	})
}
