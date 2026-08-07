// Package history provides conversation session management with a sliding
// window cap for per-chat LLM conversation history.
package history

import (
	"sync"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/persona"
)

const (
	// DefaultMaxExchanges is the default sliding window cap in exchanges.
	// An exchange begins with a user message and includes all following
	// assistant and tool messages until the next user message. It aliases
	// message.DefaultMaxExchanges — the single canonical source — so both the
	// history store and the canonical session store always resolve to the
	// same cap (issue #1239).
	DefaultMaxExchanges = message.DefaultMaxExchanges

	// DefaultSystemPrompt is the fallback system prompt when no persona is active
	// and no user override is set. It aliases persona.DefaultPrompt — the single
	// canonical source — so both the history fallback and the generic persona
	// always resolve to the same text.
	DefaultSystemPrompt = persona.DefaultPrompt
)

// SessionManager manages per-chat LLM conversation history with a sliding
// window cap. The system prompt is stored separately and prepended on reads.
// Thread-safe. Lost on server restart.
type SessionManager struct {
	mu           sync.RWMutex
	sessions     map[string]*llmSession
	maxExchanges int
}

type llmSession struct {
	messages     []message.EitriMessage
	systemPrompt string
}

// NewSessionManager creates a session manager with the given exchange cap.
// If maxExchanges <= 0, DefaultMaxExchanges (150) is used.
func NewSessionManager(maxExchanges int) *SessionManager {
	if maxExchanges <= 0 {
		maxExchanges = DefaultMaxExchanges
	}
	return &SessionManager{
		sessions:     make(map[string]*llmSession),
		maxExchanges: maxExchanges,
	}
}

// Create creates a new session with the given ID.
// If the session already exists, it is a no-op.
func (m *SessionManager) Create(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[id]; exists {
		return
	}
	m.sessions[id] = &llmSession{
		messages:     make([]message.EitriMessage, 0, 16),
		systemPrompt: DefaultSystemPrompt,
	}
}

// RestoreHistory replaces the full history for a session with the given messages.
// The first message with Role "system" is treated as the system prompt and stored
// separately; all remaining messages form the conversation history.
// If the session doesn't exist, it is created first.
func (m *SessionManager) RestoreHistory(id string, messages []message.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, exists := m.sessions[id]
	if !exists {
		s = &llmSession{
			messages:     make([]message.EitriMessage, 0, len(messages)),
			systemPrompt: DefaultSystemPrompt,
		}
		m.sessions[id] = s
	}

	// Extract system prompt if present
	if len(messages) > 0 && messages[0].Role == "system" {
		s.systemPrompt = messages[0].Content
		s.messages = toEitriMessages(messages[1:])
	} else {
		s.messages = toEitriMessages(messages)
	}
}

// toEitriMessages converts a slice of message.Message to a slice of EitriMessage,
// preserving CreatedAt, Components, and QuickReplies.
func toEitriMessages(msgs []message.Message) []message.EitriMessage {
	out := make([]message.EitriMessage, len(msgs))
	for i, m := range msgs {
		litellmMsg := message.ToLitellmMessage(m)
		out[i] = message.EitriMessage{
			Message:      litellmMsg,
			CreatedAt:    m.CreatedAt,
			Components:   m.Components,
			QuickReplies: m.QuickReplies,
		}
	}
	return out
}

// toMessages converts a slice of EitriMessage to a slice of message.Message,
// preserving CreatedAt, Components, and QuickReplies.
func toMessages(msgs []message.EitriMessage) []message.Message {
	out := make([]message.Message, len(msgs))
	for i, em := range msgs {
		m := message.FromLitellmMessage(em.Message)
		m.CreatedAt = em.CreatedAt
		m.Components = em.Components
		m.QuickReplies = em.QuickReplies
		out[i] = m
	}
	return out
}

// SetSystemPrompt updates the system prompt for a session.
// No-op if session does not exist.
func (m *SessionManager) SetSystemPrompt(id, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	s.systemPrompt = prompt
}

// Get returns true if the session exists.
func (m *SessionManager) Get(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.sessions[id]
	return exists
}

