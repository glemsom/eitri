package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	runtimeDebug "runtime/debug"
	"time"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/persona"

	"github.com/glemsom/eitri/internal/compactor"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runner/adapters"
	"github.com/glemsom/eitri/internal/runner/broadcast"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runner/runconfig"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
)

// StartRun starts a new agent run for a session with an explicit RunConfig.
// Returns warnings about stale skills, and error if run fails.
func (s *RunService) StartRun(ctx context.Context, sessionID, userMessage string, cfg runconfig.RunConfig) ([]string, error) {
	return s.startRunWithConfig(ctx, sessionID, userMessage, cfg)
}

func (s *RunService) startRunWithConfig(ctx context.Context, sessionID, userMessage string, cfg runconfig.RunConfig) ([]string, error) {
	if s.tracker.exchangeIfDone(sessionID) {
		// Previous run was done; clean slate
	}

	if s.tracker.get(sessionID) != nil {
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
		def, err := persona.Load(cfg.Workspace, cfg.ActivePersona)
		if err == nil {
			for _, skillName := range def.InjectedSkills {
				if s.skillsSvc.Lookup(skillName) != nil {
					s.uiSessionMgr.ActivateSkill(sessionID, skillName)
				}
			}
		}
	}

	skillCtx := s.resolveSessionSkillContext(sessionID)

	llmSvc, toolReg, fullSystemPrompt, err := buildLLMService(ctx, cfg, sessionID, s.debugRecorder, s.persistAuth, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr, skillCtx)
	if err != nil {
		return nil, err
	}

	toolReg.Register(tool.NewRenderQuickReplies())
	if s.skillsSvc != nil {
		toolReg.Register(tool.NewSkill(s.skillsSvc, s.uiSessionMgr))
	}
	// Sub-agent tools: only parent agents get delegate/collect
	toolReg.Register(tool.NewDelegate(s))
	toolReg.Register(tool.NewCollect(s))

	if s.historySessionMgr != nil {
		s.historySessionMgr.Create(sessionID)
		s.historySessionMgr.SetSystemPrompt(sessionID, fullSystemPrompt)
		s.historySessionMgr.AppendUser(sessionID, userMessage)
	}

	maxTurnsVal := maxTurns
	if maxTurnsVal <= 0 {
		maxTurnsVal = 10
	}

	req := &llm.Request{
		Model:  modelName,
		Stream: true,
	}

	// Set session-scoped prompt cache key if the provider supports it
	providerDesc, _ := provider.Describe(cfg.ProviderID)
	if providerDesc.SupportsPromptCache {
		req.SessionID = sessionID
	}

	if cfg.ThinkingLevel != "" {
		req.ReasoningEffort = cfg.ThinkingLevel
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
	}
	s.tracker.store(sessionID, state)

	go func() {
		defer func() {
			state.finish()
			time.Sleep(completedRunRetention)
			s.tracker.remove(sessionID, state)

			// Clean up parent config for sub-agent setup
			s.subagents.DeleteParentCfg(sessionID)
		}()

		// Ensure session status is reset and browser subscribers notified when the run completes.
		defer s.broadcastSessionStatusUpdate(sessionID, uisession.StatusIdle)

		w := runstate.NewWriter(sseState)

		// Construct adapters from service dependencies.
		var historyMgr adapters.HistoryManager
		if s.historySessionMgr != nil {
			historyMgr = adapters.NewSessionHistoryManager(s.historySessionMgr, s.uiSessionMgr, sessionID)
		} else {
			historyMgr = adapters.NewRequestHistoryManager(req)
		}
		confirmer := adapters.NewFuncConfirmer(s.confirmPath)

		err := loop.RunAgent(runCtx, loop.RunSpec{
			Service:    llmSvc,
			Request:    req,
			MaxTurns:   maxTurnsVal,
			MaxHistory: maxHistory,
			SSEWriter:  w,
			Tools:      toolReg,
		}, loop.RunOpts{
			HistoryMgr:    historyMgr,
			Confirmer:     confirmer,
			UISessionMgr:  s.uiSessionMgr,
			SessionID:     sessionID,
			ContextWindow: contextWindowTokens,
			CrashDumpFunc: s.crashDumpFunc,
			Turns:         &state.Turns,
			DebugLLMDir:   cfg.DebugLLMDir,
			OnTurnComplete: func(sid string) {
				if s.persister == nil || s.uiSessionMgr == nil || s.historySessionMgr == nil {
					return
				}
				sess := s.uiSessionMgr.Get(sid)
				if sess == nil {
					return
				}
				historyMsgs := s.historySessionMgr.History(sid)
				if historyMsgs == nil {
					return
				}
				if err := s.persister.SnapshotSession(sid, sess, historyMsgs); err != nil {
					slog.Warn("failed to snapshot session",
						slog.String("session_id", sid),
						slog.Any("error", err),
					)
				}

				// Auto-compaction: if enabled and token usage exceeds high-water mark,
				// compact tool results to free up context window space.
				if !cfg.CompactionEnabled || contextWindowTokens <= 0 {
					return
				}
				highWater := contextWindowTokens * cfg.CompactionThresholdPercent / 100
				lowWater := contextWindowTokens * cfg.CompactionLowWaterPercent / 100
				totalEst := compactor.MessagesTokenEstimate(historyMsgs)
				if totalEst <= highWater {
					return
				}

				compactedMsgs, compactedCount, freedTokens, compErr := compactor.New().Compact(ctx, historyMsgs, llmSvc, compactor.Thresholds{
					HighWater: highWater,
					LowWater:  lowWater,
				})
				if compErr != nil {
					slog.Warn("compaction failed, will retry on next turn",
						slog.String("session_id", sid),
						slog.Any("error", compErr),
					)
					// Broadcast warning toast
					if runState := s.tracker.get(sid); runState != nil {
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
				if compactedMsgs == nil || compactedCount == 0 {
					return
				}

				// Replace in-memory history with compacted version
				s.historySessionMgr.RestoreHistory(sid, compactedMsgs)

				// Snapshot the compacted history
				sessAfter := s.uiSessionMgr.Get(sid)
				historyAfter := s.historySessionMgr.History(sid)
				if sessAfter != nil && historyAfter != nil {
					if err := s.persister.SnapshotSession(sid, sessAfter, historyAfter); err != nil {
						slog.Warn("failed to snapshot compacted session",
							slog.String("session_id", sid),
							slog.Any("error", err),
						)
					}
				}

				// Broadcast compaction_complete event for UI toast
				freedK := freedTokens / 1000
				if runState := s.tracker.get(sid); runState != nil {
					runState.SSE.Broadcast(runstate.SSEEvent{
						Type: "compaction_complete",
						Data: map[string]any{
							"compacted_count": compactedCount,
							"freed_tokens":    freedTokens,
							"message":         fmt.Sprintf("Compacted %d tool results — freed ~%dk tokens", compactedCount, freedK),
						},
					})
				}
			},
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				content := sseState.BufferString()
				reasoningContent := sseState.ReasoningBufferString()
				if content != "" {
					s.appendToSession(sessionID, content, reasoningContent)
				}
				s.snapshotSession(sessionID)
				return
			}

			var maxTurnsErr *runconfig.MaxTurnsExceededError
			if errors.As(err, &maxTurnsErr) {
				content := sseState.BufferString()
				limitMsg := runstate.MaxTurnsMessage(maxTurnsErr.Limit)
				if content == "" {
					sseState.AppendBuffer(limitMsg)
					content = limitMsg
				} else {
					sseState.AppendBuffer("\n\n" + limitMsg)
					content += "\n\n" + limitMsg
				}
				reasoningContent := sseState.ReasoningBufferString()
				w.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), runstate.EstimateUsage(content))
				s.appendToSession(sessionID, content, reasoningContent)
				s.snapshotSession(sessionID)
				return
			}

			// Fatal error not covered above — trigger crash dump
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
		s.snapshotSession(sessionID)
	}()

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", cfg.ProviderID), slog.String("model", modelName))

	return skillCtx.Warnings, nil
}

// appendToSession persists an assistant message to the UI session.
func (s *RunService) appendToSession(sessionID, content, reasoningContent string) {
	if s.uiSessionMgr == nil || content == "" {
		return
	}
	// If the last message is an empty assistant (created by AppendComponent),
	// update its content instead of creating a duplicate.
	sess := s.uiSessionMgr.Get(sessionID)
	if sess != nil && len(sess.Messages) > 0 {
		last := sess.Messages[len(sess.Messages)-1]
		if last.Role == "assistant" && last.Content == "" {
			s.uiSessionMgr.UpdateLastAssistantContent(sessionID, content)
			if reasoningContent != "" {
				s.uiSessionMgr.SetLastReasoningContent(sessionID, reasoningContent)
			}
			return
		}
	}
	s.uiSessionMgr.AppendMessage(sessionID, uisession.Message{
		Role:             "assistant",
		Content:          content,
		ReasoningContent: reasoningContent,
		CreatedAt:        time.Now(),
	})
}

// snapshotSession persists the current UI session and history to disk.
func (s *RunService) snapshotSession(sessionID string) {
	if s.persister == nil || s.uiSessionMgr == nil || s.historySessionMgr == nil {
		return
	}
	sess := s.uiSessionMgr.Get(sessionID)
	if sess == nil {
		return
	}
	historyMsgs := s.historySessionMgr.History(sessionID)
	if historyMsgs == nil {
		return
	}
	if err := s.persister.SnapshotSession(sessionID, sess, historyMsgs); err != nil {
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

	sess := s.uiSessionMgr.Get(sessionID)
	if sess == nil {
		return
	}
	if sess.BrowserID == "" {
		return
	}
	s.broadcast.Broadcast(sess.BrowserID, broadcast.BrowserEvent{
		Type: "session_status",
		Data: map[string]any{
			"session_id": sessionID,
			"status":     string(sess.Status),
		},
	})
}
