// Package session manages in-memory UI sessions for the chat interface.
// Sessions are browser-scoped via browser_id cookie and persist only in memory.
// Server restart loses all sessions.
package session

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/glemsom/eitri/internal/llm"
)

// Status represents the current state of a session.
type Status string

const (
	StatusIdle    Status = "idle"
	StatusRunning Status = "running"
	StatusError   Status = "error"
)

const sessionTitlePreviewMaxRunes = 31

// ContextFile represents a file loaded as additional agent context
// (e.g., AGENTS.md or a file referenced by AGENTS.md).
type ContextFile struct {
	Path  string `json:"path"`  // relative path from workspace root (e.g. "AGENTS.md")
	Depth int    `json:"depth"` // nesting depth: 0 = root (AGENTS.md), 1 = referenced by root, etc.
}

// UISession represents a browser-facing chat session.
// It is a JSON serialization facade assembled from Manager sub-stores.
// UISession represents a browser-facing chat session with id, browser_id, title, status, messages.
// ParentID is empty for root sessions and non-empty for child sessions (sub-agents).
type UISession struct {
	ID           string    `json:"id"`
	BrowserID    string    `json:"browser_id"`
	ParentID     string    `json:"parent_id,omitempty"`
	Title        string    `json:"title"`
	Status       Status    `json:"status"`
	Messages     []llm.Message `json:"messages"`
	ActiveSkills []string  `json:"active_skills"` // names of activated skills
	Workspace    string    `json:"workspace"`     // filesystem root directory for this session
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"` // set when session is closed (not deleted)

	// SystemPrompt is the system prompt used for this session's run.
	// Persisted to session snapshots so reports can display it.
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Ring buffer of last N rendered message IDs for dedup on reconnect.
	// Capacity 10; oldest are evicted.
	RenderedMessageIDs   []string `json:"rendered_message_ids,omitempty"`
	renderedMessageIDIdx int      // next write index in the ring buffer
}

// SessionMeta holds the identity, status, and timestamp fields of a session.
// Used as the meta sub-store in Manager.
type SessionMeta struct {
	ID        string
	BrowserID string
	ParentID  string
	Title     string
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  *time.Time

	// Ring buffer of last N rendered message IDs for dedup on reconnect.
	RenderedMessageIDs   []string
	renderedMessageIDIdx int
}

// Conversation holds the chat data of a session.
// Used as the conversation sub-store in Manager.
type Conversation struct {
	Messages     []llm.Message
	SystemPrompt string
	ActiveSkills []string
}

// SessionConfig holds per-session settings.
// Used as the config sub-store in Manager.
type SessionConfig struct {
	Workspace string
}

// Manager manages in-memory UI sessions with browser ownership.
// Thread-safe. Enforces a maximum number of sessions globally.
// Session data is stored in three sub-stores for clean separation:
// metaStore (identity, status, timestamps), convoStore (messages,
// system prompt, active skills), configStore (workspace).
type Manager struct {
	mu               sync.RWMutex
	metaStore        map[string]*SessionMeta    // sessionID → metadata
	convoStore       map[string]*Conversation   // sessionID → conversation data
	configStore      map[string]*SessionConfig  // sessionID → config
	browserSessions  map[string][]string         // browserID → ordered session IDs
	nextSessionNum   map[string]int              // browserID → next session number
	maxSessions      int
	defaultWorkspace string // filesystem root for new sessions
}

// NewManager creates a session manager with the given cap and default workspace.
// New sessions will use defaultWorkspace as their initial workspace.
func NewManager(maxSessions int, defaultWorkspace string) *Manager {
	if maxSessions <= 0 {
		maxSessions = 10
	}
	return &Manager{
		metaStore:        make(map[string]*SessionMeta),
		convoStore:       make(map[string]*Conversation),
		configStore:      make(map[string]*SessionConfig),
		browserSessions:  make(map[string][]string),
		nextSessionNum:   make(map[string]int),
		maxSessions:      maxSessions,
		defaultWorkspace: defaultWorkspace,
	}
}

