package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/glemsom/eitri/internal/litellm"
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

// StartRun starts a new agent run for a session.
// Returns warnings about stale skills, and error if run fails.
func (s *RunService) StartRun(ctx context.Context, sessionID, userMessage string) ([]string, error) {
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
	providerID := s.providerID
	baseURL := s.baseURL
	apiKey := s.apiKey
	modelName := s.modelName
	systemPrompt := s.systemPrompt
	maxTurns := s.maxTurns
	maxHistory := s.maxHistory
	allowedReadPaths := s.allowedReadPaths
	providerAuth := s.providerAuth
	s.mu.Unlock()

	if baseURL == "" || modelName == "" {
		return nil, fmt.Errorf("provider not configured: set base_url and model in settings")
	}

	if providerID == "" {
		providerID = "opencode_go"
	}

	runSystemPrompt := systemPrompt
	if runSystemPrompt == "" {
		runSystemPrompt = "You are Eitri, an AI coding assistant."
	}

	// Resolve skill context for this session
	skillCtx := s.resolveSessionSkillContext(sessionID)

	// Build system prompt with skills catalog
	fullSystemPrompt := runSystemPrompt
	if s.skillsSvc != nil {
		catalog := s.skillsSvc.SkillsCatalogXML()
		if catalog != "" {
			fullSystemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context."
		}
	}
	// Append pre-activated skill content
	for _, activation := range skillCtx.Activations {
		fullSystemPrompt += "\n\nActivated skill \"" + activation.Name + "\":\n" + activation.Content
	}

	// Resolve auth (token refresh for github_copilot, etc.)
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
	_ = authUpdate // caller may persist auth update if needed

	// Create LLM service via litellm adapter factory
	llm, err := litellm.NewLLMService(litellm.AdapterConfig{
		ProviderID: providerID,
		Model:      modelName,
		BaseURL:    baseURL,
		APIKey:     apiKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LLM service: %w", err)
	}

	// Build tool registry with built-in tools
	toolReg := tool.NewRegistry()
	toolReg.Register(tool.NewBashTool(s.sessionMgr))
	toolReg.Register(tool.NewGlobTool(s.sessionMgr.Workspace()))
	toolReg.Register(tool.NewGrepTool(s.sessionMgr.Workspace()))
	toolReg.Register(tool.NewReadTool(s.sessionMgr.Workspace(), s.skillDirectories(), allowedReadPaths))
	toolReg.Register(tool.NewWriteTool(s.sessionMgr.Workspace()))
	toolReg.Register(tool.NewEditTool(s.sessionMgr.Workspace()))
	toolReg.Register(tool.NewRenderMermaidDiagram())
	toolReg.Register(tool.NewRenderQuickReplies())
	if s.skillsSvc != nil {
		toolReg.Register(tool.NewSkill(s.skillsSvc, s.uiSessionMgr))
	}

	// Set up session manager with system prompt and user message
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

	// Start agent loop in background goroutine
	go func() {
		defer func() {
			state.finish()
			time.Sleep(completedRunRetention)
			s.mu.Lock()
			if s.active[sessionID] == state {
				delete(s.active, sessionID)
			}
			s.mu.Unlock()
		}()

		if s.uiSessionMgr != nil {
			defer s.uiSessionMgr.UpdateStatus(sessionID, uisession.StatusIdle)
		}

		w := runstate.NewWriter(sseState)

		err := RunAgent(runCtx, llm, req, maxTurnsVal, maxHistory, w, toolReg, s.historySessionMgr, s.uiSessionMgr, sessionID, s.confirmPath, s.contextWindowTokens)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				// Save partial content to session on cancellation so the UI
				// reflects what was generated before the user pressed stop.
				content := sseState.BufferString()
				if content != "" {
					s.appendToSession(sessionID, content)
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
				w.Done(fmt.Sprintf("msg_%d", time.Now().UnixNano()), runstate.EstimateUsage(content))
				s.appendToSession(sessionID, content)
				return
			}

			// Error already broadcast by RunAgent via sseWriter.Error()
			return
		}

		// Success: append final assistant response to session
		content := sseState.BufferString()
		if content != "" {
			s.appendToSession(sessionID, content)
		}
	}()

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", providerID), slog.String("model", modelName))

	return skillCtx.Warnings, nil
}

// appendToSession persists an assistant message to the UI session.
func (s *RunService) appendToSession(sessionID, content string) {
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
			return
		}
	}
	s.uiSessionMgr.AppendMessage(sessionID, uisession.Message{
		Role:      "assistant",
		Content:   content,
		CreatedAt: time.Now(),
	})
}
