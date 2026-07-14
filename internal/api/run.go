package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/session"

	adkagent "google.golang.org/adk/v2/agent"

	"github.com/glemsom/eitri/internal/agent"
	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/executor"
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
	TotalTokens    int `json:"total_tokens"`
	PromptTokens   int `json:"prompt_tokens"`
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
}

// RunManager manages active runs per session.
type RunManager struct {
	mu            sync.Mutex
	active        map[string]*runState
	runnerMgr     *agentrunner.Manager
	sessionMgr    *executor.SessionManager
	uiSessionMgr  *uisession.Manager
	skillsSvc     *skills.Service
	providerID    string
	baseURL       string
	apiKey        string
	modelName     string
}

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

// UpdateProviderConfig stores provider config for creating model instances.
func (rm *RunManager) UpdateProviderConfig(cfg *config.Config) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.providerID = cfg.Provider
	rm.baseURL = cfg.BaseURL
	rm.apiKey = cfg.APIKey
	rm.modelName = cfg.Model
}

// StartRun starts a new agent run for a session. Returns error if run already active.
func (rm *RunManager) StartRun(ctx context.Context, sessionID, userMessage string) error {
	rm.mu.Lock()
	if _, exists := rm.active[sessionID]; exists {
		rm.mu.Unlock()
		return fmt.Errorf("session %s already has an active run", sessionID)
	}
	providerID := rm.providerID
	baseURL := rm.baseURL
	apiKey := rm.apiKey
	modelName := rm.modelName
	rm.mu.Unlock()

	if baseURL == "" || modelName == "" {
		return fmt.Errorf("provider not configured: set base_url and model in settings")
	}

	if providerID == "" {
		providerID = "opencode_go"
	}
	llm, err := agent.NewOpenAIModelForProvider(modelName, baseURL, apiKey, providerID)
	if err != nil {
		return fmt.Errorf("failed to create model: %w", err)
	}
	var ag adkagent.Agent
	var agErr error
	if rm.skillsSvc != nil {
		ag, agErr = agent.NewAgentWithSkills(llm, rm.sessionMgr, rm.skillsSvc, rm.uiSessionMgr)
	} else {
		ag, agErr = agent.NewAgent(llm, rm.sessionMgr)
	}
	if agErr != nil {
		return fmt.Errorf("failed to create agent: %w", agErr)
	}

	cfg := &config.Config{Provider: providerID, APIKey: apiKey, BaseURL: baseURL, Model: modelName}
	r, err := rm.runnerMgr.GetOrCreate(cfg, ag)
	if err != nil {
		return fmt.Errorf("failed to get runner: %w", err)
	}

	content := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: userMessage}},
	}

	eventCh, errCh, cancel := rm.runnerMgr.Run(ctx, r, sessionID, sessionID, content)

	state := &runState{
		SessionID: sessionID,
		Events:    eventCh,
		Errors:    errCh,
		Cancel:    cancel,
		StartedAt: time.Now(),
		Done:      make(chan struct{}),
	}

	rm.mu.Lock()
	rm.active[sessionID] = state
	rm.mu.Unlock()

	go func() {
		<-state.Done
		log.Printf("Run done for session %s", sessionID)
		rm.mu.Lock()
		if rm.active[sessionID] == state {
			delete(rm.active, sessionID)
		}
		rm.mu.Unlock()
	}()

	return nil
}

// ActiveRun returns the active run state for a session.
func (rm *RunManager) ActiveRun(sessionID string) *runState {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.active[sessionID]
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
	state.Cancel()
	close(state.Done)
	return true
}

// SSEWriter writes SSE-formatted events to a channel.
type SSEWriter struct {
	ch chan SSEEvent
}

// NewSSEWriter creates an SSE writer.
func NewSSEWriter(ch chan SSEEvent) *SSEWriter {
	return &SSEWriter{ch: ch}
}

// Token sends a text token event.
func (w *SSEWriter) Token(content string) {
	w.ch <- SSEEvent{Type: "token", Content: content}
}

// ToolCall sends a tool call event.
func (w *SSEWriter) ToolCall(name string, args interface{}) {
	w.ch <- SSEEvent{Type: "tool_call", Tool: name, Args: args}
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
	w.ch <- SSEEvent{Type: "tool_result", Tool: name, Output: outputStr}
}

// Done sends a done event with optional token usage.
func (w *SSEWriter) Done(messageID string, usage *tokenUsage) {
	w.ch <- SSEEvent{Type: "done", MessageID: messageID, Usage: usage}
}

// Component sends a generative UI component event.
func (w *SSEWriter) Component(data interface{}) {
	w.ch <- SSEEvent{Type: "component", Data: data}
}

// Error sends an error event.
func (w *SSEWriter) Error(msg string) {
	w.ch <- SSEEvent{Type: "error", Message: msg}
}

// AppendEvent processes ADK session events and sends SSE events to the writer.
// Returns the accumulated assistant response text for storage in the UI session.
func (rm *RunManager) AppendEvent(state *runState, w *SSEWriter) string {
	messageID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	var fullText strings.Builder

	for {
		select {
		case evt, ok := <-state.Events:
			if !ok {
				if fullText.Len() > 0 {
					if rm.uiSessionMgr != nil {
						rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
							Role:      "assistant",
							Content:   fullText.String(),
							CreatedAt: time.Now(),
						})
					}
					w.Done(messageID, estimateUsage(fullText.String()))
				}
				close(state.Done)
				return fullText.String()
			}
			if evt == nil {
				continue
			}

			turnComplete := evt.TurnComplete || evt.IsFinalResponse()

			if evt.Content != nil && !turnComplete {
				for _, part := range evt.Content.Parts {
					if part == nil {
						continue
					}
					if part.Text != "" {
						fullText.WriteString(part.Text)
						w.Token(part.Text)
					}
					if fc := part.FunctionCall; fc != nil {
						if fc.Name == "render_component" {
							// Emit component SSE event with input args
							argsMap := fc.Args
							compName, _ := argsMap["name"].(string)
							compData, _ := argsMap["data"].(map[string]interface{})
							w.ch <- SSEEvent{
								Type: "component",
								Name: compName,
								Data: compData,
							}
						} else {
							w.ToolCall(fc.Name, fc.Args)
						}
					}
					if fr := part.FunctionResponse; fr != nil {
						if fr.Name == "render_component" {
							// Tool result already emitted as component event above
							// Still emit a brief token to show in transcript
							fullText.WriteString("[Component rendered]")
						} else {
							w.ToolResult(fr.Name, fr.Response)
						}
					}
				}
			}

			if turnComplete {
				if rm.uiSessionMgr != nil {
					rm.uiSessionMgr.AppendMessage(state.SessionID, uisession.Message{
						Role:      "assistant",
						Content:   fullText.String(),
						CreatedAt: time.Now(),
					})
				}
				w.Done(messageID, estimateUsage(fullText.String()))
				close(state.Done)
				return fullText.String()
			}

		case err, ok := <-state.Errors:
			if !ok {
				close(state.Done)
				return fullText.String()
			}
			if err != nil {
				w.Error(formatErrorMessage(err))
				close(state.Done)
				return fullText.String()
			}

		case <-state.Done:
			return fullText.String()
		}
	}
}

// estimateUsage estimates token counts from text length.
// Uses a rough ratio: ~4 chars per token for English text.
// This is a fallback when the provider doesn't return usage data.
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
