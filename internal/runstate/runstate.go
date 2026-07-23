// Package runstate provides SSE event broadcast infrastructure for active agent runs.
// It owns subscriber fan-out, event history, and typed SSE event writing.
// It is network-agnostic — it manages channels, not HTTP connections.
package runstate

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/llm"
)

// RenderKind maps SSE events to the render kind the browser island should POST.
type RenderKind string

const (
	RenderKindToolCard  RenderKind = "tool_card"
	RenderKindComponent RenderKind = "component"
	RenderKindError     RenderKind = "error"
	RenderKindMarkdown  RenderKind = "markdown"
)

// SSEEvent represents one SSE data packet sent to the browser.
type SSEEvent struct {
	Type      string      `json:"type"`
	Kind      RenderKind  `json:"kind,omitempty"`
	Content   string      `json:"content,omitempty"`
	Name      string      `json:"name,omitempty"`
	Tool      string      `json:"tool,omitempty"`
	Args      interface{} `json:"args,omitempty"`
	Output    interface{} `json:"output,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Message   string      `json:"message,omitempty"`
	MessageID string      `json:"message_id,omitempty"`
	Usage     *TokenUsage `json:"usage,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// TokenUsage holds token count information for a completed run.
type TokenUsage struct {
	TotalTokens      int `json:"total_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// State tracks one active assistant run per session.
// Owns subscriber fan-out, event history, and text buffer.
type State struct {
	mu sync.Mutex

	subscribers      map[uint64]chan SSEEvent
	nextSubscriber   uint64
	streamsClosed    bool
	history          []SSEEvent
	subscriberCount  uint64
	replayCount      uint64

	bufferMu sync.Mutex
	buffer   strings.Builder

	reasoningMu sync.Mutex
	reasoning   strings.Builder
}

// New creates a new State ready for use.
func New() *State {
	return &State{
		subscribers: make(map[uint64]chan SSEEvent),
	}
}

// BufferString returns accumulated text.
func (s *State) BufferString() string {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	return s.buffer.String()
}

// AppendBuffer appends text to accumulator.
func (s *State) AppendBuffer(text string) {
	s.bufferMu.Lock()
	defer s.bufferMu.Unlock()
	s.buffer.WriteString(text)
}

// ReasoningBufferString returns accumulated reasoning text.
func (s *State) ReasoningBufferString() string {
	s.reasoningMu.Lock()
	defer s.reasoningMu.Unlock()
	return s.reasoning.String()
}

// AppendReasoningBuffer appends reasoning text to accumulator.
func (s *State) AppendReasoningBuffer(text string) {
	s.reasoningMu.Lock()
	defer s.reasoningMu.Unlock()
	s.reasoning.WriteString(text)
}

// Subscribe allocates a subscriber channel and replays history.
// Returns subscriberID, receive-only channel, and whether the stream is still open.
// If streams are already closed, the channel carries history (if any) and is closed.
func (s *State) Subscribe() (uint64, <-chan SSEEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	history := append([]SSEEvent(nil), s.history...)
	ch := make(chan SSEEvent, 512)
	for _, evt := range history {
		ch <- evt
	}
	if s.streamsClosed {
		close(ch)
		return 0, ch, len(history) > 0
	}

	id := s.nextSubscriber
	s.nextSubscriber++
	s.subscribers[id] = ch
	s.subscriberCount++
	if len(history) > 0 {
		s.replayCount++
	}
	return id, ch, true
}

// Unsubscribe removes a subscriber and closes its channel.
func (s *State) Unsubscribe(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch, ok := s.subscribers[id]
	if !ok {
		return
	}
	delete(s.subscribers, id)
	close(ch)
}

// Broadcast sends an event to all current subscribers and appends it to history.
// No-op after CloseStreams is called.
func (s *State) Broadcast(evt SSEEvent) {
	subscribers := s.broadcastPrepare()
	if subscribers == nil {
		return
	}
	evt.Timestamp = time.Now()
	s.history = append(s.history, evt)
	s.mu.Unlock()

	for _, ch := range subscribers {
		sendSSEEvent(ch, evt)
	}
}

// BroadcastDone sends a done event with kind "markdown", closes all subscriber streams, and marks the state as closed.
func (s *State) BroadcastDone(messageID string, usage *TokenUsage) {
	s.closeStreams(&SSEEvent{Type: "done", Kind: RenderKindMarkdown, MessageID: messageID, Usage: usage})
}

// BroadcastError sends an error event with kind "error" and closes all subscriber streams.
func (s *State) BroadcastError(msg string) {
	s.closeStreams(&SSEEvent{Type: "error", Kind: RenderKindError, Message: msg})
}

// BroadcastClosed sends a closed event and closes all subscriber streams.
func (s *State) BroadcastClosed(message string) {
	s.closeStreams(&SSEEvent{Type: "closed", Message: message})
}

// Closed returns true if the state streams have been closed.
func (s *State) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamsClosed
}

// History returns a copy of all broadcast events.
func (s *State) History() []SSEEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SSEEvent(nil), s.history...)
}

// SubscriberCount returns the number of distinct subscribers that have connected.
func (s *State) SubscriberCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subscriberCount
}

// ReplayCount returns the number of times history has been replayed to a subscriber.
func (s *State) ReplayCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replayCount
}

// broadcastPrepare locks the mutex and returns the subscriber list.
// Returns nil if streams are closed. Caller must Unlock after use.
func (s *State) broadcastPrepare() []chan SSEEvent {
	s.mu.Lock()
	if s.streamsClosed {
		s.mu.Unlock()
		return nil
	}
	subscribers := make([]chan SSEEvent, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		subscribers = append(subscribers, ch)
	}
	return subscribers
}

func (s *State) closeStreams(evt *SSEEvent) {
	s.mu.Lock()
	if s.streamsClosed {
		s.mu.Unlock()
		return
	}
	if evt != nil {
		evt.Timestamp = time.Now()
		s.history = append(s.history, *evt)
	}
	s.streamsClosed = true
	subscribers := make([]chan SSEEvent, 0, len(s.subscribers))
	for id, ch := range s.subscribers {
		subscribers = append(subscribers, ch)
		delete(s.subscribers, id)
	}
	s.mu.Unlock()

	for _, ch := range subscribers {
		if evt != nil {
			sendSSEEvent(ch, *evt)
		}
		close(ch)
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

// Writer provides typed helpers that broadcast SSE events to a State.
type Writer struct {
	state *State
}

// NewWriter creates an SSE writer that broadcasts to state.
func NewWriter(state *State) *Writer {
	return &Writer{state: state}
}

// State returns the underlying state (for direct broadcast access).
func (w *Writer) State() *State {
	return w.state
}

// Token sends a text token event and appends to the text buffer.
func (w *Writer) Token(content string) {
	w.state.AppendBuffer(content)
	w.state.Broadcast(SSEEvent{Type: "token", Content: content})
}

// ToolCall sends a tool call event with kind "tool_card".
func (w *Writer) ToolCall(name string, args interface{}) {
	w.state.Broadcast(SSEEvent{Type: "tool_call", Kind: RenderKindToolCard, Tool: name, Args: args})
}

// ToolResult sends a tool result event with kind "tool_card".
func (w *Writer) ToolResult(name string, output interface{}) {
	outputStr := ""
	if s, ok := output.(string); ok {
		outputStr = s
	} else {
		if b, err := json.Marshal(output); err == nil {
			outputStr = string(b)
		}
	}
	w.state.Broadcast(SSEEvent{Type: "tool_result", Kind: RenderKindToolCard, Tool: name, Output: outputStr})
}

// Done sends a done event with optional token usage and closes streams.
func (w *Writer) Done(messageID string, usage *TokenUsage) {
	w.state.BroadcastDone(messageID, usage)
}

// Component sends a generative UI component event with kind "component".
func (w *Writer) Component(data interface{}) {
	w.state.Broadcast(SSEEvent{Type: "component", Kind: RenderKindComponent, Data: data})
}

// ContextUpdate broadcasts a context_update SSE event with token estimates.
func (w *Writer) ContextUpdate(update *ContextUpdate) {
	w.state.Broadcast(SSEEvent{Type: "context_update", Data: update})
}

// SkillActivated broadcasts a skill_activated event with the skill name.
func (w *Writer) SkillActivated(name string) {
	w.state.Broadcast(SSEEvent{Type: "skill_activated", Tool: name})
}

// Error sends an error event with kind "error" and closes streams.
func (w *Writer) Error(msg string) {
	w.state.BroadcastError(msg)
}

// ThinkingDelta broadcasts a thinking_delta SSE event with reasoning content
// and appends to the reasoning buffer for persistence across session switches.
func (w *Writer) ThinkingDelta(content string) {
	w.state.AppendReasoningBuffer(content)
	w.state.Broadcast(SSEEvent{Type: "thinking_delta", Content: content})
}

// ContextUpdate holds estimated token counts broken down by category.
type ContextUpdate struct {
	TotalTokens      int `json:"total_tokens"`
	ContextWindow    int `json:"context_window"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	SystemTokens     int `json:"system_tokens"`
	HistoryTokens    int `json:"history_tokens"`
	SkillTokens      int `json:"skill_tokens"`
}

// ComputeContext estimates token counts for the given messages using a 4-char-per-token heuristic.
//
// Token breakdown:
//   - System tokens: sum of content lengths for messages with role "system"
//   - Skill tokens: portion of system prompt after the last "Activated skill" substring
//   - History tokens: sum of all non-system messages (user + assistant + tool)
//   - Prompt tokens: system + history (skill tokens are part of system)
//   - Completion tokens: 0 (set by caller when known)
//   - Total tokens: prompt + completion
func ComputeContext(messages []llm.Message, contextWindow int) *ContextUpdate {
	const charsPerToken = 4

	var historyLen int
	var systemBuilder strings.Builder

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemBuilder.WriteString(msg.Content)
		default:
			historyLen += len(msg.Content)
		}
	}

	systemContent := systemBuilder.String()
	systemTokens := len(systemContent) / charsPerToken
	if systemTokens < 1 && len(systemContent) > 0 {
		systemTokens = 1
	}

	// Skill tokens: content after last "Activated skill" in system prompt
	var skillTokens int
	if idx := strings.LastIndex(systemContent, "Activated skill"); idx >= 0 {
		skillContent := systemContent[idx+len("Activated skill"):]
		skillTokens = len(skillContent) / charsPerToken
	}

	historyTokens := historyLen / charsPerToken
	if historyTokens < 1 && historyLen > 0 {
		historyTokens = 1
	}

	promptTokens := systemTokens + historyTokens
	completionTokens := 0
	totalTokens := promptTokens + completionTokens

	return &ContextUpdate{
		TotalTokens:      totalTokens,
		ContextWindow:    contextWindow,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		SystemTokens:     systemTokens,
		HistoryTokens:    historyTokens,
		SkillTokens:      skillTokens,
	}
}

// EstimateUsage estimates token counts from text length.
// Uses a rough ratio: ~4 chars per token for English text.
// This is a fallback when the provider doesn't return usage data.
func EstimateUsage(text string) *TokenUsage {
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
	return &TokenUsage{
		TotalTokens:      totalTokens,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
	}
}

// FormatErrorMessage converts ADK/provider errors to user-friendly messages.
func FormatErrorMessage(err error) string {
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

// MaxTurnsMessage returns a user-facing message for max-turn limits.
func MaxTurnsMessage(limit int) string {
	if limit == 1 {
		return "Stopped after reaching max turns limit (1). Increase Max Turns in Settings if this task needs tool follow-up steps."
	}
	return fmt.Sprintf("Stopped after reaching max turns limit (%d). Increase Max Turns in Settings if this task needs more tool/model steps.", limit)
}
