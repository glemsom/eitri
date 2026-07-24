# 0016 — Session persistence via JSON snapshots

**Status:** Accepted

## Context

Eitri stores all session state (chat messages, components, active skills, LLM
conversation history, HTTP traces) in memory. A server restart loses everything.
The crash dump system (`internal/debug/`) proved that serializing the full session
state to JSON is straightforward — the models already have JSON tags. We needed a
persistence layer that survives restarts and lets users inspect past sessions.

## Decision

Persist session state as timestamped JSON snapshot files under `~/.eitri/`,
written after each complete agent turn. Key design choices:

### File layout

```
~/.eitri/
├── config.json
├── sessions/
│   └── <session_id>/
│       ├── session.json              ← symlink to latest timestamped file
│       ├── 2024-01-15T10-30-00.json  ← full UISession state
│       ├── 2024-01-15T10-31-15.json
│       └── traces/
│           ├── trace_1.json          ← one file per HTTP trace
│           └── trace_2.json
└── history/
    └── <session_id>/
        ├── history.json              ← symlink to latest timestamped file
        ├── 2024-01-15T10-30-00.json  ← LLM conversation (canonical schema)
        └── 2024-01-15T10-31-15.json
```

### Snapshot trigger

Snapshots are written synchronously after each complete agent turn (assistant
message appended + all tool results processed). Traces are written individually
on LLM provider call completion. A final flush on SIGTERM/SIGINT catches any
unsaved data.

### Write path

- Timestamped files use ISO8601 with colons replaced by dashes for cross-platform
  filesystem compatibility (same convention as crash dumps).
- The latest snapshot is pointed to by a symlink (`session.json → 2024-...json`),
  updated atomically via temp-file + rename.
- History files use a canonical JSON schema with a `version` field (start at 1)
  rather than dumping internal structs directly, giving freedom to change the
  internal LLM message type without breaking disk format.

### Retention

Keep all snapshots, with a global 1 GiB cap. Pruning evicts the oldest
timestamped files across all sessions when the cap is exceeded. The latest
snapshot for any session with active data is never pruned.

### Restore

On startup, the latest snapshot for each session is read back and used to
hydrate the in-memory session manager, history manager, and trace recorder.
Restored sessions are always marked `idle` — a snapshot represents a completed
turn, so there is no half-running state to resume.

### Separation of concerns

A new `internal/persist/` package owns all I/O. The session manager, history
manager, and trace recorder remain pure in-memory with no filesystem
dependencies. The Persister is wired in at the `cmd/eitri/main.go` level and
injected into the `RunService` and `Recorder` callbacks.

## Consequences

Positive:

- Sessions survive server restarts — the user sees their previous conversations
  when they reopen the browser.
- Timestamped snapshots provide a browsable history of session evolution
  (`ls`, `cat`, `jq` work without Eitri running).
- The 1 GiB cap prevents unbounded disk growth while keeping all snapshots for
  any realistically-sized session.
- Crash dump reuse: the same serialization models, directory conventions, and
  JSON tags are shared.

Negative:

- Full-JSON snapshots are not incremental — each snapshot duplicates the entire
  session state. At ~10-50 KB per snapshot this is negligible, but sessions with
  thousands of turns or large file diffs embedded in messages could grow.
- No queryability — you cannot SQL-query past sessions. File system tools
  (`grep`, `jq`) suffice for the current use case but may not scale.
- The symlink approach is Unix-centric; Windows junction points would need
  separate handling if Eitri ever targets Windows.

## Alternatives considered

### SQLite

Rejected as overengineered for v1. Adds a C dependency (or `modernc.org/sqlite`
at ~2 MB in the binary), schema migrations, and more code. The queryability
benefit is not yet needed — file system tools cover the debugging use case.

### Append-only JSON-lines WAL

Rejected because it requires defining an event schema, implementing replay
logic, and compaction. The complexity exceeds the benefit for a single-binary
tool where full snapshots are already cheap.

### Poll-based persistence (goroutine polling for dirty state)

Rejected in favour of explicit calls from the run loop. Polling adds latency,
complexity, and race potential for no benefit — the run loop already knows
exactly when a turn completes.

### Store only UI sessions, not LLM history

Rejected because the LLM conversation history (system prompt + tool calls +
results) is essential for debugging prompt issues and understanding exactly
what was sent to the provider. The two representations diverged during
development — history has message types (tool, tool_call) that UI messages
fold into assistant message components.