// AppendUser appends a user message with the given text.
// No-op if session does not exist.
func (m *SessionManager) AppendUser(id, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	msg := message.Message{Role: "user", Content: text}
	s.messages = append(s.messages, message.EitriMessage{
		Message:   message.ToLitellmMessage(msg),
		CreatedAt: time.Now(),
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// AppendAssistant appends an assistant message with text content and optional
// tool calls. No-op if session does not exist, or if both content and
func (m *SessionManager) AppendAssistant(id, content string, toolCalls []message.ToolCall) {
	// Skip empty assistant messages — they serialise as
	// {"role":"assistant"} with no content or tool_calls, which some
	// providers reject.
	if content == "" && len(toolCalls) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	msg := message.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}
	s.messages = append(s.messages, message.EitriMessage{
		Message:   message.ToLitellmMessage(msg),
		CreatedAt: time.Now(),
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// AppendTool appends a tool result message. No-op if session does not exist.
// rawContent is the pre-compression output for debugging snapshots.
func (m *SessionManager) AppendTool(id, toolUseID, content, rawContent string, isError bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	msg := message.Message{
		Role:       "tool",
		ToolCallID: toolUseID,
		Content:    content,
		RawContent: rawContent,
	}
	s.messages = append(s.messages, message.EitriMessage{
		Message:   message.ToLitellmMessage(msg),
		CreatedAt: time.Now(),
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// History returns a copy of the conversation history with the system prompt
// prepended as an EitriMessage. Returns nil if session does not exist.
func (m *SessionManager) History(id string) []message.EitriMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[id]
	if s == nil {
		return nil
	}

	// Build system prompt message
	sysPrompt := s.systemPrompt
	if sysPrompt == "" {
		sysPrompt = DefaultSystemPrompt
	}

	// Build system prompt as an EitriMessage
	sysMsg := message.EitriMessage{
		Message: litellm.Message{
			Role:   litellm.Role("system"),
			Blocks: []litellm.Block{litellm.TextBlock{Text: sysPrompt}},
		},
		CreatedAt: time.Now(),
	}

	// Prepend system message to stored EitriMessages
	messages := make([]message.EitriMessage, 0, 1+len(s.messages))
	messages = append(messages, sysMsg)
	messages = append(messages, s.messages...)

	return messages
}

// RepairPendingToolUse returns a copy of messages with any trailing
// unresolved assistant tool call closed by a synthetic tool error result.
//
// A run that is cancelled while a tool is executing can leave the history
// ending in an assistant message with a tool call but no matching tool result.
// Appending a user message directly after that produces an invalid
// OpenAI-style sequence ("user message follows unresolved tool use") which the
// provider hard-rejects. This repairs the dangling tool use so a resume is
// valid. History that does not end in an unresolved assistant tool call is
// returned unchanged.
func RepairPendingToolUse(messages []message.EitriMessage) []message.EitriMessage {
	if len(messages) == 0 {
		return messages
	}
	last := messages[len(messages)-1]
	if last.Role != litellm.Role("assistant") {
		return messages
	}
	toolCalls := last.ToolCalls()
	if len(toolCalls) == 0 {
		return messages
	}

	out := make([]message.EitriMessage, len(messages), len(messages)+len(toolCalls))
	copy(out, messages)

	// A single canceled-out error result is enough to close the pending tool
	// use(s); the LLM sees the agent's own unexecuted call and replies.
	result := message.Message{
		Role:       "tool",
		ToolCallID: toolCalls[0].ID,
		Content:    "Tool execution was cancelled before it produced a result.",
	}
	out = append(out, message.EitriMessage{
		Message:   message.ToLitellmMessage(result),
		CreatedAt: time.Now(),
	})
	return out
}

// RepairPendingToolUse repairs this session's conversation history if it ends
// in an unresolved assistant tool call (see the package-level
// RepairPendingToolUse). No-op if the session does not exist. Should be called
// before appending a fresh user message on resume.
func (m *SessionManager) RepairPendingToolUse(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	repaired := RepairPendingToolUse(s.messages)
	s.messages = repaired
}

// Close removes a session. No-op if session does not exist.
func (m *SessionManager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// trimExchangesLocked removes oldest exchanges when the user message count
// exceeds maxExchanges. Must be called with m.mu held.
func (m *SessionManager) trimExchangesLocked(messages []message.EitriMessage) []message.EitriMessage {
	if m.maxExchanges <= 0 {
		return messages
	}

	// Count user messages
	var userCount int
	for _, msg := range messages {
		if msg.Role == "user" {
			userCount++
		}
	}
	if userCount <= m.maxExchanges {
		return messages
	}

	// Need to remove the oldest (userCount - maxExchanges) exchanges
	toRemove := userCount - m.maxExchanges

	// Find the index of the toRemove-th user message (0-indexed)
	var removeIdx int
	count := 0
	for i, msg := range messages {
		if msg.Role == "user" {
			count++
			if count == toRemove {
				removeIdx = i
				break
			}
		}
	}

	return messages[removeIdx+1:]
}
