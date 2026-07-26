// Package session manages in-memory browser-facing UI sessions.
// Sessions are scoped by browser_id (from cookie) and persist only in
// memory — server restart loses all sessions.
//
// Manager is the central type: it creates sessions, validates browser
// ownership, supports parent-child nesting for sub-agents, tracks active
// skills, manages a rendered-message-ID ring buffer for dedup on reconnect,
// and enforces a global session cap.
//
// Each UISession holds messages, components, quick-reply options, and
// reasoning content — all the data the browser needs to render the chat.
//
// Key types:
//   - Manager — thread-safe session lifecycle manager
//   - UISession — one browser chat session
//   - Message — canonical message type (alias for llm.Message)
//   - ComponentData — UI component data (alias for llm.ComponentData)
//   - Status — session status constants (idle, running, error)
//
// Key functions:
//   - NewManager — create a session manager with a capacity cap
//   - Create / Get / GetValidated / Delete — CRUD with browser ownership
//   - CreateChild — create a sub-agent child session
//   - AppendMessage / AppendComponent — add data to a session
//   - UpdateTitle / UpdateStatus — mutate session metadata
//   - ActivateSkill / DeactivateSkill — manage active skills per session
//   - AddRenderedMessageID / HasRenderedMessageID — dedup ring buffer
//
// Dependencies: internal/llm (Message and ComponentData type aliases)
//
// Extension points:
//   - Add session persistence (e.g. SQLite) for crash recovery
//   - Add session search/filter by metadata
//   - Add session expiry/cleanup goroutine
package session
