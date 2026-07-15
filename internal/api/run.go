package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/session"

	adkagent "google.golang.org/adk/v2/agent"

	"github.com/glemsom/eitri/internal/agent"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	agentrunner "github.com/glemsom/eitri/internal/runner"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// sessionIDSetter is an optional interface for models that support prompt caching via session ID.
type sessionIDSetter interface {
	SetSessionID(string) *provider.OpenAIModel
}

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

// runState holds ADK event channels, cancel, and the SSE broadcast state for one run.
type runState struct {
	SessionID string
	Events    <-chan *session.Event
	Errors    <-chan error
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	SSE *runstate.State
}

func (rs *runState) finish() {
	rs.doneOnce.Do(func() {
		close(rs.Done)
	})
}

// RunManager manages active runs per session.
type RunManager struct {
	mu           sync.Mutex
	active       map[string]*runState
	runnerMgr    *agentrunner.Manager
	sessionMgr   *executor.SessionManager
	uiSessionMgr *uisession.Manager
	skillsSvc    *skills.Service
	providerID   string
	baseURL      string
	apiKey       string
	providerAuth json.RawMessage
	modelName    string
	systemPrompt string
	maxTurns     int

	configPath   string
	httpClient   *http.Client
	copilotOAuth provider.GitHubCopilotOAuthConfig
}

const completedRunRetention = 5 * time.Second

// NewRunManager creates a run manager.
func NewRunManager(runnerMgr *agentrunner.Manager, sessionMgr *executor.SessionManager) *RunManager {
	return &RunManager{
		active:     make(map[string]*runState),
		runnerMgr:  runnerMgr,
		sessionMgr: sessionMgr,
	}
}

// SetSkillsService sets the skills service for the run manager.
func (rm *RunManager) SetSkillsService(svc *skills.Service) {
	rm.skillsSvc = svc
}

// SetUISessionManager sets the UI session manager for the run manager.
func (rm *RunManager) SetUISessionManager(mgr *uisession.Manager) {
	rm.uiSessionMgr = mgr
}

// SetAuthRefresh configures request-time auth refresh persistence.
func (rm *RunManager) SetAuthRefresh(configPath string, httpClient *http.Client, copilotOAuth provider.GitHubCopilotOAuthConfig) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.configPath = configPath
	rm.httpClient = httpClient
	rm.copilotOAuth = copilotOAuth
}

// UpdateProviderConfig stores provider config for creating model instances.
func (rm *RunManager) UpdateProviderConfig(cfg *config.Config) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.providerID = cfg.Provider
	rm.baseURL = cfg.BaseURL
	rm.apiKey = cfg.APIKey
	rm.providerAuth = append(rm.providerAuth[:0], cfg.ProviderAuth...)
	rm.modelName = cfg.Model
	rm.systemPrompt = cfg.SystemPrompt
	rm.maxTurns = cfg.MaxTurns
}

func (rm *RunManager) persistAuthUpdate(update *provider.AuthUpdate) error {
	if update == nil {
		return nil
	}

	rm.mu.Lock()
	configPath := rm.configPath
	rm.mu.Unlock()
	if configPath == "" {
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("failed to load config for refreshed provider auth: %w", err)
	}
	cfg.APIKey = update.APIKey
	cfg.ProviderAuth = append(json.RawMessage(nil), update.ProviderAuth...)
	if err := config.Save(configPath, cfg); err != nil {
		return fmt.Errorf("failed to save refreshed provider auth: %w", err)
	}
	if rm.runnerMgr != nil {
		rm.runnerMgr.Invalidate()
	}
	rm.UpdateProviderConfig(cfg)
	return nil
}

