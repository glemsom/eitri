// manager.go — Manager lifecycle: construction, CRUD, and session capacity.

package session

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/glemsom/eitri/internal/message"
)

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
		Messages: make([]message.Message, 0),
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

// Count returns the total number of sessions across all browsers.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.metaStore)
}
