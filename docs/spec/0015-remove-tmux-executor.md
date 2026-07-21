# Spec: Remove tmux executor, simplify `bash` tool

## Problem Statement

Eitri's `bash` tool is backed by a tmux session per chat session — a complex subsystem (~400 lines) that manages persistent shell state, sentinel-marker output capture, process group tracking, and session death recovery. This dependency on `tmux` binary and the polling-based output capture (merged stdout+stderr) adds fragility and complexity without proportional benefit: most agent commands are self-contained, and the LLM can manage background processes via shell job control directly. The separate stderr pipe is also lost, making error diagnosis harder for the LLM.

## Solution

Replace the tmux-backed `bash` tool with direct `os/exec` command execution. Each `bash` call spawns a fresh shell process, captures stdout/stderr separately, returns exit codes natively, and applies a configurable timeout. Remove the entire `internal/executor/` package and all its supporting infrastructure (audit, session manager, mock, process group kill logic).

## User Stories

1. As an Eitri user, I want the `bash` tool to work without `tmux` installed on my system, so that I have fewer dependencies to manage.

2. As an Eitri user, I want the `bash` tool to return stderr output separately from stdout, so that the LLM can distinguish error messages from normal output.

3. As an Eitri user, I want the `bash` tool to return the command's exit code, so that the LLM can detect command failures reliably.

4. As an Eitri operator, I want Eitri to start up without a `tmux` binary audit check, so that startup is faster and has fewer failure modes.

5. As an Eitri user, I want each `bash` call to time out independently via a configurable `command_timeout` setting, so that long-running commands don't hang the agent.

6. As an Eitri user, I want the output from `bash` calls to be capped at 128 KiB (same as today), so that overly verbose commands don't waste context window.

7. As an Eitri developer, I want the `bash` tool to have no per-session state or session manager dependency, so that the codebase is simpler to reason about and test.

8. As an Eitri user, I want the LLM to still be able to run background processes (e.g. `npm run dev &`) by managing them via shell job control (`$!`, `kill`), so that dev-server workflows remain possible.

9. As an Eitri user, I want the `bash` tool's JSON schema and dispatch to remain unchanged, so that existing model integrations continue to work.

10. As an Eitri user, I want the `bash` tool description updated to reflect that each call runs in a fresh shell, so that the LLM knows to chain commands with `&&` or set env vars explicitly.

## Implementation Decisions

### Module changes

- **`internal/tool/bash.go`**: Rewrite `BashTool`. Constructor takes `workspace string` and `timeout time.Duration`. `Call()` creates `exec.CommandContext(ctx, "bash", "-c", command)` with `cmd.Dir = workspace`. Captures stdout/stderr via pipes (`cmd.StdoutPipe()`, `cmd.StderrPipe()`). Applies timeout via `context.WithTimeout`. Returns text blocks with stdout, stderr, exit code, and truncation/timeout markers. Max 128 KiB output per call.

- **`internal/executor/`**: Delete entire package (9 files).

- **`internal/runner/service.go`**: Remove `sessionMgr *executor.SessionManager` field from `RunService`. Remove `RunServiceDeps.SessionManager`. `CloseSession()` only cancels active run + closes history session. `NewRunService()` no longer receives executor dependency.

- **`internal/runner/run.go`**: `StartRun()` creates `BashTool` with `NewBashTool(s.workspace, s.cmdTimeout)` directly. `s.workspace` stored as field on `RunService` (already available from startup config).

- **`cmd/eitri/main.go`**: Remove `executor.RunAudit()`. Remove `executor.NewSessionManager(...)`, `executorMgr.StartTimeoutLoop(...)`. Remove `executorMgr` from `cleanupRuntime()`. `NewRunService()` called with updated deps.

- **`internal/config/manager.go`**: Keep both `command_timeout` and `session_timeout` in config schema. `command_timeout` remains active. `session_timeout` becomes dead config (no runtime effect), kept for backward compatibility.

### Testing

- Delete `internal/executor/executor_test.go`, `session_test.go`, `tmux_test.go`, `audit_test.go`
- Update `internal/api/run_test.go`: `newManagedTestServerWithRuns` and variants no longer create executor. `TestDeleteSessionCancelsActiveRunClosesExecutorAndClosesStream` removes executor-creation/replacement checks, keeps run-cancelled and stream-closed assertions.
- Update `internal/runner/service_test.go`: no longer imports executor package.
- Update `internal/tool/bash_test.go`: update constructor call, add tests for stdout/stderr/exit code output format, timeout behavior.

### Output format

```
<stdout>
<stderr>
[exit code N]
[command timed out]      # if applicable
[output truncated]       # if >= 128 KiB
```

Non-zero `[exit code N]` triggers `isError=true` so LLM sees a tool error. Timeout also sets `isError=true`. Truncation does not set `isError` (output is still useful).

## Testing Decisions

- **Good test**: verify the `Call()` method returns correct output given a known command. Not testing `exec.Command` internals — test the tool at its public seam.
- **Module tested**: `internal/tool/bash_test.go` — unit tests for stdout/stderr capture, exit code reporting, timeout behavior, truncation.
- **Prior art**: existing `tool` tests (`tool_test.go`, `read_test.go`, `grep_test.go`) test tools via `Call()` with known inputs and assert output text blocks.
- **Integration**: `internal/api/run_test.go` exercises the full agent+tool path. Tests updated to match new constructor signatures.
- **No new test seams**: `BashTool` tested via `tool.Registry.Dispatch()` same as all other tools.

## Out of Scope

- Changing the config schema (fields kept)
- Changing other tools (read, write, edit, glob, grep, skill, render tools)
- Adding sandboxing or containerization
- Changing the LLM transport layer
- Modifying the frontend or SSE protocol

## Further Notes

- `session_timeout` config field remains in UI and JSON schema but has no runtime effect. Marked as "not used in current runtime" in config documentation.
- The ADR-0015 supersedes the earlier tmux-based executor ADRs.
