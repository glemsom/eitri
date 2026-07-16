// Package agent provides the agent loop, LLM service interface, and
// conversation session management.
package agent

import (
	"sync"

	"github.com/voocel/litellm"
)

const (
	// DefaultMaxExchanges is the default sliding window cap in exchanges.
	// An exchange begins with a user message and includes all following
	// assistant and tool messages until the next user message.
	DefaultMaxExchanges = 50

	// DefaultSystemPrompt is the fallback system prompt when none is configured.
	DefaultSystemPrompt = "You are Eitri, an AI coding assistant."
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
		Role:   litellm.RoleUser,
		Blocks: []litellm.Block{litellm.TextBlock{Text: text}},
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// AppendAssistant appends an assistant message with text blocks and optional
// tool call blocks. No-op if session does not exist.
func (m *SessionManager) AppendAssistant(id string, blocks []litellm.Block, toolCalls []litellm.ToolUseBlock) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	msgBlocks := make([]litellm.Block, 0, len(blocks)+len(toolCalls))
	msgBlocks = append(msgBlocks, blocks...)
	for _, tc := range toolCalls {
		msgBlocks = append(msgBlocks, tc)
	}
	if len(msgBlocks) == 0 {
		return
	}
	s.messages = append(s.messages, litellm.Message{
		Role:   litellm.RoleAssistant,
		Blocks: msgBlocks,
	})
	s.messages = m.trimExchangesLocked(s.messages)
}

// AppendTool appends a tool result message. No-op if session does not exist.
func (m *SessionManager) AppendTool(id, toolUseID string, blocks []litellm.Block, isError bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	contentBlocks := make([]litellm.Block, len(blocks))
	copy(contentBlocks, blocks)
	s.messages = append(s.messages, litellm.Message{
		Role: litellm.RoleTool,
		Blocks: []litellm.Block{litellm.ToolResultBlock{
			ToolUseID: toolUseID,
			Content:   contentBlocks,
			IsError:   isError,
		}},
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
		Role:   litellm.RoleSystem,
		Blocks: []litellm.Block{litellm.TextBlock{Text: sysPrompt}},
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
		if msg.Role == litellm.RoleUser {
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
		if msg.Role == litellm.RoleUser {
			count++
			if count == toRemove {
				// Remove everything from the start of this user message
				removeIdx = i
				break
			}
		}
	}

	// Also check if there's a preceding user message with same role we need to
	// find the boundary better. Actually, we want to remove messages[0..removeIdx].
	// But we want to keep the system prompt (not in messages slice) and the remaining
	// messages.
	return messages[removeIdx+1:]
}
