// Package runner provides RunService — the seam for run lifecycle management.
// It owns agent loop execution, SSE broadcast, session persistence,
// and auth persistence callbacks.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/litellm"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
	"github.com/glemsom/eitri/internal/tool"
)

// RunState holds SSE broadcast state and cancel for one run.
type RunState struct {
	SessionID string
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	SSE *runstate.State
}

func (rs *RunState) finish() {
	rs.doneOnce.Do(func() {
		close(rs.Done)
	})
}

// PersistAuthFunc is the callback type for persisting refreshed auth state.
type PersistAuthFunc = provider.PersistAuthFunc

// RunServiceDeps holds the dependencies for RunService.
type RunServiceDeps struct {
	SessionManager *executor.SessionManager
	UISessionMgr   *uisession.Manager
	SkillsService  *skills.Service
}

// RunService owns the run lifecycle: agent loop execution,
// SSE broadcast, session persistence, and auth refresh callbacks.
type RunService struct {
	mu              sync.Mutex
	active          map[string]*RunState
	sessionMgr      *executor.SessionManager
	uiSessionMgr    *uisession.Manager
	skillsSvc       *skills.Service
	historySessionMgr *history.SessionManager
	providerID      string
	baseURL         string
	apiKey          string
	providerAuth    json.RawMessage
	modelName       string
	systemPrompt    string
	maxTurns        int
	maxHistory      int
	persistAuth     PersistAuthFunc
}

const completedRunRetention = 5 * time.Second

// NewRunService creates a RunService with the given dependencies.
func NewRunService(deps RunServiceDeps) *RunService {
	return &RunService{
		active:       make(map[string]*RunState),
		sessionMgr:   deps.SessionManager,
		uiSessionMgr: deps.UISessionMgr,
		skillsSvc:    deps.SkillsService,
	}
}

// SetSkillsService sets the skills service.
func (s *RunService) SetSkillsService(svc *skills.Service) {
	s.skillsSvc = svc
}

// SetUISessionManager sets the UI session manager.
func (s *RunService) SetUISessionManager(mgr *uisession.Manager) {
	s.uiSessionMgr = mgr
}

// SetPersistAuth sets the auth persistence callback.
func (s *RunService) SetPersistAuth(fn PersistAuthFunc) {
	s.persistAuth = fn
}

// UpdateProviderConfig stores provider config for creating LLM service instances.
func (s *RunService) UpdateProviderConfig(cfg *config.Config) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerID = cfg.Provider
	s.baseURL = cfg.BaseURL
	s.apiKey = cfg.APIKey
	s.providerAuth = append(s.providerAuth[:0], cfg.ProviderAuth...)
	s.modelName = cfg.Model
	s.systemPrompt = cfg.SystemPrompt
	s.maxTurns = cfg.MaxTurns
	s.maxHistory = cfg.MaxHistory
	s.historySessionMgr = history.NewSessionManager(cfg.MaxHistory)
}

// InvalidateRunners clears any cached state so new runs pick up config changes.
// No-op in the new architecture (no runner cache).
func (s *RunService) InvalidateRunners() {
	// No runner cache to invalidate; config is read fresh on each StartRun.
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
			fullSystemPrompt += "\n\nAvailable skills:\n" + catalog + "\n\nWhen a task matches a skill description, call activate_skill with the skill name before proceeding. This loads the skill's instructions, references, and scripts into context."
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
	toolReg.Register(tool.NewFileViewer(s.sessionMgr.Workspace(), s.skillDirectories()))
	toolReg.Register(tool.NewFileEditor(s.sessionMgr.Workspace()))
	toolReg.Register(tool.NewRenderComponent())
	if s.skillsSvc != nil {
		toolReg.Register(tool.NewActivateSkill(s.skillsSvc, s.uiSessionMgr))
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

		err := RunAgent(runCtx, llm, req, maxTurnsVal, maxHistory, w, toolReg, s.historySessionMgr, sessionID)
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

// ActiveRun returns the active RunState for a session, or nil if none.
func (s *RunService) ActiveRun(sessionID string) *RunState {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	s.mu.Unlock()
	if !exists {
		return nil
	}
	select {
	case <-state.Done:
		return nil
	default:
		return state
	}
}

// lookupRun returns the run state without checking if done.
func (s *RunService) lookupRun(sessionID string) *RunState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sessionID]
}