// StartRun starts a new agent run for a session. Returns error if run already active.
func (rm *RunManager) StartRun(ctx context.Context, sessionID, userMessage string) (sessionSkillContext, error) {
	rm.mu.Lock()
	if existing, exists := rm.active[sessionID]; exists {
		select {
		case <-existing.Done:
			delete(rm.active, sessionID)
		default:
			rm.mu.Unlock()
			return sessionSkillContext{}, fmt.Errorf("session %s already has an active run", sessionID)
		}
	}
	providerID := rm.providerID
	baseURL := rm.baseURL
	apiKey := rm.apiKey
	providerAuth := append(json.RawMessage(nil), rm.providerAuth...)
	modelName := rm.modelName
	systemPrompt := rm.systemPrompt
	maxTurns := rm.maxTurns
	httpClient := rm.httpClient
	copilotOAuth := rm.copilotOAuth
	rm.mu.Unlock()

	if baseURL == "" || modelName == "" {
		return sessionSkillContext{}, fmt.Errorf("provider not configured: set base_url and model in settings")
	}

	if providerID == "" {
		providerID = "opencode_go"
	}
	skillCtx := rm.resolveSessionSkillContext(sessionID)
	runSystemPrompt := systemPrompt
	if note := skillCtx.systemPromptNote(); note != "" {
		if runSystemPrompt != "" {
			runSystemPrompt += "\n\n"
		}
		runSystemPrompt += note
	}

	chatResult, err := provider.NewChatModel(ctx, provider.ChatRequest{
		ProviderID:   providerID,
		BaseURL:      baseURL,
		APIKey:       apiKey,
		ProviderAuth: providerAuth,
		Model:        modelName,
	}, provider.ChatOptions{
		HTTPClient:         httpClient,
		GitHubCopilotOAuth: copilotOAuth,
	})
	if err != nil {
		return sessionSkillContext{}, fmt.Errorf("failed to create model: %w", err)
	}
	if err := rm.persistAuthUpdate(chatResult.AuthUpdate); err != nil {
		return sessionSkillContext{}, err
	}

	effectiveAPIKey := apiKey
	effectiveProviderAuth := append(json.RawMessage(nil), providerAuth...)
	if chatResult.AuthUpdate != nil {
		effectiveAPIKey = chatResult.AuthUpdate.APIKey
		effectiveProviderAuth = append(json.RawMessage(nil), chatResult.AuthUpdate.ProviderAuth...)
	}

	// Set session ID on model for prompt caching before skill context wrapping.
	if setter, ok := chatResult.Model.(sessionIDSetter); ok {
		chatResult.Model = setter.SetSessionID(sessionID)
	}

	llm := newSkillContextLLM(chatResult.Model, skillCtx.Activations)

	var ag adkagent.Agent
	var agErr error
	if rm.skillsSvc != nil {
		ag, agErr = agent.NewAgentWithPromptAndSkills(llm, rm.sessionMgr, runSystemPrompt, rm.skillsSvc, rm.uiSessionMgr)
	} else {
		ag, agErr = agent.NewAgentWithPrompt(llm, rm.sessionMgr, runSystemPrompt)
	}
	if agErr != nil {
		return sessionSkillContext{}, fmt.Errorf("failed to create agent: %w", agErr)
	}

	cfg := &config.Config{Provider: providerID, APIKey: effectiveAPIKey, ProviderAuth: effectiveProviderAuth, BaseURL: baseURL, Model: modelName, SystemPrompt: agent.BuildSystemPrompt(runSystemPrompt, rm.skillsSvc)}
	r, err := rm.runnerMgr.GetOrCreate(cfg, ag)
	if err != nil {
		return sessionSkillContext{}, fmt.Errorf("failed to get runner: %w", err)
	}

	content := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: userMessage}},
	}

	eventCh, errCh, cancel := rm.runnerMgr.Run(ctx, r, sessionID, sessionID, content, maxTurns)

	state := &runState{
		SessionID:   sessionID,
		Events:      eventCh,
		Errors:      errCh,
		Cancel:      cancel,
		StartedAt:   time.Now(),
		Done:        make(chan struct{}),
		SSE:         runstate.New(),
	}

	slog.Info("run started", slog.String("session_id", sessionID), slog.String("provider", providerID), slog.String("model", modelName))

	rm.mu.Lock()
	rm.active[sessionID] = state
	rm.mu.Unlock()

	go func() {
		<-state.Done
		slog.Info("run done", slog.String("session_id", sessionID))
		time.Sleep(completedRunRetention)
		rm.mu.Lock()
		if rm.active[sessionID] == state {
			delete(rm.active, sessionID)
		}
		rm.mu.Unlock()
	}()

	return skillCtx, nil
}

func (rm *RunManager) lookupRun(sessionID string) *runState {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.active[sessionID]
}

// ActiveRun returns unfinished active run state for a session.
func (rm *RunManager) ActiveRun(sessionID string) *runState {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return nil
	}
	select {
	case <-state.Done:
		return nil
	default:
		return state
	}
}

