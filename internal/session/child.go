// child.go — parent-child (sub-agent) session management.

package session

import (
	"fmt"
	"time"

	"github.com/glemsom/eitri/internal/message"
)

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
		Messages: make([]message.Message, 0),
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
