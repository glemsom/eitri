// Package session manages in-memory browser-facing UI sessions.
// Sessions are scoped by browser_id (from cookie) and persist only in
// memory — server restart loses all sessions.
//
// Manager is the central type: it creates sessions, validates browser
// ownership, supports parent-child nesting for sub-agents, tracks active
// skills, manages a rendered-message-ID ring buffer for dedup on reconnect,
// and enforces a global session cap.
//
// Internally, Manager stores session data in three sub-stores for clean
// separation of concerns:
//   - metaStore (SessionMeta) — identity, status, timestamps, ring buffer
//   - convoStore (Conversation) — messages, system prompt, active skills
//   - configStore (SessionConfig) — workspace path
//
// UISession is kept as a JSON serialization facade — it is assembled from
// the three sub-stores on demand when snapshot I/O or direct field access
// is needed. All runtime CRUD and mutation operations work on the sub-stores
// directly through typed accessor methods.
//
// The JSON snapshot format (session.json on disk) remains fully backward-
// compatible: existing snapshots from before the sub-store split are still
// loadable via LoadFromDisk.
//
// Key types:
//   - Manager — thread-safe session lifecycle manager
//   - UISession — JSON serialization facade for one browser chat session
//   - SessionMeta — identity, status, and timestamp view (meta sub-store)
//   - Conversation — messages, system prompt, and active skills (convo sub-store)
//   - SessionConfig — per-session settings, e.g. workspace (config sub-store)
//   - Status — session status constants (idle, running, error)
//
// Key functions:
//   - NewManager — create a session manager with a capacity cap
//   - Create / Get / GetValidated / Delete — CRUD with browser ownership
//   - CreateChild — create a sub-agent child session
//   - GetMeta / GetConversation / GetConfig — typed accessor views
//   - UpdateMeta / AppendToConversation / UpdateConfig — typed setter methods
//   - AppendMessage / AppendComponent — add data to a session
//   - UpdateTitle / UpdateStatus — mutate session metadata
//   - ActivateSkill / DeactivateSkill — manage active skills per session
//   - AddRenderedMessageID / HasRenderedMessageID — dedup ring buffer
//
// Dependencies: internal/message (Message and ComponentData types)
//
// Extension points:
//   - Add session persistence (e.g. SQLite) for crash recovery
//   - Add session search/filter by metadata
//   - Add session expiry/cleanup goroutine
package session
