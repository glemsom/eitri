// browser.go — browser session ordering: listing, last-active, and the browserSessions index.

package session

import (
	"time"
)

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

// BrowserCount returns the number of sessions for a given browser_id.
func (m *Manager) BrowserCount(browserID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.browserSessions[browserID])
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
