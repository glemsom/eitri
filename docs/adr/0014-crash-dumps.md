# 0014 — Crash dump directory for unexpected failures

- **Status:** Accepted
- **Date:** 2025-07-23

## Context

When Eitri hits an unexpected error (provider HTTP 400, agent loop panic, batch run failure), there is nowhere to capture diagnostic state. Users and developers must reproduce the error to debug it. The debug API (#554, #555, #557) exposes live state via HTTP, but that data disappears on process exit and requires a running server.

We need a persistent snapshot written at the moment of failure — a crash dump — alongside the debug API.

## Decision

Write structured crash dumps to `~/.eitri/crash-dump/`. One timestamped directory per crash.

### File structure

Each crash creates a directory named `crash-<ISO8601-timestamp>/` (colon replaced with dashes for cross-platform compatibility) containing:

| File | Contents |
|------|----------|
| `crash.json` | Error chain (`%+v`), version, sanitized config summary, runtime summary (up since, active run count, session count, trace count) |
| `sessions.json` | All UI sessions with full message history (same shape as `GET /api/debug/sessions`) |
| `traces.json` | All completed + in-flight HTTP traces from the debug recorder (same shape as `GET /api/debug/http`) |
| `goroutines.txt` | Pprof goroutine dump (`/debug/pprof/goroutine?debug=2`) — captures all goroutine stacks at crash moment |

### Trigger points

Three paths write a crash dump:

1. **Batch mode failure** — `main.go` after `BatchRun()` returns an error (non-cancelled). Before `os.Exit(1)`.
2. **UI run goroutine error** — `run.go`'s goroutine when it encounters a non-cancelled, non-max-turns error in the agent loop.
3. **Panic recovery** — a deferred `recover()` in `RunAgent` (the agent loop) catches panics, writes a dump, then re-panics.

### Trigger mechanism

`RunService` gains a `CrashDumpFunc func(err error, stack []byte)` field, set at construction time in `main.go`. The closure has access to all dependencies (config manager, version, debug recorder). The goroutine in `run.go` calls `s.CrashDumpFunc(err, debug.Stack())` when it hits a fatal error. The batch handler in `main.go` calls a local `writeCrashDump()` helper directly since there is no RunService context.

### Data assembly

The dump-writing function lives in a new `internal/debug/dump.go` file (within the existing `debug` package). It is a standalone function that takes all data as explicit parameters — no hidden imports from `runner` or `session`. Callers (the closure in `main.go`, or the batch error handler) assemble the snapshot from the services they already hold.

### Secrets

All config values are sanitized before writing — API keys and auth tokens replaced with `"***"`. The `crash.json` file uses the same `HasAPIKey` boolean approach as the debug API's config summary.

### Out of scope

- No auto-cleanup / retention policy (user manages disk).
- No inotify / watch-based detection.
- No network upload of crash dumps.
- No crash dump for expected control flow (max turns, user cancellation).
