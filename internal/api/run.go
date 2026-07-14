package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/session"

	adkagent "google.golang.org/adk/v2/agent"

	"github.com/glemsom/eitri/internal/agent"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
	"github.com/glemsom/eitri/internal/provider"
	agentrunner "github.com/glemsom/eitri/internal/runner"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// SSEEvent represents one SSE data packet sent to the browser.
type SSEEvent struct {
	Type      string      `json:"type"`
	Content   string      `json:"content,omitempty"`
	Name      string      `json:"name,omitempty"`
	Tool      string      `json:"tool,omitempty"`
	Args      interface{} `json:"args,omitempty"`
	Output    interface{} `json:"output,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Message   string      `json:"message,omitempty"`
	MessageID string      `json:"message_id,omitempty"`
	Usage     *tokenUsage `json:"usage,omitempty"`
}

// tokenUsage holds token count information for a completed run.
type tokenUsage struct {
	TotalTokens      int `json:"total_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// runState tracks one active assistant run per session.
type runState struct {
	SessionID string
	Events    <-chan *session.Event
	Errors    <-chan error
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	bufferMu sync.Mutex
	buffer   strings.Builder

	streamMu       sync.Mutex
	subscribers    map[uint64]chan SSEEvent
	nextSubscriber uint64
	streamsClosed  bool
	history        []SSEEvent
}

func (rs *runState) finish() {
	rs.doneOnce.Do(func() {
		close(rs.Done)
	})
}

func (rs *runState) appendBuffer(text string) {
	rs.bufferMu.Lock()
	defer rs.bufferMu.Unlock()
	rs.buffer.WriteString(text)
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

func (rs *runState) bufferString() string {
	rs.bufferMu.Lock()
	defer rs.bufferMu.Unlock()
	return rs.buffer.String()
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
		subscribers: make(map[uint64]chan SSEEvent),
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

func (rm *RunManager) subscribe(sessionID string) (uint64, <-chan SSEEvent, bool) {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return 0, nil, false
	}
	return state.subscribe()
}

func (rm *RunManager) unsubscribe(sessionID string, subscriberID uint64) {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.unsubscribe(subscriberID)
}

func (rm *RunManager) notifySessionClosed(sessionID, message string) {
	state := rm.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.closeStreams(&SSEEvent{Type: "closed", Message: message})
}

func (rm *RunManager) notifyAllStreamsClosed(message string) {
	rm.mu.Lock()
	states := make([]*runState, 0, len(rm.active))
	for _, state := range rm.active {
		states = append(states, state)
	}
	rm.mu.Unlock()

	for _, state := range states {
		state.closeStreams(&SSEEvent{Type: "closed", Message: message})
	}
}

func sendSSEEvent(ch chan SSEEvent, evt SSEEvent) {
	defer func() {
		_ = recover()
	}()
	select {
	case ch <- evt:
	default:
	}
}

func (rs *runState) subscribe() (uint64, <-chan SSEEvent, bool) {
	rs.streamMu.Lock()
	defer rs.streamMu.Unlock()

	history := append([]SSEEvent(nil), rs.history...)
	ch := make(chan SSEEvent, 512)
	for _, evt := range history {
		ch <- evt
	}
	if rs.streamsClosed {
		close(ch)
		return 0, ch, len(history) > 0
	}

	id := rs.nextSubscriber
	rs.nextSubscriber++
	rs.subscribers[id] = ch
	return id, ch, true
}

func (rs *runState) unsubscribe(id uint64) {
	rs.streamMu.Lock()
	defer rs.streamMu.Unlock()

	ch, ok := rs.subscribers[id]
	if !ok {
		return
	}
	delete(rs.subscribers, id)
	close(ch)
}

func (rs *runState) broadcast(evt SSEEvent) {
	rs.streamMu.Lock()
	if rs.streamsClosed {
		rs.streamMu.Unlock()
		return
	}
	rs.history = append(rs.history, evt)
	subscribers := make([]chan SSEEvent, 0, len(rs.subscribers))
	for _, ch := range rs.subscribers {
		subscribers = append(subscribers, ch)
	}
	rs.streamMu.Unlock()

	for _, ch := range subscribers {
		sendSSEEvent(ch, evt)
	}
}

func (rs *runState) closeStreams(evt *SSEEvent) {
	rs.streamMu.Lock()
	if rs.streamsClosed {
		rs.streamMu.Unlock()
		return
	}
	if evt != nil {
		rs.history = append(rs.history, *evt)
	}
	rs.streamsClosed = true
	subscribers := make([]chan SSEEvent, 0, len(rs.subscribers))
	for id, ch := range rs.subscribers {
		subscribers = append(subscribers, ch)
		delete(rs.subscribers, id)
	}
	rs.streamMu.Unlock()

	for _, ch := range subscribers {
		if evt != nil {
			sendSSEEvent(ch, *evt)
		}
		close(ch)
	}
}

// SSEWriter writes SSE-formatted events to run subscribers.
type SSEWriter struct {
	state *runState
}

// NewSSEWriter creates an SSE writer.
func NewSSEWriter(state *runState) *SSEWriter {
	return &SSEWriter{state: state}
}

// Token sends a text token event.
func (w *SSEWriter) Token(content string) {
	w.state.broadcast(SSEEvent{Type: "token", Content: content})
}

// ToolCall sends a tool call event.
func (w *SSEWriter) ToolCall(name string, args interface{}) {
	w.state.broadcast(SSEEvent{Type: "tool_call", Tool: name, Args: args})
}

// ToolResult sends a tool result event.
func (w *SSEWriter) ToolResult(name string, output interface{}) {
	outputStr := ""
	if s, ok := output.(string); ok {
		outputStr = s
	} else {
		if b, err := json.Marshal(output); err == nil {
			outputStr = string(b)
		}
	}
	w.state.broadcast(SSEEvent{Type: "tool_result", Tool: name, Output: outputStr})
}

// Done sends a done event with optional token usage.
func (w *SSEWriter) Done(messageID string, usage *tokenUsage) {
	w.state.closeStreams(&SSEEvent{Type: "done", MessageID: messageID, Usage: usage})
}

// Component sends a generative UI component event.
func (w *SSEWriter) Component(data interface{}) {
	w.state.broadcast(SSEEvent{Type: "component", Data: data})
}

// Error sends an error event.
func (w *SSEWriter) Error(msg string) {
	w.state.closeStreams(&SSEEvent{Type: "error", Message: msg})
}

// AppendEvent processes ADK session events and sends SSE events to the writer.
// Returns the accumulated assistant response text for storage in the UI session.
func (rm *RunManager) AppendEvent(state *runState, w *SSEWriter) string {
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
				content := state.bufferString()
				if content != "" {
					if rm.uiSessionMgr != nil {
						rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
							Role:      "assistant",
							Content:   content,
							CreatedAt: time.Now(),
						})
					}
					w.Done(messageID, estimateUsage(content))
				}
				state.finish()
				return content
			}
			if evt == nil {
				continue
			}

			turnComplete := !evt.Partial && !contentHasFunctionCalls(evt.Content) && !contentHasFunctionResponses(evt.Content)
			acceptFinalText := turnComplete && state.bufferString() == ""

			if evt.Content != nil {
				for _, part := range evt.Content.Parts {
					if part == nil {
						continue
					}
					if part.Text != "" && (!turnComplete || acceptFinalText) {
						state.appendBuffer(part.Text)
						w.Token(part.Text)
					}
					if fc := part.FunctionCall; fc != nil {
						if fc.Name == "render_component" {
							// Emit component SSE event with input args
							argsMap := fc.Args
							compName, _ := argsMap["name"].(string)
							compData, _ := argsMap["data"].(map[string]interface{})
							w.state.broadcast(SSEEvent{
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
							// Tool result already emitted as component event above
							// Still emit a brief token to show in transcript
							state.appendBuffer("[Component rendered]")
						} else {
							w.ToolResult(fr.Name, fr.Response)
						}
					}
				}
			}

			if turnComplete {
				content := state.bufferString()
				if rm.uiSessionMgr != nil {
					rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
						Role:      "assistant",
						Content:   content,
						CreatedAt: time.Now(),
					})
				}
				w.Done(messageID, estimateUsage(content))
				state.finish()
				return content
			}

		case err, ok := <-state.Errors:
			if !ok {
				state.finish()
				return state.bufferString()
			}
			if err != nil {
				return rm.finishWithError(state, w, messageID, err)
			}

		case <-state.Done:
			state.closeStreams(nil)
			return state.bufferString()
		}
	}
}

