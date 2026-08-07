package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	runtimeDebug "runtime/debug"
	"strings"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persona"

	"github.com/glemsom/eitri/internal/compactor"
	"github.com/glemsom/eitri/internal/timeline"
	"github.com/glemsom/eitri/internal/tokenizer"

	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
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
	state.RunID = timeline.GenerateRunID(sessionID, state.StartedAt)
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

		// UI-mode run-completer: the same per-turn persistence + auto-compaction
		// + re-snapshot path batch and sub-agent runs use (run_completer.go),
		// wired with the UI snapshot source so per-turn snapshots live-sync the
		// run's live conversation into the UI session and preserve full
		// UI-session fidelity via CopySession (ADR-0028).
		completer := &runCompleter{
			svc:        s,
			historyMgr: historyMgr,
			id:         sessionID,
			cfg:        cfg,
			startedAt:  state.StartedAt,
		}
		completer.snapshotSource = completer.uiSnapshotSource

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
			RunID:            timeline.GenerateRunID(sessionID, state.StartedAt),
			ContextWindow:    contextWindowTokens,
			CrashDumpFunc:    s.crashDumpFunc,
			Turns:            &state.Turns,
			DebugLLMDir:      cfg.DebugLLMDir,
			TurnTimeout:      cfg.TurnTimeout,
			TurnCompleter:    completer,
			CalibrationStore: s.calibrationStore,
			ModelName:        cfg.ModelName,
			RetryPolicy:      &cfg.RetryPolicy,
		})
		if err != nil {
			// Terminal status and termination reason come from the single exit
			// taxonomy shared with the batch and sub-agent paths (ADR-0029):
			// idle on cancellation / max-turns, error on true failure. The
			// per-reason branches below keep only the transport-specific work
			// (message appending, SSE events, crash dumps).
			outcome := classifyRunExit(err, runCtx)
			switch outcome.Termination.Reason {
			case timeline.TerminationCancelled:
				// The final turn is already in the UI conversation: the
				// UI-mode run-completer live-syncs each completed turn,
				// including the last one (ADR-0028). When the run-completer's
				// live-sync cannot run (no disk persister attached), the
				// run-end append below is the sole path that puts the streamed
				// reply into the UI conversation (issue #1217).
				s.syncRunResultToUISession(sessionID, sseState.BufferString(), sseState.ReasoningBufferString())
				s.setSessionStatusAndSnapshot(sessionID, outcome.Status)
				s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, outcome.Termination)
				w.Error("Run cancelled")
				return

			case timeline.TerminationMaxTurns:
				// Stream the max-turns message to live subscribers (SSE) but do
				// not append it to the UI conversation — the completed turns,
				// including the final assistant message, are already there via
				// the run-completer's per-turn live-sync (issue #1203).
				limitMsg := outcome.Termination.Message
				content := sseState.BufferString()
				if content == "" {
					sseState.AppendBuffer(limitMsg)
					content = limitMsg
				} else {
					sseState.AppendBuffer("\n\n" + limitMsg)
					content += "\n\n" + limitMsg
				}
				w.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), tokenizer.EstimateUsage(content, s.calibrationStore, cfg.ModelName))
				s.syncRunResultToUISession(sessionID, content, sseState.ReasoningBufferString())
				s.setSessionStatusAndSnapshot(sessionID, outcome.Status)
				s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, outcome.Termination)
				return

			default:
				// Fatal error — mark the session failed before persisting
				// diagnostics so UI and disk snapshots do not stay running.
				s.setSessionStatusAndSnapshot(sessionID, outcome.Status)
				w.Error(err.Error())
				s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, outcome.Termination)
				if s.crashDumpFunc != nil {
					s.crashDumpFunc(err, runtimeDebug.Stack())
				}
				return
			}
		}

		// Normal completion: the final assistant message is already in the UI
		// conversation via the run-completer's live-history sync of the last
		// completed turn (ADR-0028) when a disk persister is attached. Without
		// a persister the per-turn live-sync cannot run, so the run-end append
		// below is the sole path that puts the streamed reply into the UI
		// conversation — the browser's final-render POST reads it (issue
		// #1217). The append dedups against the per-turn sync, so it is safe
		// to call unconditionally on both configurations (issue #1203).
		outcome := classifyRunExit(nil, runCtx)
		s.syncRunResultToUISession(sessionID, sseState.BufferString(), sseState.ReasoningBufferString())
		s.setSessionStatusAndSnapshot(sessionID, outcome.Status)
		s.persistRunTimeline(sessionID, state.RunID, state.StartedAt, sseState, cfg, outcome.Termination)
	}()

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", cfg.ProviderID), slog.String("model", modelName))

	return skillCtx.Warnings, nil
}

