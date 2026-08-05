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
// Read-path accessors:
//   - Shared read accessors (GetMetaShared, GetConversationShared,
//     GetConfigShared) — return the manager's internal state directly as
//     shared references, without copying. Callers must not mutate them and
//     must not retain them across calls that mutate the session. This is the
//     primary read API.
//   - Cheap facade assembly (Get, GetValidated, All, ListByBrowser) — build a
//     UISession facade with shared references; O(1) for the conversation, no
//     deep copy. Read-only views of manager-owned state.
//   - Explicit copy helpers (CopySession, CopyMeta, CopyConversation,
//     CopyConfig) — return detached deep copies for the few callers that
//     genuinely need them: JSON snapshot serialization (the persister needs a
//     detached UISession facade), the ChatPage/ReportPage template rendering
//     path (the templates consume the assembled UISession facade while a run
//     may be mutating in place), and the debug endpoints (they are polled
//     concurrently with active agent runs whose in-place mutations would race
//     with shared references).
//
// Expand-contract sequence (issues #979 → #980 → #981): the expand step added
// the shared read accessors; the migrate step (#980) switched read-only
// callers from the copying getters to the shared accessors; the contract step
// (#981) removed the deep-copy behaviour from the default read path. The
// copying getters (GetMeta, GetConversation, GetConfig) no longer exist — the
// default read path returns shared references, and remaining copy needs are
// served by the explicitly-named copy helpers above. New code that only reads
// session state should use the shared accessors or cheap facades.
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
//   - GetMetaShared / GetConversationShared / GetConfigShared — shared
//     read-only accessor views (primary read API)
//   - CopySession / CopyMeta / CopyConversation / CopyConfig — explicit
//     detached-copy helpers for the few callers that need snapshots
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
