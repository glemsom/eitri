// metadata.go — session metadata mutations: title, status, closed-at timestamps, and the SessionMeta accessors (copying + shared).

package session

import (
	"time"
)

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

// CopyMeta returns a detached deep copy of the SessionMeta for the session
// identified by id. Returns nil if the session does not exist.
//
// CopyMeta exists for callers that genuinely need a detached copy: the debug
// endpoints, which run concurrently with active agent runs and must not race
// with in-place mutations. Ordinary read-only access should use
// GetMetaShared, which is a cheap shared-reference return.
func (m *Manager) CopyMeta(id string) *SessionMeta {
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

// GetMetaShared returns the live SessionMeta for a session as a shared
// reference, without copying. Returns nil if the session does not exist.
//
// The returned pointer is owned by the manager. Callers must treat it as
// read-only and MUST NOT mutate it or any value reachable from it — all
// mutation must go through the manager's mutating methods (UpdateTitle,
// UpdateStatus, UpdateMeta, etc.). The manager may mutate the pointed-to
// state concurrently, so the reference is only valid for the duration of a
// single read and must not be retained across calls that mutate the session.
func (m *Manager) GetMetaShared(id string) *SessionMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.metaStore[id]
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
