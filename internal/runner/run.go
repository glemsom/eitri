package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	runtimeDebug "runtime/debug"
	"time"

	"github.com/glemsom/eitri/internal/llm"

	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/tool"
)

// MaxTurnsExceededError reports that a run hit its configured turn cap.
type MaxTurnsExceededError struct {
	Limit int
}

func (e *MaxTurnsExceededError) Error() string {
	return fmt.Sprintf("max turns limit reached: %d", e.Limit)
}

// StartRun starts a new agent run for a session with an explicit RunConfig.
// Returns warnings about stale skills, and error if run fails.
func (s *RunService) StartRun(ctx context.Context, sessionID, userMessage string, cfg RunConfig) ([]string, error) {
	return s.startRunWithConfig(ctx, sessionID, userMessage, cfg)
}

func (s *RunService) startRunWithConfig(ctx context.Context, sessionID, userMessage string, cfg RunConfig) ([]string, error) {
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

	skillCtx := s.resolveSessionSkillContext(sessionID)

	fullSystemPrompt, err := buildSystemPrompt(cfg, skillCtx, s.skillsSvc)
	if err != nil {
		return nil, err
	}

	llmSvc, toolReg, err := buildLLMService(ctx, cfg, sessionID, s.debugRecorder, s.persistAuth, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr)
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
		var historyMgr HistoryManager
		if s.historySessionMgr != nil {
			historyMgr = newSessionHistoryManager(s.historySessionMgr, s.uiSessionMgr, sessionID)
		} else {
			historyMgr = newRequestHistoryManager(req)
		}
		confirmer := newFuncConfirmer(s.confirmPath)

		err := RunAgent(runCtx, AgentConfig{
			Service:       llmSvc,
			Request:       req,
			MaxTurns:      maxTurnsVal,
			MaxHistory:    maxHistory,
			SSEWriter:     w,
			Tools:         toolReg,
			HistoryMgr:    historyMgr,
			Confirmer:     confirmer,
			UISessionMgr:  s.uiSessionMgr,
			SessionID:     sessionID,
			ContextWindow: contextWindowTokens,
			CrashDumpFunc: s.crashDumpFunc,
			Turns:         &state.Turns,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				content := sseState.BufferString()
				reasoningContent := sseState.ReasoningBufferString()
				if content != "" {
					s.appendToSession(sessionID, content, reasoningContent)
				}
				return
			}

			var maxTurnsErr *MaxTurnsExceededError
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
	s.broadcast.Broadcast(sess.BrowserID, BrowserEvent{
		Type: "session_status",
		Data: map[string]any{
			"session_id": sessionID,
			"status":     string(sess.Status),
		},
	})
}
