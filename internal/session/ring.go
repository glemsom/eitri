// ring.go — rendered-message-ID dedup ring buffer.

package session

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
