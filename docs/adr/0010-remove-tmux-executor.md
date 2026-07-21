# Replace tmux-backed `bash` tool with direct `exec.Command` execution

## Status

Accepted

## Context

The `bash` tool currently delegates command execution to a per-session `TmuxExecutor`, which manages a long-running tmux session for each chat session. This was designed to provide:

- Persistent shell state (cwd, env, vars) across turns
- Background process lifetime management
- Process group tracking for cleanup

In practice, the agent loop rarely depends on cross-turn shell state. Most commands are self-contained (`cd dir && go build`) within a single tool call. Background processes can be managed by the LLM via shell job control (`&`, `kill`, process IDs) without tmux's interposition.

The tmux approach also carries costs:

- **Dependency**: requires `tmux` binary on `$PATH`, verified by `RunAudit()` at startup
- **Fragile output capture**: sentinel-marker polling via `tmux capture-pane` — race conditions, stale content, combined stdout+stderr
- **Complexity**: ~400 lines of tmux-specific code (session lifecycle, process group traversal, pane capture, death recovery)
- **No stderr isolation**: combined output stream, stderr always empty in `CommandResult`

A simpler `bash` tool using `os/exec` directly gives proper stdout/stderr separation, exit code handling, and removes an entire subsystem.

## Decision

Replace `TmuxExecutor` (and the entire `internal/executor/` package) with direct `exec.Command` execution inside `BashTool`.

### What changes

1. **`BashTool` creates `exec.Command` directly** — per-call process, no persistent session
2. **`BashTool` receives `workspace` and `timeout` as constructor params** (replaces `SessionManager` dependency)
3. **Output format includes stderr and exit code** — stdout and stderr captured separately via pipes, exit code from `cmd.ProcessState.ExitCode()`
4. **Per-command timeout via `context.WithTimeout`** — configurable from `command_timeout` config field (default 60s)
5. **Output capped at 128 KiB** — same limit as before
6. **Delete the entire `internal/executor/` package** — `executor.go`, `tmux.go`, `session.go`, `audit.go`, `mock.go`, and all test files
7. **Remove `SessionManager` from `RunService`** — `CloseSession()` only cancels run + closes history
8. **Remove `RunAudit()` from startup** — no startup dependency check
9. **Remove `ExecutorMgr` from `main.go`** — no `NewSessionManager`, no `StartTimeoutLoop`, no `cleanupRuntime`

### What stays

- `CommandResult`-like output struct stays conceptually (inlined into `BashTool` return)
- `BashTool` name + schema + dispatch unchanged
- `bash` tool description updated to reflect new behavior
- All config fields preserved: `command_timeout` stays active, `session_timeout` becomes dead config (kept for backward compat, documented as unused)
- All other tools (`read`, `write`, `edit`, `glob`, `grep`, etc.) unchanged — they already receive `workspace` directly

## Considered Options

- **Keep tmux, add a second `bash_simple` tool**: Adds surface area without removing complexity. LLM would need to choose between tools.
- **Hybrid: keep `SessionManager` but swap `TmuxExecutor` for `SimpleExecutor`**: Preserves abstraction that has no real value once only one implementation exists. Adds code for the interface without callers.
- **Pure delete (chosen)**: Remove the entire executor subsystem. `BashTool` calls `exec.Command` directly. Simplest.

## Consequences

- **Positive**: ~400 fewer lines of code. No tmux dependency. Proper stderr/stdout separation. Cleaner startup path. Exit codes directly from process state.
- **Positive**: Background processes become orphans (reparented to PID 1) when command returns. LLMs can manage this with shell job control or `nohup`.
- **Negative**: No persistent shell state across turns. Agent must use `&&` chains or set env vars explicitly. Trades systematic state for simpler code.
- **Negative**: No session death recovery. If a shell invocation fails (OOM, binary not found), next call starts fresh — same behavior as any CLI.
- **Negative**: `session_timeout` config field becomes dead. Kept in schema for backward compat.
