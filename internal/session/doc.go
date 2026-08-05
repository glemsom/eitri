// Package session manages in-memory browser-facing UI sessions.
// Sessions are scoped by browser_id (from cookie) and persist only in
// memory — server restart loses all sessions.
//
// Manager is the central type: it creates sessions, validates browser
// ownership, supports parent-child nesting for sub-agents, tracks active
// skills, manages a rendered-message-ID ring buffer for dedup on reconnect,
// and enforces a global session cap.
//
// File layout (all files are in this package):
//   - types.go — shared types (Status, ContextFile, UISession, SessionMeta,
//     Conversation, SessionConfig, Manager struct)
//   - manager.go — Manager lifecycle: construction, CRUD, capacity, disk loads
//   - helpers.go — internal assemble/split helpers and ID generation
//   - metadata.go — session metadata mutations (title, status, timestamps)
//   - conversation.go — messages, components, quick replies, active skills
//   - config.go — per-session config/workspace
//   - browser.go — browser session ordering and indexing
//   - child.go — parent-child (sub-agent) session management
//   - ring.go — rendered-message-ID dedup ring buffer
//
// Internally, Manager stores session data in three sub-stores for clean
// separation of concerns:
//   - metaStore (SessionMeta) — identity, status, timestamps, ring buffer
//   - convoStore (Conversation) — messages, system prompt, active skills
//   - configStore (SessionConfig) — workspace path
//
// Read-path accessors exist in two flavours:
//   - Copying getters (GetMeta, GetConversation, GetConfig) — return detached
//     copies safe for mutation. These deep-copy the conversation (every
//     message, including components) on every call and are being phased out.
//   - Shared read accessors (GetMetaShared, GetConversationShared,
//     GetConfigShared) — return the manager's internal state directly as
//     shared references. Callers must not mutate them. This is the preferred
//     read API going forward.
//
// Expand-contract sequence (issues #979 → #980 → #981): the expand step added
// the shared read accessors (this codebase's current state); the migrate step
// (#980) switches read-only callers from the copying getters to the shared
// accessors; the contract step (#981) removes the deep-copy behaviour from the
// read path. Until #981 lands, the copying getters must stay unchanged so the
// migrate step can proceed incrementally with CI green throughout. New code
// that only reads session state should use the shared accessors.
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
//   - GetMeta / GetConversation / GetConfig — copying accessor views (legacy)
//   - GetMetaShared / GetConversationShared / GetConfigShared — shared
//     read-only accessor views (preferred; see expand-contract note above)
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
