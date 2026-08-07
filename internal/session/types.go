// types.go — shared types: Status, ContextFile, UISession, SessionMeta, Conversation, SessionConfig, Manager.

package session

import (
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/message"
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
	ID           string            `json:"id"`
	BrowserID    string            `json:"browser_id"`
	ParentID     string            `json:"parent_id,omitempty"`
	Title        string            `json:"title"`
	Status       Status            `json:"status"`
	Messages     []message.Message `json:"messages"`
	ActiveSkills []string          `json:"active_skills"` // names of activated skills
	Workspace    string            `json:"workspace"`     // filesystem root directory for this session
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	ClosedAt     *time.Time        `json:"closed_at,omitempty"` // set when session is closed (not deleted)

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
	Messages     []message.Message
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
	metaStore        map[string]*SessionMeta   // sessionID → metadata
	convoStore       map[string]*Conversation  // sessionID → conversation data
	configStore      map[string]*SessionConfig // sessionID → config
	browserSessions  map[string][]string       // browserID → ordered session IDs
	nextSessionNum   map[string]int            // browserID → next session number
	maxSessions      int
	maxExchanges     int    // per-session exchange-cap sliding window (message.DefaultMaxExchanges)
	defaultWorkspace string // filesystem root for new sessions
}