func (rm *RunManager) finishWithError(state *runState, w *SSEWriter, messageID string, err error) string {
	var maxTurnsErr *agentrunner.MaxTurnsExceededError
	if errors.As(err, &maxTurnsErr) {
		content := state.bufferString()
		limitMsg := maxTurnsMessage(maxTurnsErr.Limit)
		if content == "" {
			state.appendBuffer(limitMsg)
			content = limitMsg
		} else {
			state.appendBuffer("\n\n" + limitMsg)
			content += "\n\n" + limitMsg
		}
		if rm.uiSessionMgr != nil {
			rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
				Role:      "assistant",
				Content:   content,
				CreatedAt: time.Now(),
			})
		}
		w.Done(messageID, estimateUsage(content))
		state.finish()
		return content
	}
	w.Error(formatErrorMessage(err))
	state.finish()
	return state.bufferString()
}

// estimateUsage estimates token counts from text length.
// Uses a rough ratio: ~4 chars per token for English text.
// This is a fallback when the provider doesn't return usage data.
func maxTurnsMessage(limit int) string {
	if limit == 1 {
		return "Stopped after reaching max turns limit (1). Increase Max Turns in Settings if this task needs tool follow-up steps."
	}
	return fmt.Sprintf("Stopped after reaching max turns limit (%d). Increase Max Turns in Settings if this task needs more tool/model steps.", limit)
}

