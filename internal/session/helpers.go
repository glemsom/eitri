// helpers.go — internal helpers for assembling/disassembling UISession from the three sub-stores, and ID generation.

package session

import (
	"crypto/rand"
	"fmt"

	"github.com/glemsom/eitri/internal/message"
)

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
		s.Messages = make([]message.Message, len(convo.Messages))
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
		Messages:     make([]message.Message, len(s.Messages)),
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
