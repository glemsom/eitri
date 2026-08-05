// conversation.go — conversation mutations: messages, components, quick replies, reasoning content, active skills, and the Conversation accessors (copying + shared).

package session

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/glemsom/eitri/internal/message"
)

// AppendMessage appends a message to a session. No-op if session not found.
// Title is updated to the latest user message's preview.
func (m *Manager) AppendMessage(id string, msg message.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if convo := m.convoStore[id]; convo != nil {
		if msg.Role == "user" {
			if title := sessionTitlePreview(msg.Content); title != "" {
				if meta := m.metaStore[id]; meta != nil {
					meta.Title = title
				}
			}
		}
		convo.Messages = append(convo.Messages, msg)
		if meta := m.metaStore[id]; meta != nil {
			meta.UpdatedAt = time.Now()
		}
	}
}

// AppendComponent appends component data to a session.
// Creates an empty assistant message if no assistant message exists yet.
func (m *Manager) AppendComponent(id string, comp message.ComponentData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil {
		return nil
	}
	if len(convo.Messages) == 0 {
		return nil
	}
	last := &convo.Messages[len(convo.Messages)-1]
	if last.Role != "assistant" {
		// Create an assistant message so components have a target to attach to.
		// Content will be filled when the run completes.
		convo.Messages = append(convo.Messages, message.Message{
			Role:      "assistant",
			Content:   "",
			CreatedAt: time.Now(),
		})
		last = &convo.Messages[len(convo.Messages)-1]
	}
	last.Components = append(last.Components, comp)
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
	return nil
}

// SetQuickReplies sets quick reply options on the last assistant message.
// Creates an empty assistant message if no assistant message exists yet.
func (m *Manager) SetQuickReplies(id string, options []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil {
		return nil
	}
	if len(convo.Messages) == 0 || convo.Messages[len(convo.Messages)-1].Role != "assistant" {
		convo.Messages = append(convo.Messages, message.Message{
			Role:      "assistant",
			Content:   "",
			CreatedAt: time.Now(),
		})
	}
	last := &convo.Messages[len(convo.Messages)-1]
	last.QuickReplies = options
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
	return nil
}

// UpdateLastAssistantContent updates the content of the last assistant message.
// Does nothing if session not found or last message is not assistant.
func (m *Manager) UpdateLastAssistantContent(id, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil || len(convo.Messages) == 0 {
		return
	}
	last := &convo.Messages[len(convo.Messages)-1]
	if last.Role != "assistant" {
		return
	}
	last.Content = content
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
}

// AppendLastReasoningContent appends reasoning content to the last assistant message.
// Does nothing if session not found or last message is not assistant.
func (m *Manager) AppendLastReasoningContent(id, reasoningContent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil || len(convo.Messages) == 0 {
		return
	}
	last := &convo.Messages[len(convo.Messages)-1]
	if last.Role != "assistant" {
		return
	}
	last.ReasoningContent += reasoningContent
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
}

// SetLastReasoningContent sets the reasoning content on the last assistant message.
// Does nothing if session not found or last message is not assistant.
func (m *Manager) SetLastReasoningContent(id, reasoningContent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil || len(convo.Messages) == 0 {
		return
	}
	last := &convo.Messages[len(convo.Messages)-1]
	if last.Role != "assistant" {
		return
	}
	last.ReasoningContent = reasoningContent
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
}

func sessionTitlePreview(message string) string {
	normalized := strings.Join(strings.Fields(message), " ")
	if normalized == "" {
		return ""
	}
	if utf8.RuneCountInString(normalized) <= sessionTitlePreviewMaxRunes {
		return normalized
	}
	runes := []rune(normalized)
	return string(runes[:sessionTitlePreviewMaxRunes-1]) + "…"
}

// ActivateSkill adds a skill name to the session's active skills. No-op if session not found.
// Deduplicates: if skill already active, returns false.
func (m *Manager) ActivateSkill(id, skillName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil {
		return false
	}
	for _, existing := range convo.ActiveSkills {
		if existing == skillName {
			return false // already active
		}
	}
	convo.ActiveSkills = append(convo.ActiveSkills, skillName)
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
	return true
}

// DeactivateSkill removes a skill name from the session's active skills. No-op if session not found.
func (m *Manager) DeactivateSkill(id, skillName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil {
		return
	}
	for i, name := range convo.ActiveSkills {
		if name == skillName {
			convo.ActiveSkills = append(convo.ActiveSkills[:i], convo.ActiveSkills[i+1:]...)
			if meta := m.metaStore[id]; meta != nil {
				meta.UpdatedAt = time.Now()
			}
			return
		}
	}
}

// ActiveSkills returns the list of active skill names for a session. Returns nil if session not found.
func (m *Manager) ActiveSkills(id string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	convo := m.convoStore[id]
	if convo == nil {
		return nil
	}
	result := make([]string, len(convo.ActiveSkills))
	copy(result, convo.ActiveSkills)
	return result
}

// CopyConversation returns a detached deep copy of the Conversation for the
// session identified by id. Returns nil if the session does not exist.
//
// CopyConversation exists for callers that genuinely need a detached copy:
// the debug endpoints, which run concurrently with active agent runs and must
// not race with in-place mutations. Ordinary read-only access should use
// GetConversationShared, which is a cheap shared-reference return.
func (m *Manager) CopyConversation(id string) *Conversation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	convo := m.convoStore[id]
	if convo == nil {
		return nil
	}
	msgs := make([]message.Message, len(convo.Messages))
	copy(msgs, convo.Messages)
	skills := make([]string, len(convo.ActiveSkills))
	copy(skills, convo.ActiveSkills)
	return &Conversation{
		Messages:     msgs,
		SystemPrompt: convo.SystemPrompt,
		ActiveSkills: skills,
	}
}

// GetConversationShared returns the live Conversation for a session as a
// shared reference, without copying. Returns nil if the session does not exist.
//
// The returned pointer is owned by the manager. Callers must treat it as
// read-only and MUST NOT mutate it or any value reachable from it — all
// mutation must go through the manager's mutating methods (AppendMessage,
// AppendToConversation, ReplaceConversationMessages, SetSystemPrompt, etc.).
// The manager may mutate the pointed-to state concurrently, so the reference
// is only valid for the duration of a single read and must not be retained
// across calls that mutate the session.
func (m *Manager) GetConversationShared(id string) *Conversation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.convoStore[id]
}

// AppendToConversation appends a message to the session's conversation.
// No-op if the session does not exist.
func (m *Manager) AppendToConversation(id string, msg message.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil {
		return
	}
	convo.Messages = append(convo.Messages, msg)
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
}

// ReplaceConversationMessages replaces all messages in the session's conversation.
// The system prompt is NOT affected — only the message list is replaced.
// No-op if the session does not exist.
func (m *Manager) ReplaceConversationMessages(id string, msgs []message.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	convo := m.convoStore[id]
	if convo == nil {
		return
	}
	convo.Messages = msgs
	if meta := m.metaStore[id]; meta != nil {
		meta.UpdatedAt = time.Now()
	}
}

// SetSystemPrompt sets the system prompt on a session.
// No-op if the session does not exist.
func (m *Manager) SetSystemPrompt(id, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if convo := m.convoStore[id]; convo != nil {
		convo.SystemPrompt = prompt
		if meta := m.metaStore[id]; meta != nil {
			meta.UpdatedAt = time.Now()
		}
	}
}