// CancelRun cancels the active run for a session.
func (rm *RunManager) CancelRun(sessionID string) bool {
	rm.mu.Lock()
	state, exists := rm.active[sessionID]
	if exists {
		delete(rm.active, sessionID)
	}
	rm.mu.Unlock()

	if !exists {
		return false
	}
	slog.Info("run canceled", slog.String("session_id", sessionID))
	// Broadcast done event so SSE subscribers re-enable composer (issue #103)
	state.SSE.Broadcast(runstate.SSEEvent{Type: "done"})
	state.Cancel()
	state.finish()
	return true
}

// CancelAll cancels every active run.
func (rm *RunManager) CancelAll() {
	rm.mu.Lock()
	states := make([]*runState, 0, len(rm.active))
	for sessionID, state := range rm.active {
		delete(rm.active, sessionID)
		states = append(states, state)
	}
	rm.mu.Unlock()

	for _, state := range states {
		slog.Info("run canceled", slog.String("session_id", state.SessionID))
		state.Cancel()
		state.finish()
	}
}

// CloseSession cancels an active run for sessionID and closes its executor.
func (rm *RunManager) CloseSession(sessionID string) error {
	rm.CancelRun(sessionID)
	if rm.sessionMgr == nil {
		return nil
	}
	return rm.sessionMgr.Close(sessionID)
}

func (rm *RunManager) subscribe(sessionID string) (uint64, <-chan runstate.SSEEvent, bool) {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return 0, nil, false
	}
	return state.SSE.Subscribe()
}

func (rm *RunManager) unsubscribe(sessionID string, subscriberID uint64) {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.Unsubscribe(subscriberID)
}

func (rm *RunManager) notifySessionClosed(sessionID, message string) {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.BroadcastClosed(message)
}

func (rm *RunManager) notifyAllStreamsClosed(message string) {
	rm.mu.Lock()
	states := make([]*runState, 0, len(rm.active))
	for _, state := range rm.active {
		states = append(states, state)
	}
	rm.mu.Unlock()

	for _, state := range states {
		state.SSE.BroadcastClosed(message)
	}
}

// AppendEvent processes ADK session events and sends SSE events.
// Returns the accumulated assistant response text for storage in the UI session.
func (rm *RunManager) AppendEvent(state *runState, w *runstate.Writer) string {
	messageID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	for {
		select {
		case evt, ok := <-state.Events:
			if !ok {
				select {
				case err, ok := <-state.Errors:
					if ok && err != nil {
						return rm.finishWithError(state, w, messageID, err)
					}
				default:
				}
				content := state.SSE.BufferString()
				if content != "" {
					if rm.uiSessionMgr != nil {
						rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
							Role:      "assistant",
							Content:   content,
							CreatedAt: time.Now(),
						})
					}
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
							// Emit component SSE event with input args
							argsMap := fc.Args
							compName, _ := argsMap["name"].(string)
							compData, _ := argsMap["data"].(map[string]interface{})
							state.SSE.Broadcast(runstate.SSEEvent{
								Type: "component",
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
				if rm.uiSessionMgr != nil {
					rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
						Role:      "assistant",
						Content:   content,
						CreatedAt: time.Now(),
					})
				}
				w.Done(messageID, runstate.EstimateUsage(content))
				state.finish()
				return content
			}

		case err, ok := <-state.Errors:
			if !ok {
				content := state.SSE.BufferString()
				if content != "" {
					if rm.uiSessionMgr != nil {
						rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
							Role:      "assistant",
							Content:   content,
							CreatedAt: time.Now(),
						})
					}
				}
				w.Done(messageID, runstate.EstimateUsage(content))
				state.finish()
				return content
			}
			if err != nil {
				return rm.finishWithError(state, w, messageID, err)
			}

		case <-state.Done:
			state.SSE.BroadcastDone(messageID, runstate.EstimateUsage(state.SSE.BufferString()))
			state.finish()
			return state.SSE.BufferString()
		}
	}
}

func (rm *RunManager) finishWithError(state *runState, w *runstate.Writer, messageID string, err error) string {
	var maxTurnsErr *agentrunner.MaxTurnsExceededError
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
		if rm.uiSessionMgr != nil {
			rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
				Role:      "assistant",
				Content:   content,
				CreatedAt: time.Now(),
			})
		}
		w.Done(messageID, runstate.EstimateUsage(content))
		state.finish()
		return content
	}
	w.Error(runstate.FormatErrorMessage(err))
	state.finish()
	return state.SSE.BufferString()
}