func estimateUsage(text string) *tokenUsage {
	const charsPerToken = 4
	totalTokens := len(text) / charsPerToken
	if totalTokens < 1 {
		totalTokens = 1
	}
	// Rough split: ~2/3 prompt tokens, ~1/3 completion tokens
	completionTokens := totalTokens / 3
	if completionTokens < 1 {
		completionTokens = 1
	}
	promptTokens := totalTokens - completionTokens
	return &tokenUsage{
		TotalTokens:      totalTokens,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}
}

// formatErrorMessage converts ADK/provider errors to user-friendly messages.
func formatErrorMessage(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "Connection refused: LLM provider is not reachable. Check that your provider is running."
	case strings.Contains(msg, "401") || strings.Contains(msg, "Authentication"):
		return "Authentication failed. Check your API key in Settings."
	case strings.Contains(msg, "429") || strings.Contains(msg, "Rate limit"):
		return "Rate limited by provider. Please wait a moment and try again."
	case strings.Contains(msg, "context length") || strings.Contains(msg, "maximum context"):
		return "Context length exceeded. Try a shorter message or reduce conversation history."
	case strings.Contains(msg, "model no longer available") || strings.Contains(msg, "model not found"):
		return "Selected model no longer available. Choose another model in Settings."
	case strings.Contains(msg, "streaming tool calls") || strings.Contains(msg, "streaming not supported"):
		return "Provider does not support required streaming tool calls. Use OpenCode Go or another compatible provider."
	case strings.Contains(msg, "timeout"):
		return "Request timed out. The provider took too long to respond."
	case strings.Contains(msg, "port already in use") || strings.Contains(msg, "address already in use"):
		return "Cannot bind port: address already in use. Try EITRI_ADDR=127.0.0.1:8081 eitri."
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "lookup"):
		return "Cannot reach provider at the configured URL. Check base_url in Settings."
	default:
		return fmt.Sprintf("LLM error: %s", msg)
	}
}