// assembleSession builds a UISession from the three sub-stores.
// Returns nil if the session ID does not exist in metaStore.
// The caller receives a deep copy safe to use outside the lock.
func (m *Manager) assembleSession(id string) *UISession {
	meta := m.metaStore[id]
	if meta == nil {
		return nil
	}
	convo := m.convoStore[id]
	cfg := m.configStore[id]

	s := &UISession{
		ID:                   meta.ID,
		BrowserID:            meta.BrowserID,
		ParentID:             meta.ParentID,
		Title:                meta.Title,
		Status:               meta.Status,
		CreatedAt:            meta.CreatedAt,
		UpdatedAt:            meta.UpdatedAt,
		ClosedAt:             meta.ClosedAt,
		RenderedMessageIDs:   make([]string, len(meta.RenderedMessageIDs)),
		renderedMessageIDIdx: meta.renderedMessageIDIdx,
	}
	copy(s.RenderedMessageIDs, meta.RenderedMessageIDs)

	if convo != nil {
		s.Messages = make([]llm.Message, len(convo.Messages))
		copy(s.Messages, convo.Messages)
		s.SystemPrompt = convo.SystemPrompt
		s.ActiveSkills = make([]string, len(convo.ActiveSkills))
		copy(s.ActiveSkills, convo.ActiveSkills)
	}

	if cfg != nil {
		s.Workspace = cfg.Workspace
	}

	return s
}

// splitSession disassembles a UISession into the three sub-store entries.
// Must be called with m.mu held (write lock).
func (m *Manager) splitSession(s *UISession) {
	m.metaStore[s.ID] = &SessionMeta{
		ID:                   s.ID,
		BrowserID:            s.BrowserID,
		ParentID:             s.ParentID,
		Title:                s.Title,
		Status:               s.Status,
		CreatedAt:            s.CreatedAt,
		UpdatedAt:            s.UpdatedAt,
		ClosedAt:             s.ClosedAt,
		RenderedMessageIDs:   make([]string, len(s.RenderedMessageIDs)),
		renderedMessageIDIdx: s.renderedMessageIDIdx,
	}
	copy(m.metaStore[s.ID].RenderedMessageIDs, s.RenderedMessageIDs)

	m.convoStore[s.ID] = &Conversation{
		Messages:     make([]llm.Message, len(s.Messages)),
		SystemPrompt: s.SystemPrompt,
		ActiveSkills: make([]string, len(s.ActiveSkills)),
	}
	copy(m.convoStore[s.ID].Messages, s.Messages)
	copy(m.convoStore[s.ID].ActiveSkills, s.ActiveSkills)

	m.configStore[s.ID] = &SessionConfig{
		Workspace: s.Workspace,
	}
}

// newID generates a random hex identifier using crypto/rand.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random ID: %v", err))
	}
	return fmt.Sprintf("%x", b)
}

// All returns a copy of all sessions. Used for bulk operations.
func (m *Manager) All() []*UISession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*UISession, 0, len(m.metaStore))
	for id := range m.metaStore {
		if s := m.assembleSession(id); s != nil {
			result = append(result, s)
		}
	}
	return result
}

// Create creates a new session for the given browser_id.
// Returns the session and any error. If the browser has reached the session cap,
// returns a CapReached error.
func (m *Manager) Create(browserID string) (*UISession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check global cap
	if len(m.metaStore) >= m.maxSessions {
		return nil, fmt.Errorf("session cap of %d reached", m.maxSessions)
	}

	id := newID()
	m.nextSessionNum[browserID]++

	now := time.Now()
	m.metaStore[id] = &SessionMeta{
		ID:        id,
		BrowserID: browserID,
		Title:     fmt.Sprintf("Session %d", m.nextSessionNum[browserID]),
		Status:    StatusIdle,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.convoStore[id] = &Conversation{
		Messages: make([]llm.Message, 0),
	}
	m.configStore[id] = &SessionConfig{
		Workspace: m.defaultWorkspace,
	}

	m.browserSessions[browserID] = append(m.browserSessions[browserID], id)

	return m.assembleSession(id), nil
}

// Add inserts a pre-existing session directly into the manager.
// Used for restoring sessions from disk on startup.
// If a session with the same ID already exists, it is overwritten.
func (m *Manager) Add(sess *UISession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.splitSession(sess)
	// Ensure it appears in browser sessions list
	found := false
	for _, id := range m.browserSessions[sess.BrowserID] {
		if id == sess.ID {
			found = true
			break
		}
	}
	if !found {
		m.browserSessions[sess.BrowserID] = append(m.browserSessions[sess.BrowserID], sess.ID)
	}
}

// LoadFromDisk adds a previously-persisted session back into the in-memory manager.
// The data parameter is raw JSON (the contents of a session.json snapshot file).
// The session status is forced to idle regardless of the stored value.
// Returns the loaded session and nil error on success.
// Returns an error if the data cannot be unmarshalled.
// Returns ErrSessionIDCollision if a session with the same ID already exists
// in the manager (should not happen with proper close).
func (m *Manager) LoadFromDisk(data []byte) (*UISession, error) {
	var sess UISession
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("cannot unmarshal session data: %w", err)
	}

	// Check for ID collision
	m.mu.RLock()
	_, exists := m.metaStore[sess.ID]
	m.mu.RUnlock()
	if exists {
		return nil, fmt.Errorf("%w: session %s", ErrSessionIDCollision, sess.ID)
	}

	sess.Status = StatusIdle
	sess.UpdatedAt = time.Now()
	sess.ClosedAt = nil

	m.Add(&sess)
	// Return an assembled copy
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.assembleSession(sess.ID), nil
}

