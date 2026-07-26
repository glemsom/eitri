# Spec 003: Split UISession god-struct into focused sub-types

## Problem

`UISession` (682 lines in `internal/session/session.go`) mixes three distinct concerns in one struct:

```go
type UISession struct {
    // Session identity (meta)
    ID        string    `json:"id"`
    BrowserID string    `json:"browser_id"`
    ParentID  string    `json:"parent_id,omitempty"`
    Title     string    `json:"title"`
    Status    Status    `json:"status"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    ClosedAt  *time.Time `json:"closed_at,omitempty"`

    // Conversation content
    Messages     []Message `json:"messages"`
    SystemPrompt string    `json:"system_prompt,omitempty"`

    // Session config
    Workspace    string   `json:"workspace"`
    ActiveSkills []string `json:"active_skills"`

    // UI ephemera
    RenderedMessageIDs   []string `json:"rendered_message_ids,omitempty"`
    renderedMessageIDIdx int
}
```

Everything (API handlers, run service, persister, debug package) reads and writes this one type. A reader can't tell at a glance which fields matter for a given code path. The `Manager` (same file, 682 lines) operates on all of these fields through a wide surface of methods. This makes both human and LLM reading harder because every subsystem sees the full god-struct regardless of what it actually needs.

## Solution

Split into three focused types used by composition, and refactor `Manager` into internal sub-stores:

1. **`SessionMeta`** — identity, status, timestamps (needed by sidebar listings, session CRUD)
2. **`Conversation`** — messages, system prompt, active skills (needed by run service, LLM adapters, reports)
3. **`SessionConfig`** — workspace (needed by tool dispatch, sandbox)

## User Stories

1. As a developer reading the API handler for the session sidebar, I want to see only the identity/title/status of each session, so that I can understand the listing flow without scrolling past messages and workspace config.
2. As an LLM agent tracing how a message gets appended, I want to work with a `Conversation` type that only has messages and skills, so that I don't waste context on irrelevant session metadata.
3. As a contributor adding a new session config field (e.g., theme preference), I want to add it to `SessionConfig` without touching the conversation or metadata types, so that changes stay scoped.

## Implementation Decisions

**New types:**

```go
// Session identity — always needed to reference a session
type SessionMeta struct {
    ID        string    `json:"id"`
    BrowserID string    `json:"browser_id"`
    ParentID  string    `json:"parent_id,omitempty"`
    Title     string    `json:"title"`
    Status    Status    `json:"status"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
    ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// Conversation — the chat data itself
type Conversation struct {
    Messages     []Message
    SystemPrompt string
    ActiveSkills []string
}

// Session config — per-session overrides
type SessionConfig struct {
    Workspace string
}
```

**Manager refactoring:** The `Manager` struct delegates to internal sub-stores:
- `metaStore map[string]*SessionMeta`
- `conversationStore map[string]*Conversation`
- `configStore map[string]*SessionConfig`

The `Manager` still exists as the public API (same methods), but internally dispatches to the appropriate sub-store. This keeps public call-site changes minimal.

**Approach:** Expand-contract. Phase 1 adds the sub-types and sub-stores alongside the existing `UISession`. Phase 2 migrates `Manager`'s internal layout to use sub-stores. Phase 3 optionally adds typed accessor methods on `Manager` (e.g., `GetConversation(id) *Conversation`) for callers that only need conversation data.

## Testing Decisions

- Existing tests continue to pass after each phase.
- New unit tests on the sub-store types verify that operations on one store don't affect the others (no field leakage).
- The JSON serialization of `UISession` for disk snapshots must remain stable (backward-compatible read path).

## Out of Scope

- Extracting to separate files is in scope; extracting to separate packages is out of scope (keep in `internal/session/`).
- Changing the snapshot JSON schema on disk.

## Further Notes

This is a structural refactoring of the session package's internals. The public API surface (`Manager` methods) changes minimally, if at all. The main benefit is internal clarity and narrower interfaces for LLMs reading the code.
