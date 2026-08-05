# 0016 — Session persistence via JSON snapshots

**Status**: Accepted (updated for single-file snapshot)

## Context

Eitri stores all session state (chat messages, components, active skills, LLM
conversation history, HTTP traces) in memory. A server restart loses everything.
The crash dump system (`internal/debug/`) proved that serializing the full session
state to JSON is straightforward — the models already have JSON tags. We needed a
persistence layer that survives restarts and lets users inspect past sessions.

## Decision

Persist session state as a single `session.json` snapshot file per session under
`~/.eitri/sessions/<id>/`, written atomically after each complete agent turn.
Key design choices:

### File layout

```
~/.eitri/
├── config.json
└── sessions/
    └── <session_id>/
        ├── session.json              ← single snapshot (atomically overwritten)
        ├── timeline/                 ← condensed run timelines
        │   └── 2024-01-15T10-30-00.json
        └── traces/
            └── trace_1.json
```

The `history/` directory has been removed. Session messages now carry all
LLM-oriented fields (tool calls, tool_call_id) so the snapshot is the single
source of truth for both UI rendering and LLM replay.

### Snapshot trigger

Snapshots are written synchronously after each complete agent turn (assistant
message appended + all tool results processed). Traces are written individually
on LLM provider call completion. A final flush on SIGTERM/SIGINT catches any
unsaved data.

### Write path

- `session.json` is written atomically via temp-file + rename in the same
  directory (`.<name>.tmp-*` → `session.json`), preventing partial writes.
- No timestamped chain files or symlinks — a single overwritten file per session.

### Retention

The global 1 GiB cap applies to the `sessions/` directory. Pruning evicts the
oldest timeline and trace files across all sessions when the cap is exceeded.
The `session.json` file is never pruned.

### Restore

On startup, the latest snapshot for each session is read back from disk.
All restored sessions are forced to `idle` status — a snapshot represents a
completed turn, so there is no half-running state. LLM conversation histories
are derived from the session snapshot messages.

**Session decoupling (v0.1.5+):** Sessions are no longer hydrated into the
in-memory session manager on startup. Only LLM conversation histories and HTTP
traces are restored into their respective managers. The session manager starts
empty — the user creates new sessions via the + button in the sidebar. Disk
snapshots persist solely for troubleshooting and historical debugging; they
are not used to reconstruct active UI state after a restart.

### Separation of concerns

A new `internal/persist/` package owns all I/O. The session manager, history
manager, and trace recorder remain pure in-memory with no filesystem
dependencies. The Persister is wired in at the `cmd/eitri/main.go` level and
injected into the `RunService` and `Recorder` callbacks.

### Message consolidation

`session.Message` now embeds LLM-oriented fields (`ToolCallID`, `ToolCalls`)
so that one message type serves both UI rendering and LLM replay. The `llm`
package continues to use its own wire type for API communication; conversion
happens at the adapter layer.

## Consequences

Positive:

- Snapshot browsing is straightforward (`cat`, `jq` work without Eitri running).
- The 1 GiB cap prevents unbounded disk growth.
- Crash dump reuse: the same serialization models, directory conventions, and
  JSON tags are shared.
- Session decoupling keeps the session manager pure in-memory — no restore
  logic to maintain, no stale state on startup.
- Single-file format eliminates timestamped file accumulation and symlink
  complexity.
- History is embedded in the session snapshot, removing the `history/` directory
  and the `HistorySchema` type (kept for backward-compatible reads).

Negative:

- Sessions no longer survive server restarts in the UI — the user starts with
  an empty sidebar on each restart and must create new sessions manually.
- Full-JSON snapshots are not incremental — each snapshot duplicates the entire
  session state. At ~10-50 KB per snapshot this is negligible, but sessions with
  thousands of turns or large file diffs embedded in messages could grow.
- No queryability — you cannot SQL-query past sessions. File system tools
  (`grep`, `jq`) suffice for the current use case but may not scale.

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