// ErrSessionIDCollision is returned by LoadFromDisk when a session with the
// same ID already exists in the manager.
var ErrSessionIDCollision = fmt.Errorf("session ID collision")

// Get returns a session by ID. Returns nil if not found.
func (m *Manager) Get(id string) *UISession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.assembleSession(id)
}

// GetValidated returns a session by ID, checking ownership by browser_id.
// Returns the session and true if found and owned by browserID.
// Returns nil and false if not found or ownership mismatch.
func (m *Manager) GetValidated(id, browserID string) (*UISession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta := m.metaStore[id]
	if meta == nil {
		return nil, false
	}
	if meta.BrowserID != browserID {
		return nil, false
	}
	return m.assembleSession(id), true
}

// CreateChild creates a child session under a parent session.
// Returns error if parent session does not exist or cap is reached.
func (m *Manager) CreateChild(parentID, browserID, title string) (*UISession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Verify parent exists
	parentMeta := m.metaStore[parentID]
	if parentMeta == nil {
		return nil, fmt.Errorf("parent session %s not found", parentID)
	}

	// Check global cap
	if len(m.metaStore) >= m.maxSessions {
		return nil, fmt.Errorf("session cap of %d reached", m.maxSessions)
	}

	id := newID()
	now := time.Now()
	m.metaStore[id] = &SessionMeta{
		ID:        id,
		BrowserID: browserID,
		ParentID:  parentID,
		Title:     title,
		Status:    StatusRunning,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.convoStore[id] = &Conversation{
		Messages: make([]llm.Message, 0),
	}
	m.configStore[id] = &SessionConfig{
		Workspace: m.configStore[parentID].Workspace,
	}

	m.browserSessions[browserID] = append(m.browserSessions[browserID], id)

	return m.assembleSession(id), nil
}

// ChildrenOf returns all child sessions for a given parent session ID.
func (m *Manager) ChildrenOf(parentID string) []*UISession {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*UISession
	for id, meta := range m.metaStore {
		if meta.ParentID == parentID {
			if s := m.assembleSession(id); s != nil {
				result = append(result, s)
			}
		}
	}
	return result
}

// cascadeRemoveChildren removes all child sessions of the given parent from the manager.
// If beforeRemove is non-nil, it is called on each child before removal (e.g. to set ClosedAt).
// Must be called with m.mu held.
func (m *Manager) cascadeRemoveChildren(parentID, browserID string, beforeRemove func(*UISession)) {
	var childIDs []string
	for id, meta := range m.metaStore {
		if meta.ParentID == parentID {
			childIDs = append(childIDs, id)
		}
	}
	for _, cid := range childIDs {
		if beforeRemove != nil {
			if child := m.assembleSession(cid); child != nil {
				beforeRemove(child)
			}
		}
		delete(m.metaStore, cid)
		delete(m.convoStore, cid)
		delete(m.configStore, cid)
		bSessions := m.browserSessions[browserID]
		for i, sid := range bSessions {
			if sid == cid {
				m.browserSessions[browserID] = append(bSessions[:i], bSessions[i+1:]...)
				break
			}
		}
	}
}

// Close removes a session from the in-memory manager without deleting disk data.
// Sets ClosedAt timestamp. Cascade-closes any child sessions.
func (m *Manager) Close(id string) *UISession {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta := m.metaStore[id]
	if meta == nil {
		return nil
	}

	now := time.Now()
	meta.ClosedAt = &now
	meta.UpdatedAt = now

	// Cascade close children first
	m.cascadeRemoveChildren(id, meta.BrowserID, func(child *UISession) {
		if childMeta := m.metaStore[child.ID]; childMeta != nil {
			childMeta.ClosedAt = &now
			childMeta.UpdatedAt = now
		}
	})

	sess := m.assembleSession(id)

	delete(m.metaStore, id)
	delete(m.convoStore, id)
	delete(m.configStore, id)

	// Remove from browser sessions list
	browserSessions := m.browserSessions[meta.BrowserID]
	for i, sid := range browserSessions {
		if sid == id {
			m.browserSessions[meta.BrowserID] = append(browserSessions[:i], browserSessions[i+1:]...)
			break
		}
	}

	return sess
}

// Delete removes a session by ID. Cancels any active run (delegated to caller).
// Cascade-deletes any child sessions. Returns the deleted session if found.
// Unlike Close, this permanently removes the session; caller should also clean up disk data.
func (m *Manager) Delete(id string) *UISession {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta := m.metaStore[id]
	if meta == nil {
		return nil
	}

	sess := m.assembleSession(id)

	// Cascade delete children first
	m.cascadeRemoveChildren(id, meta.BrowserID, nil)

	delete(m.metaStore, id)
	delete(m.convoStore, id)
	delete(m.configStore, id)

	// Remove from browser sessions list
	browserSessions := m.browserSessions[meta.BrowserID]
	for i, sid := range browserSessions {
		if sid == id {
			m.browserSessions[meta.BrowserID] = append(browserSessions[:i], browserSessions[i+1:]...)
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
		if s := m.assembleSession(id); s != nil {
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
	return len(m.metaStore)
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
	if meta := m.metaStore[id]; meta != nil {
		meta.Title = title
		meta.UpdatedAt = time.Now()
	}
}

// UpdateStatus updates the status of a session. No-op if session not found.
func (m *Manager) UpdateStatus(id string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta := m.metaStore[id]; meta != nil {
		meta.Status = status
		meta.UpdatedAt = time.Now()
	}
}

// SetWorkspace updates the workspace root directory for a session.
// No-op if session not found. Takes effect on the next agent run.
func (m *Manager) SetWorkspace(id, workspace string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta := m.metaStore[id]; meta != nil {
		if cfg := m.configStore[id]; cfg != nil {
			cfg.Workspace = workspace
		}
		meta.UpdatedAt = time.Now()
	}
}

// AppendMessage appends a message to a session. No-op if session not found.
// Title is updated to the latest user message's preview.
func (m *Manager) AppendMessage(id string, msg llm.Message) {
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
func (m *Manager) AppendComponent(id string, comp llm.ComponentData) error {
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
		convo.Messages = append(convo.Messages, llm.Message{
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
		convo.Messages = append(convo.Messages, llm.Message{
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

// ringBufferCap is the max number of rendered message IDs tracked per session.
const ringBufferCap = 10

// AddRenderedMessageID adds a message ID to the session's dedup ring buffer.
// No-op if session not found.
func (m *Manager) AddRenderedMessageID(id, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := m.metaStore[id]
	if meta == nil {
		return
	}
	if messageID == "" {
		return
	}
	// Initialize ring buffer on first use
	if meta.RenderedMessageIDs == nil {
		meta.RenderedMessageIDs = make([]string, ringBufferCap)
		meta.renderedMessageIDIdx = 0
	}
	meta.RenderedMessageIDs[meta.renderedMessageIDIdx] = messageID
	meta.renderedMessageIDIdx = (meta.renderedMessageIDIdx + 1) % ringBufferCap
}

// HasRenderedMessageID returns true if the message ID is in the session's dedup ring buffer.
func (m *Manager) HasRenderedMessageID(id, messageID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta := m.metaStore[id]
	if meta == nil || meta.RenderedMessageIDs == nil || messageID == "" {
		return false
	}
	for _, mid := range meta.RenderedMessageIDs {
		if mid == messageID {
			return true
		}
	}
	return false
}

// GetMeta returns a SessionMeta view of the session identified by id.
// Returns nil if the session does not exist.
// The returned SessionMeta is a copy safe for use outside the lock.
func (m *Manager) GetMeta(id string) *SessionMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	meta := m.metaStore[id]
	if meta == nil {
		return nil
	}
	cp := &SessionMeta{
		ID:                   meta.ID,
		BrowserID:            meta.BrowserID,
		ParentID:             meta.ParentID,
		Title:                meta.Title,
		Status:               meta.Status,
		CreatedAt:            meta.CreatedAt,
		UpdatedAt:            meta.UpdatedAt,
		ClosedAt:             meta.ClosedAt,
		RenderedMessageIDs:   make([]string, len(meta.RenderedMessageIDs)),
		renderedMessageIDIdx: meta.renderedMessageIDIdx,
	}
	copy(cp.RenderedMessageIDs, meta.RenderedMessageIDs)
	return cp
}

// GetConversation returns a Conversation view of the session identified by id.
// Returns nil if the session does not exist.
// The returned Conversation is a copy safe for use outside the lock.
func (m *Manager) GetConversation(id string) *Conversation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	convo := m.convoStore[id]
	if convo == nil {
		return nil
	}
	msgs := make([]llm.Message, len(convo.Messages))
	copy(msgs, convo.Messages)
	skills := make([]string, len(convo.ActiveSkills))
	copy(skills, convo.ActiveSkills)
	return &Conversation{
		Messages:     msgs,
		SystemPrompt: convo.SystemPrompt,
		ActiveSkills: skills,
	}
}

// GetConfig returns a SessionConfig view of the session identified by id.
// Returns nil if the session does not exist.
// The returned SessionConfig is a copy safe for use outside the lock.
func (m *Manager) GetConfig(id string) *SessionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.configStore[id]
	if cfg == nil {
		return nil
	}
	return &SessionConfig{
		Workspace: cfg.Workspace,
	}
}

// UpdateMeta updates the metadata fields of a session from the given SessionMeta.
// Only non-zero-value fields are applied. The session's UpdatedAt is always set to now.
// No-op if the session does not exist.
func (m *Manager) UpdateMeta(id string, meta *SessionMeta) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing := m.metaStore[id]
	if existing == nil || meta == nil {
		return
	}
	if meta.Title != "" {
		existing.Title = meta.Title
	}
	if meta.Status != "" {
		existing.Status = meta.Status
	}
	if meta.ClosedAt != nil {
		existing.ClosedAt = meta.ClosedAt
	}
	existing.UpdatedAt = time.Now()
}

// AppendToConversation appends a message to the session's conversation.
// No-op if the session does not exist.
func (m *Manager) AppendToConversation(id string, msg llm.Message) {
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

// UpdateConfig updates the configuration fields of a session from the given SessionConfig.
// Only non-zero-value fields are applied. No-op if the session does not exist.
func (m *Manager) UpdateConfig(id string, config *SessionConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existingCfg := m.configStore[id]
	if existingCfg == nil || config == nil {
		return
	}
	if config.Workspace != "" {
		existingCfg.Workspace = config.Workspace
	}
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

// SetBrowserID reassigns a session to a new browser ID.
// Handles updating both the session's BrowserID field and the
// browser session index. No-op if the session does not exist.
func (m *Manager) SetBrowserID(id, browserID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta := m.metaStore[id]
	if meta == nil {
		return
	}
	// Remove from old browser index
	if meta.BrowserID != "" {
		oldList := m.browserSessions[meta.BrowserID]
		for i, sid := range oldList {
			if sid == id {
				m.browserSessions[meta.BrowserID] = append(oldList[:i], oldList[i+1:]...)
				break
			}
		}
	}
	meta.BrowserID = browserID
	m.browserSessions[browserID] = append(m.browserSessions[browserID], id)
	meta.UpdatedAt = time.Now()
}

// SetClosedAt sets the ClosedAt timestamp on a session. Pass nil to clear it.
// No-op if the session does not exist.
func (m *Manager) SetClosedAt(id string, t *time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if meta := m.metaStore[id]; meta != nil {
		meta.ClosedAt = t
		meta.UpdatedAt = time.Now()
	}
}
