// config.go — per-session configuration (workspace path).

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