// syncRunResultToUISession appends the run's streamed reply to the UI session
// conversation (in-memory). It is the run-end counterpart to the
// run-completer's per-turn live-sync (ADR-0028): the per-turn sync only runs
// when a disk persister is attached, so on persister-less configurations —
// browser E2E test servers and any embedded run service without persistence —
// this append is what makes the final assistant message reach the UI
// conversation that the browser's final-render POST reads. Without it the
// streaming-markdown final-render browser tests fail with an empty bubble
// (issue #1217).
//
// When a persister IS attached the final reply is already in the UI
// conversation via the per-turn sync, and the suffix-match dedup below makes
// the append a no-op — so calling this unconditionally on every exit path is
// safe (issue #1203).
func (s *RunService) syncRunResultToUISession(sessionID, content, reasoningContent string) {
	if s.persister != nil || s.uiSessionMgr == nil || content == "" {
		return
	}
	// If the last message is an empty assistant (created by AppendComponent /
	// SetQuickReplies during tool execution), update its content instead of
	// creating a duplicate — preserving the UI-only fields (components,
	// quick replies) attached to it.
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
		// session by the run-completer's per-turn live-history sync. Avoid
		// appending a duplicate at run completion: the last UI message is the
		// final assistant reply while `content` is the run's *accumulated* SSE
		// buffer (all turns' text concatenated), so match the suffix rather
		// than the whole buffer.
		if last.Role == "assistant" && last.Content != "" && strings.HasSuffix(content, last.Content) {
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

// persistRunTimeline builds and persists a condensed timeline for the run.
// It takes the run ID and start time explicitly rather than the UI RunState,
// so the same timeline-writing path is callable for headless (batch) runs,
// which have no RunState (issue #1038).
func (s *RunService) persistRunTimeline(sessionID, runID string, startedAt time.Time, sseState *runstate.State, cfg RunConfig, termination *timeline.TimelineTermination) {
	if s.persister == nil {
		return
	}

	events := timeline.CondensedEvents(sseState.History())
	now := time.Now()

	timelineRec := &timeline.Timeline{
		Version:   1,
		RunID:     runID,
		SessionID: sessionID,
		Provider: timeline.TimelineProvider{
			Model:      cfg.ModelName,
			ProviderID: cfg.ProviderID,
		},
		StartedAt:   startedAt,
		EndedAt:     now,
		Termination: termination,
		Events:      events,
	}

	if err := s.persister.SaveTimeline(sessionID, timelineRec); err != nil {
		slog.Warn("failed to persist timeline",
			slog.String("session_id", sessionID),
			slog.Any("error", err),
		)
	}
}

// setSessionStatusAndSnapshot sets the session status, persists a snapshot to
// disk with the updated status, and broadcasts the status change to browser
// subscribers. The status comes from the shared exit taxonomy (ADR-0029); on
// the UI exit paths it must be called before snapshotSession so the on-disk
// snapshot reflects the terminal state (idle/error) instead of "running".
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

// appendToSession no longer exists (issue #1203): the run-end append of the
// accumulated SSE buffer was removed — each completed turn, including the
// final one, reaches the UI conversation exactly once through the unified
// completion path's live-history sync (run_completer.go, ADR-0028). Sub-agent
// child sessions append their transcript directly in subagent.go.

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

// Per-turn completion for UI runs moved into the unified runCompleter
// (run_completer.go, ADR-0028): RunService no longer implements
// loop.TurnCompleter. startRunWithConfig wires a UI-mode runCompleter into
// loop.RunOpts.TurnCompleter.

// compactSessionHistory runs the compactor on the given messages using the
// provided LLM service, gated by high-water and low-water thresholds.
// The high-water gate uses the CalibrationStore's per-model chars-per-token
// estimate when a store and model are provided, falling back to the default
// 4.0 ratio when the store is nil.
// Returns the compacted messages, count, freed tokens, pruned tool calls, and any error.
// Shared by auto-compaction (autoCompactAfterTurn) and manual compaction
// (CompactSession).
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
