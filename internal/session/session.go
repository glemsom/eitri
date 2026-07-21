// Package session manages in-memory UI sessions for the chat interface.
// Sessions are browser-scoped via browser_id cookie and persist only in memory.
// Server restart loses all sessions.
package session

import (
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Status represents the current state of a session.
type Status string

const (
	StatusIdle    Status = "idle"
	StatusRunning Status = "running"
	StatusError   Status = "error"
)

const sessionTitlePreviewMaxRunes = 31

// ComponentData holds a rendered UI component attached to an assistant message.
type ComponentData struct {
	Name string                 `json:"name"`
	Data map[string]interface{} `json:"data"`
}

// Message represents a single chat message in a session.
type Message struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	Components       []ComponentData `json:"components,omitempty"`
	QuickReplies     []string        `json:"quick_replies,omitempty"`
}

// UISession represents a browser-facing chat session.
// UISession represents a browser-facing chat session with id, browser_id, title, status, messages.
type UISession struct {
	ID           string    `json:"id"`
	BrowserID    string    `json:"browser_id"`
	Title        string    `json:"title"`
	Status       Status    `json:"status"`
	Messages     []Message `json:"messages"`
	ActiveSkills []string  `json:"active_skills"` // names of activated skills
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Manager manages in-memory UI sessions with browser ownership.
// Thread-safe. Enforces a maximum number of sessions globally.
type Manager struct {
	mu              sync.RWMutex
	sessions        map[string]*UISession // sessionID → session
	browserSessions map[string][]string   // browserID → ordered session IDs
	nextSessionNum  map[string]int        // browserID → next session number
	maxSessions     int
}

// NewManager creates a session manager with the given cap.
func NewManager(maxSessions int) *Manager {
	if maxSessions <= 0 {
		maxSessions = 10
	}
	return &Manager{
		sessions:        make(map[string]*UISession),
		browserSessions: make(map[string][]string),
		nextSessionNum:  make(map[string]int),
		maxSessions:     maxSessions,
	}
}

// All returns a copy of all sessions. Used for bulk operations.
func (m *Manager) All() []*UISession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*UISession, 0, len(m.sessions))
	for _, s := range m.sessions {
		result = append(result, s)
	}
	return result
}


// newID generates a random hex identifier using crypto/rand.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random ID: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

// Create creates a new session for the given browser_id.
// Returns the session and any error. If the browser has reached the session cap,
// returns a CapReached error.
func (m *Manager) Create(browserID string) (*UISession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check global cap
	if len(m.sessions) >= m.maxSessions {
		return nil, fmt.Errorf("session cap of %d reached", m.maxSessions)
	}

	id := newID()
	m.nextSessionNum[browserID]++

	sess := &UISession{
		ID:        id,
		BrowserID: browserID,
		Title:     fmt.Sprintf("Session %d", m.nextSessionNum[browserID]),
		Status:    StatusIdle,
		Messages:  make([]Message, 0),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	m.sessions[id] = sess
	m.browserSessions[browserID] = append(m.browserSessions[browserID], id)

	return sess, nil
}

// Get returns a session by ID. Returns nil if not found.
func (m *Manager) Get(id string) *UISession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// GetValidated returns a session by ID, checking ownership by browser_id.
// Returns the session and true if found and owned by browserID.
// Returns nil and false if not found or ownership mismatch.
func (m *Manager) GetValidated(id, browserID string) (*UISession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess := m.sessions[id]
	if sess == nil {
		return nil, false
	}
	if sess.BrowserID != browserID {
		return nil, false
	}
	return sess, true
}

// Delete removes a session by ID. Cancels any active run (delegated to caller).
// Returns the deleted session if found.
func (m *Manager) Delete(id string) *UISession {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess := m.sessions[id]
	if sess == nil {
		return nil
	}

	delete(m.sessions, id)

	// Remove from browser sessions list
	browserID := sess.BrowserID
	sessions := m.browserSessions[browserID]
	for i, sid := range sessions {
		if sid == id {
			m.browserSessions[browserID] = append(sessions[:i], sessions[i+1:]...)
			break
		}
	}

	return sess
}

// ListByBrowser returns all sessions for a given browser_id, ordered by creation.
func (m *Manager) ListByBrowser(browserID string) []*UISession {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := m.browserSessions[browserID]
	result := make([]*UISession, 0, len(ids))
	for _, id := range ids {
		if s := m.sessions[id]; s != nil {
			result = append(result, s)
		}
	}
	return result
}

// LastActive returns the most recently updated session for a browser_id.
// Returns nil if no sessions exist.
func (m *Manager) LastActive(browserID string) *UISession {
	sessions := m.ListByBrowser(browserID)
	if len(sessions) == 0 {
		return nil
	}

	var last *UISession
	for _, s := range sessions {
		if last == nil || s.UpdatedAt.After(last.UpdatedAt) {
			last = s
		}
	}
	return last
}

// Count returns the total number of sessions across all browsers.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// BrowserCount returns the number of sessions for a given browser_id.
func (m *Manager) BrowserCount(browserID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.browserSessions[browserID])
}