// Subscribe attaches an SSE subscriber for an active run.
// Returns (subscriberID, channel, ok).
func (s *RunService) Subscribe(sessionID string) (uint64, <-chan runstate.SSEEvent, bool) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return 0, nil, false
	}
	return state.SSE.Subscribe()
}

// Unsubscribe removes an SSE subscriber.
func (s *RunService) Unsubscribe(sessionID string, id uint64) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.Unsubscribe(id)
}

// AppendEvent is no longer used. The agent loop owns SSE broadcast directly.
// Deprecated: kept for backward compatibility in tests; always returns "".
func (s *RunService) AppendEvent(state *RunState) string {
	return ""
}

// appendToSession persists an assistant message to the UI session.
func (s *RunService) appendToSession(sessionID, content string) {
	if s.uiSessionMgr != nil {
		s.uiSessionMgr.AppendMessage(sessionID, uisession.Message{
			Role:      "assistant",
			Content:   content,
			CreatedAt: time.Now(),
		})
	}
}

// Cancel cancels the active run for a session.
func (s *RunService) Cancel(sessionID string) bool {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	if exists {
		delete(s.active, sessionID)
	}
	s.mu.Unlock()

	if !exists {
		return false
	}
	slog.Info("run canceled", slog.String("session_id", sessionID))
	state.SSE.Broadcast(runstate.SSEEvent{Type: "done", Kind: runstate.RenderKindMarkdown})
	state.Cancel()
	state.finish()
	return true
}

// CancelAll cancels every active run.
func (s *RunService) CancelAll() {
	s.mu.Lock()
	states := make([]*RunState, 0, len(s.active))
	for sessionID, state := range s.active {
		delete(s.active, sessionID)
		states = append(states, state)
	}
	s.mu.Unlock()

	for _, state := range states {
		slog.Info("run canceled", slog.String("session_id", state.SessionID))
		state.Cancel()
		state.finish()
	}
}

// CloseSession cancels the active run and closes the executor session.
func (s *RunService) CloseSession(sessionID string) error {
	s.Cancel(sessionID)
	if s.historySessionMgr != nil {
		s.historySessionMgr.Close(sessionID)
	}
	if s.sessionMgr == nil {
		return nil
	}
	return s.sessionMgr.Close(sessionID)
}

// NotifySessionClosed broadcasts a closed event for a session.
func (s *RunService) NotifySessionClosed(sessionID, message string) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.BroadcastClosed(message)
}

// NotifyAllStreamsClosed broadcasts a closed event to all active sessions.
func (s *RunService) NotifyAllStreamsClosed(message string) {
	s.mu.Lock()
	states := make([]*RunState, 0, len(s.active))
	for _, state := range s.active {
		states = append(states, state)
	}
	s.mu.Unlock()

	for _, state := range states {
		state.SSE.BroadcastClosed(message)
	}
}

// ————— Helper types and functions —————

// MaxTurnsExceededError reports that a run hit its configured turn cap.
type MaxTurnsExceededError struct {
	Limit int
}

func (e *MaxTurnsExceededError) Error() string {
	return fmt.Sprintf("max turns limit reached: %d", e.Limit)
}

type runSkillActivation struct {
	Name    string
	Content string
}

type sessionSkillContext struct {
	Activations []runSkillActivation
	Warnings    []string
}

func (s *RunService) resolveSessionSkillContext(sessionID string) sessionSkillContext {
	if s == nil || s.uiSessionMgr == nil || s.skillsSvc == nil {
		return sessionSkillContext{}
	}

	names := s.uiSessionMgr.ActiveSkills(sessionID)
	if len(names) == 0 {
		return sessionSkillContext{}
	}

	resolved := sessionSkillContext{Activations: make([]runSkillActivation, 0, len(names))}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		skill := s.skillsSvc.Lookup(name)
		if skill == nil {
			s.uiSessionMgr.DeactivateSkill(sessionID, name)
			resolved.Warnings = append(resolved.Warnings, staleSkillWarning(name))
			continue
		}

		resources := skills.ListResources(skill.Path)
		resolved.Activations = append(resolved.Activations, runSkillActivation{
			Name:    skill.Name,
			Content: skills.SkillContent(skill.Body, resources, skill.Path),
		})
	}
	return resolved
}

func staleSkillWarning(name string) string {
	return fmt.Sprintf("Active Skill %q no longer available. Skipped for this Run and removed from active Skills.", name)
}

func (s *RunService) skillDirectories() []string {
	if s.skillsSvc == nil {
		return nil
	}
	return s.skillsSvc.SkillDirectories()
}
