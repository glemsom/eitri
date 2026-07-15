// Package runner provides RunService — the seam for run lifecycle management.
// It owns agent building, runner cache, SSE broadcast, session persistence,
// and auth persistence callbacks.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/genai"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"

	eitriagent "github.com/glemsom/eitri/internal/agent"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// RunState holds ADK event channels, cancel, and the SSE broadcast state for one run.
type RunState struct {
	SessionID string
	Events    <-chan *session.Event
	Errors    <-chan error
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
	RunnerManager  *Manager
	SessionManager *executor.SessionManager
	UISessionMgr   *uisession.Manager
	SkillsService  *skills.Service
}

// RunService owns the run lifecycle: agent building, runner cache,
// SSE broadcast, session persistence, and auth refresh callbacks.
type RunService struct {
	mu            sync.Mutex
	active        map[string]*RunState
	runnerMgr     *Manager
	sessionMgr    *executor.SessionManager
	uiSessionMgr  *uisession.Manager
	skillsSvc     *skills.Service
	providerID    string
	baseURL       string
	apiKey        string
	providerAuth  json.RawMessage
	modelName     string
	systemPrompt  string
	maxTurns      int
	persistAuth   PersistAuthFunc
}

const completedRunRetention = 5 * time.Second

// NewRunService creates a RunService with the given dependencies.
func NewRunService(deps RunServiceDeps) *RunService {
	return &RunService{
		active:       make(map[string]*RunState),
		runnerMgr:    deps.RunnerManager,
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

// UpdateProviderConfig stores provider config for creating model instances.
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
}

// InvalidateRunners clears all cached runners so they are recreated on next StartRun.
func (s *RunService) InvalidateRunners() {
	s.runnerMgr.Invalidate()
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
	providerAuth := append(json.RawMessage(nil), s.providerAuth...)
	modelName := s.modelName
	systemPrompt := s.systemPrompt
	maxTurns := s.maxTurns
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

	// Build PersistAuth options
	chatOpts := provider.ChatOptions{}
	if s.persistAuth != nil {
		chatOpts.PersistAuth = s.persistAuth
	}

	chatResult, err := provider.NewChatModel(ctx, provider.ChatRequest{
		ProviderID:   providerID,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		ProviderAuth: providerAuth,
		Model:        modelName,
	}, chatOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create model: %w", err)
	}

	effectiveAPIKey := apiKey
	effectiveProviderAuth := append(json.RawMessage(nil), providerAuth...)
	if chatResult.AuthUpdate != nil {
		effectiveAPIKey = chatResult.AuthUpdate.APIKey
		effectiveProviderAuth = append(json.RawMessage(nil), chatResult.AuthUpdate.ProviderAuth...)
	}

	// Set session ID on model for prompt caching before skill context wrapping.
	chatModel := chatResult.Model
	if setter, ok := chatModel.(interface{ SetSessionID(string) *provider.OpenAIModel }); ok {
		chatModel = setter.SetSessionID(sessionID)
	}

	// Resolve skill context for this session
	skillCtx := s.resolveSessionSkillContext(sessionID)
	llm := s.wrapWithSkillContext(chatModel, skillCtx)

	var ag adkagent.Agent
	var agErr error
	if s.skillsSvc != nil {
		ag, agErr = eitriagent.NewAgentWithPromptAndSkills(llm, s.sessionMgr, runSystemPrompt, s.skillsSvc, s.uiSessionMgr)
	} else {
		ag, agErr = eitriagent.NewAgentWithPrompt(llm, s.sessionMgr, runSystemPrompt)
	}
	if agErr != nil {
		return nil, fmt.Errorf("failed to create agent: %w", agErr)
	}

	cfg := &config.Config{
		Provider:     providerID,
		APIKey:       effectiveAPIKey,
		ProviderAuth: effectiveProviderAuth,
		BaseURL:      baseURL,
		Model:        modelName,
		SystemPrompt: runSystemPrompt,
	}
	r, err := s.runnerMgr.GetOrCreate(cfg, ag)
	if err != nil {
		return nil, fmt.Errorf("failed to get runner: %w", err)
	}

	content := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: userMessage}},
	}

	eventCh, errCh, cancel := s.runnerMgr.Run(ctx, r, sessionID, sessionID, content, maxTurns)

	state := &RunState{
		SessionID:   sessionID,
		Events:      eventCh,
		Errors:      errCh,
		Cancel:      cancel,
		StartedAt:   time.Now(),
		Done:        make(chan struct{}),
		SSE:         runstate.New(),
	}

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", providerID), slog.String("model", modelName))

	s.mu.Lock()
	s.active[sessionID] = state
	s.mu.Unlock()

	go func() {
		<-state.Done
		slog.Info("run done", slog.String("session_id", sessionID))
		time.Sleep(completedRunRetention)
		s.mu.Lock()
		if s.active[sessionID] == state {
			delete(s.active, sessionID)
		}
		s.mu.Unlock()
	}()

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

// AppendEvent processes ADK session events and broadcasts SSE events.
// Returns the accumulated assistant response text.
func (s *RunService) AppendEvent(state *RunState) string {
	w := runstate.NewWriter(state.SSE)
	return s.appendEvent(state, w)
}

