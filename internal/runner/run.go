package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	runtimeDebug "runtime/debug"
	"time"

	"github.com/glemsom/eitri/internal/litellm"
	"github.com/glemsom/eitri/internal/debug"

	"github.com/glemsom/eitri/internal/history"
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
	s.mu.Lock()
	if existing, exists := s.active[sessionID]; exists {
		select {
		case <-existing.Done:
			delete(s.active, sessionID)
		default:
			s.mu.Unlock()
			return nil, fmt.Errorf("session %s already has an active run", sessionID)
		}
	}
	providerID := cfg.ProviderID
	baseURL := cfg.BaseURL
	apiKey := cfg.APIKey
	modelName := cfg.ModelName
	systemPrompt := cfg.SystemPrompt
	maxTurns := cfg.MaxTurns
	maxHistory := cfg.MaxHistory
	providerAuth := cfg.ProviderAuth
	workspace := cfg.Workspace
	contextWindowTokens := cfg.ContextWindowTokens
	s.mu.Unlock()

	// Store parent config for sub-agent setup
	s.parentCfgMu.Lock()
	s.parentCfgs[sessionID] = cfg
	s.parentCfgMu.Unlock()

	if baseURL == "" || modelName == "" {
		return nil, fmt.Errorf("provider not configured: set base_url and model in settings")
	}

	if providerID == "" {
		providerID = "opencode_go"
	}

	runSystemPrompt := systemPrompt
	if runSystemPrompt == "" {
		runSystemPrompt = history.DefaultSystemPrompt
	}

	skillCtx := s.resolveSessionSkillContext(sessionID)

	fullSystemPrompt := runSystemPrompt

	repoInstructions, err := readRepositoryInstructions(workspace)
	if err != nil {
		return nil, fmt.Errorf("read repository instructions: %w", err)
	}
	if repoInstructions != "" {
		fullSystemPrompt += "\n\n" + repoInstructions
	}
	if s.skillsSvc != nil {
		catalog := s.skillsSvc.SkillsCatalogXML()
		if catalog != "" {
			fullSystemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context."
		}
	}
	for _, activation := range skillCtx.Activations {
		fullSystemPrompt += "\n\nActivated skill \"" + activation.Name + "\":\n" + activation.Content
	}

	reqAuth := provider.ResolveAuthRequest{
		ProviderID:   providerID,
		APIKey:       apiKey,
		ProviderAuth: providerAuth,
	}
	authPersist := s.persistAuth
	resolvedKey, authUpdate, authErr := provider.ResolveAuth(ctx, reqAuth, authPersist)
	if authErr != nil {
		return nil, fmt.Errorf("auth resolution: %w", authErr)
	}
	if resolvedKey != "" {
		apiKey = resolvedKey
	}
	_ = authUpdate

	adapterCfg := litellm.AdapterConfig{
		ProviderID: providerID,
		Model:      modelName,
		BaseURL:    baseURL,
		APIKey:     apiKey,
	}

	// If debug recorder is enabled, wrap the default transport for HTTP trace recording
	if s.debugRecorder != nil {
		adapterCfg.RoundTripper = debug.NewRecordingRoundTripper(nil, s.debugRecorder, sessionID, providerID)
	}

	llm, err := litellm.NewLLMService(adapterCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM service: %w", err)
	}

	toolReg := buildBaseToolRegistry(cfg, s.skillDirectories(), s.skillsSvc, s.uiSessionMgr)
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

	req := &litellm.Request{
		Model:  modelName,
		Stream: true,
	}

	// Set session-scoped prompt cache key if the provider supports it
	providerDesc, _ := provider.Describe(providerID)
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

	s.mu.Lock()
	s.active[sessionID] = state
	s.mu.Unlock()

	go func() {
		defer func() {
			state.finish()
			time.Sleep(completedRunRetention)
			s.mu.Lock()
			if s.active[sessionID] == state {
				delete(s.active, sessionID)
			}
			s.mu.Unlock()

			// Clean up parent config for sub-agent setup
			s.parentCfgMu.Lock()
			delete(s.parentCfgs, sessionID)
			s.parentCfgMu.Unlock()
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

		err := RunAgent(runCtx, llm, req, maxTurnsVal, maxHistory, w, toolReg, historyMgr, confirmer, s.uiSessionMgr, sessionID, contextWindowTokens, s.crashDumpFunc, &state.Turns)
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

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", providerID), slog.String("model", modelName))

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


// broadcastSessionStatusUpdate updates the session status and broadcasts the change
// to all browser-level SSE subscribers for this session's browser.
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
	s.BroadcastToBrowser(sess.BrowserID, BrowserEvent{
		Type: "session_status",
		Data: map[string]interface{}{
			"session_id": sessionID,
			"status":     string(sess.Status),
		},
	})
}