// UpdateTitle updates the title of a session. No-op if session not found.
func (m *Manager) UpdateTitle(id, title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		s.Title = title
		s.UpdatedAt = time.Now()
	}
}

// UpdateStatus updates the status of a session. No-op if session not found.
func (m *Manager) UpdateStatus(id string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		s.Status = status
		s.UpdatedAt = time.Now()
	}
}

// AppendMessage appends a message to a session. No-op if session not found.
// Title is updated to the latest user message's preview.
func (m *Manager) AppendMessage(id string, msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		if msg.Role == "user" {
			if title := sessionTitlePreview(msg.Content); title != "" {
				s.Title = title
			}
		}
		s.Messages = append(s.Messages, msg)
		s.UpdatedAt = time.Now()
	}
}

// AppendComponent appends component data to a session.
// Creates an empty assistant message if no assistant message exists yet.
func (m *Manager) AppendComponent(id string, comp ComponentData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil
	}
	if len(s.Messages) == 0 {
		return nil
	}
	last := &s.Messages[len(s.Messages)-1]
	if last.Role != "assistant" {
		// Create an assistant message so components have a target to attach to.
		// Content will be filled when the run completes.
		s.Messages = append(s.Messages, Message{
			Role:      "assistant",
			Content:   "",
			CreatedAt: time.Now(),
		})
		last = &s.Messages[len(s.Messages)-1]
	}
	last.Components = append(last.Components, comp)
	s.UpdatedAt = time.Now()
	return nil
}

// SetQuickReplies sets quick reply options on the last assistant message.
// Creates an empty assistant message if no assistant message exists yet.
func (m *Manager) SetQuickReplies(id string, options []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return nil
	}
	if len(s.Messages) == 0 || s.Messages[len(s.Messages)-1].Role != "assistant" {
		s.Messages = append(s.Messages, Message{
			Role:      "assistant",
			Content:   "",
			CreatedAt: time.Now(),
		})
	}
	last := &s.Messages[len(s.Messages)-1]
	last.QuickReplies = options
	s.UpdatedAt = time.Now()
	return nil
}


// UpdateLastAssistantContent updates the content of the last assistant message.
// Does nothing if session not found or last message is not assistant.
func (m *Manager) UpdateLastAssistantContent(id, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || len(s.Messages) == 0 {
		return
	}
	last := &s.Messages[len(s.Messages)-1]
	if last.Role != "assistant" {
		return
	}
	last.Content = content
	s.UpdatedAt = time.Now()
}

// AppendingReasoningContent appends reasoning content to the last assistant message.
// Does nothing if session not found or last message is not assistant.
func (m *Manager) AppendLastReasoningContent(id, reasoningContent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || len(s.Messages) == 0 {
		return
	}
	last := &s.Messages[len(s.Messages)-1]
	if last.Role != "assistant" {
		return
	}
	last.ReasoningContent += reasoningContent
	s.UpdatedAt = time.Now()
}

// SetLastReasoningContent sets the reasoning content on the last assistant message.
// Does nothing if session not found or last message is not assistant.
func (m *Manager) SetLastReasoningContent(id, reasoningContent string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil || len(s.Messages) == 0 {
		return
	}
	last := &s.Messages[len(s.Messages)-1]
	if last.Role != "assistant" {
		return
	}
	last.ReasoningContent = reasoningContent
	s.UpdatedAt = time.Now()
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
	s := m.sessions[id]
	if s == nil {
		return false
	}
	for _, existing := range s.ActiveSkills {
		if existing == skillName {
			return false // already active
		}
	}
	s.ActiveSkills = append(s.ActiveSkills, skillName)
	s.UpdatedAt = time.Now()
	return true
}

// DeactivateSkill removes a skill name from the session's active skills. No-op if session not found.
func (m *Manager) DeactivateSkill(id, skillName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	for i, name := range s.ActiveSkills {
		if name == skillName {
			s.ActiveSkills = append(s.ActiveSkills[:i], s.ActiveSkills[i+1:]...)
			s.UpdatedAt = time.Now()
			return
		}
	}
}

// ActiveSkills returns the list of active skill names for a session. Returns nil if session not found.
func (m *Manager) ActiveSkills(id string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[id]
	if s == nil {
		return nil
	}
	result := make([]string, len(s.ActiveSkills))
	copy(result, s.ActiveSkills)
	return result
}