func (s *RunService) appendEvent(state *RunState, w *runstate.Writer) string {
	messageID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	for {
		select {
		case evt, ok := <-state.Events:
			if !ok {
				select {
				case err, ok := <-state.Errors:
					if ok && err != nil {
						return s.finishWithError(state, w, messageID, err)
					}
				default:
				}
				content := state.SSE.BufferString()
				if content != "" {
					s.appendToSession(state.SessionID, content)
				}
				w.Done(messageID, runstate.EstimateUsage(content))
				state.finish()
				return content
			}
			if evt == nil {
				continue
			}

			turnComplete := !evt.Partial && !contentHasFunctionCalls(evt.Content) && !contentHasFunctionResponses(evt.Content)
			acceptFinalText := turnComplete && state.SSE.BufferString() == ""

			if evt.Content != nil {
				for _, part := range evt.Content.Parts {
					if part == nil {
						continue
					}
					if part.Text != "" && (!turnComplete || acceptFinalText) {
						state.SSE.AppendBuffer(part.Text)
						w.Token(part.Text)
					}
					if fc := part.FunctionCall; fc != nil {
						if fc.Name == "render_component" {
							argsMap := fc.Args
							compName, _ := argsMap["name"].(string)
							compData, _ := argsMap["data"].(map[string]interface{})
							state.SSE.Broadcast(runstate.SSEEvent{
								Type: "component",
								Kind: runstate.RenderKindComponent,
								Name: compName,
								Data: compData,
							})
						} else {
							w.ToolCall(fc.Name, fc.Args)
						}
					}
					if fr := part.FunctionResponse; fr != nil {
						if fr.Name == "render_component" {
							state.SSE.AppendBuffer("[Component rendered]")
						} else {
							w.ToolResult(fr.Name, fr.Response)
						}
					}
				}
			}

			if turnComplete {
				content := state.SSE.BufferString()
				s.appendToSession(state.SessionID, content)
				w.Done(messageID, runstate.EstimateUsage(content))
				state.finish()
				return content
			}

		case err, ok := <-state.Errors:
			if !ok {
				content := state.SSE.BufferString()
				if content != "" {
					s.appendToSession(state.SessionID, content)
				}
				w.Done(messageID, runstate.EstimateUsage(content))
				state.finish()
				return content
			}
			if err != nil {
				return s.finishWithError(state, w, messageID, err)
			}

		case <-state.Done:
			state.SSE.BroadcastDone(messageID, runstate.EstimateUsage(state.SSE.BufferString()))
			state.finish()
			return state.SSE.BufferString()
		}
	}
}

func (s *RunService) finishWithError(state *RunState, w *runstate.Writer, messageID string, err error) string {
	var maxTurnsErr *MaxTurnsExceededError
	if errors.As(err, &maxTurnsErr) {
		content := state.SSE.BufferString()
		limitMsg := runstate.MaxTurnsMessage(maxTurnsErr.Limit)
		if content == "" {
			state.SSE.AppendBuffer(limitMsg)
			content = limitMsg
		} else {
			state.SSE.AppendBuffer("\n\n" + limitMsg)
			content += "\n\n" + limitMsg
		}
		s.appendToSession(state.SessionID, content)
		w.Done(messageID, runstate.EstimateUsage(content))
		state.finish()
		return content
	}
	w.Error(runstate.FormatErrorMessage(err))
	state.finish()
	return state.SSE.BufferString()
}

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

type skillContextLLM struct {
	base        model.LLM
	activations []runSkillActivation
}

func (s *RunService) wrapWithSkillContext(base model.LLM, ctx sessionSkillContext) model.LLM {
	if len(ctx.Activations) == 0 {
		return base
	}
	copied := append([]runSkillActivation(nil), ctx.Activations...)
	return &skillContextLLM{base: base, activations: copied}
}

func (m *skillContextLLM) Name() string {
	return m.base.Name()
}

func (m *skillContextLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if len(m.activations) == 0 {
		return m.base.GenerateContent(ctx, req, stream)
	}

	cloned := *req
	cloned.Contents = append(skillActivationContents(m.activations), req.Contents...)
	return m.base.GenerateContent(ctx, &cloned, stream)
}

func skillActivationContents(activations []runSkillActivation) []*genai.Content {
	contents := make([]*genai.Content, 0, len(activations)*2)
	for i, activation := range activations {
		callID := fmt.Sprintf("active_skill_%d_%s", i, activation.Name)
		contents = append(contents,
			&genai.Content{
				Role: genai.RoleModel,
				Parts: []*genai.Part{{
					FunctionCall: &genai.FunctionCall{
						ID:   callID,
						Name: "activate_skill",
						Args: map[string]any{"name": activation.Name},
					},
				}},
			},
			&genai.Content{
				Role: genai.RoleUser,
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       callID,
						Name:     "activate_skill",
						Response: map[string]any{"content": activation.Content},
					},
				}},
			},
		)
	}
	return contents
}

// ————— ADK content helpers —————

func contentHasFunctionCalls(content *genai.Content) bool {
	if content == nil {
		return false
	}
	for _, part := range content.Parts {
		if part != nil && part.FunctionCall != nil {
			return true
		}
	}
	return false
}

func contentHasFunctionResponses(content *genai.Content) bool {
	if content == nil {
		return false
	}
	for _, part := range content.Parts {
		if part != nil && part.FunctionResponse != nil {
			return true
		}
	}
	return false
}
