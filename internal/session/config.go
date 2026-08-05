// config.go — per-session configuration (workspace path) and the SessionConfig accessors (copying + shared).

package session

import (
	"time"
)

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

// CopyConfig returns a detached deep copy of the SessionConfig for the
// session identified by id. Returns nil if the session does not exist.
//
// CopyConfig exists for callers that genuinely need a detached copy.
// Ordinary read-only access should use GetConfigShared, which is a cheap
// shared-reference return.
func (m *Manager) CopyConfig(id string) *SessionConfig {
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

// GetConfigShared returns the live SessionConfig for a session as a shared
// reference, without copying. Returns nil if the session does not exist.
//
// The returned pointer is owned by the manager. Callers must treat it as
// read-only and MUST NOT mutate it — all mutation must go through the
// manager's mutating methods (SetWorkspace, UpdateConfig, etc.). The manager
// may mutate the pointed-to state concurrently, so the reference is only
// valid for the duration of a single read and must not be retained across
// calls that mutate the session.
func (m *Manager) GetConfigShared(id string) *SessionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configStore[id]
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
