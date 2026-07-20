// Package history provides conversation session management with a sliding
// window cap for per-chat LLM conversation history.
package history

import (
	"sync"

	"github.com/glemsom/eitri/internal/litellm"
)

const (
	// DefaultMaxExchanges is the default sliding window cap in exchanges.
	// An exchange begins with a user message and includes all following
	// assistant and tool messages until the next user message.
	DefaultMaxExchanges = 50

	// DefaultSystemPrompt is the fallback system prompt when none is configured.
	DefaultSystemPrompt = `You are Eitri, an expert AI coding agent. You can help the user by reading/writing/editing files, executing commands - and giving recommendations to the user.

## Core behavior
- Be concise. Prefer the simplest correct solution. Avoid overengineering.
- Prefer small, focused edits over large rewrites. Preserve existing style.
- Remove imports or code left unused by your changes.
- Before reading a file, first use grep to locate relevant code by regex. Then use read with start_line and end_line (populated from grep's output line numbers) to read only the needed section. Avoid reading entire files unless grep confirms the full content is relevant.`
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
	messages     []litellm.Message
	systemPrompt string
}

// NewSessionManager creates a session manager with the given exchange cap.
// If maxExchanges <= 0, DefaultMaxExchanges (50) is used.
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
		messages:     make([]litellm.Message, 0, 16),
		systemPrompt: DefaultSystemPrompt,
	}
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
	s.messages = append(s.messages, litellm.Message{
		Role:    "user",
		Content: text,
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// AppendAssistant appends an assistant message with text content and optional
// tool calls. No-op if session does not exist.
func (m *SessionManager) AppendAssistant(id, content string, toolCalls []litellm.ToolCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	s.messages = append(s.messages, litellm.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// AppendTool appends a tool result message. No-op if session does not exist.
func (m *SessionManager) AppendTool(id, toolUseID, content string, isError bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	s.messages = append(s.messages, litellm.Message{
		Role:       "tool",
		ToolCallID: toolUseID,
		Content:    content,
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// History returns a copy of the conversation history with the system prompt
// prepended. Returns nil if session does not exist.
func (m *SessionManager) History(id string) []litellm.Message {
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
	sysMsg := litellm.Message{
		Role:    "system",
		Content: sysPrompt,
	}

	// Deep copy messages
	messages := make([]litellm.Message, 0, 1+len(s.messages))
	messages = append(messages, sysMsg)
	messages = append(messages, s.messages...)

	return messages
}

// Close removes a session. No-op if session does not exist.
func (m *SessionManager) Close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// trimExchangesLocked removes oldest exchanges when the user message count
// exceeds maxExchanges. Must be called with m.mu held.
func (m *SessionManager) trimExchangesLocked(messages []litellm.Message) []litellm.Message {
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
