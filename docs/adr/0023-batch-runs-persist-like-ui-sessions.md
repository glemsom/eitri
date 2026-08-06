# 0023 — Batch runs persist like UI sessions

**Status**: Accepted (amended — batch session IDs are auto-generated; the `EITRI_BATCH_SESSION_ID` env override was removed by ADR-0025)
**Date**: 2026-08-05

## Context

`eitri -b` was fire-and-forget: a headless batch run wrote zero data under
`~/.eitri/sessions/`. Its LLM calls fed the in-memory debug recorder (issue
#987), but the recorder's `OnComplete` → `SaveTraceAsync` persistence was a
silent no-op because `SaveTrace` skips sessions with no `session.json` on disk
(treated as permanently deleted). The failure path also exited via `os.Exit(1)`
without draining the async trace queue, dropping any queued traces. There was
no way to review what a batch run did — no conversation transcript, no tool
calls, no timing, no timeline.

## Decision

Batch runs leave the same reviewable trail on disk as UI sessions, using the
existing snapshot schema unchanged so report generation and on-demand session
load work with no consumer changes:

```
~/.eitri/sessions/<id>/
├── session.json              ← snapshot, rewritten each turn (same shape as UI)
├── timeline/<ts>.json        ← per-run timeline with termination reason
└── traces/<trace_id>.json    ← one HTTP trace per LLM call
```

### Schema reuse

Batch snapshots are plain `session.UISession` facades (id, prompt-derived
title, status, messages with tool calls, `system_prompt`, workspace,
timestamps) serialized by the existing `SnapshotSession` write path. No new
schema, no new report path, no new load path. A batch session on disk is
indistinguishable from a UI session to consumers: `jq`/`cat` inspection,
`POST /api/sessions/{id}/load`, and session report generation all work
unchanged.

### Per-turn completion seam (no agent-loop changes)

Snapshots are written after each complete agent turn via the existing
`loop.TurnCompleter` seam — the same callback the UI path uses. Batch mode
plugs in its own `batchTurnCompleter` that assembles the `UISession` facade
from the batch run's history manager (system prompt stripped from messages and
stored in `system_prompt`, matching UI snapshots). A first snapshot with
`running` status is written before the agent loop starts so `session.json`
exists before the first LLM call completes — otherwise the first turn's traces
would be dropped by `SaveTrace`'s deleted-session guard.

### Terminal snapshot and timeline on every exit path

After the agent loop returns, a terminal snapshot (status `idle` on success,
`error` on any failure) and a timeline are written unconditionally. The
timeline termination reason classifies the outcome like the UI: `completed`,
`cancelled` (context deadline/cancel), `max_turns`, or `error`, via the
RunState-free `persistRunTimeline` path (issue #1038).

### Trace queue drained before exit

The failure path in `cmd/eitri/main.go` now drains the async trace queue
(`persister.Flush`) before `os.Exit(1)`, mirroring the success path — queued
traces reach the batch session's `traces/` instead of being dropped. Combined
with the initial snapshot, every LLM call's trace is persisted.

### Session ID and title

The batch session ID is auto-generated (like the UI) by the unified run-job
ID helper — `runJobID` — which produces and path-safety-validates a
`batch-<hex>` ID (no path separators, no `..`) for the on-disk sessions
directory. The former `EITRI_BATCH_SESSION_ID` env override was removed by
ADR-0025 (issue #1108); callers locate a batch session by the run's auto-
generated `session_id`. Titles derive from the prompt with the same
`session.TitlePreview` rule as UI sessions (issue #1038), falling back to the
session ID for blank prompts.

### Retention and opt-out

Retention follows the existing policy unchanged: `session.json` is never
pruned; traces and timelines participate in the global 1 GiB cap. There is no
opt-out flag — `EITRI_DIR` already redirects the whole storage root.

## Consequences

Positive:

- Batch runs are auditable with the same `jq`/`cat` tools as UI sessions.
- No new schema or consumer changes; the snapshot stays the single source of truth.
- The agent loop is untouched — the turn-completion seam was already there.

Negative:

- Batch mode now writes to disk even for throwaway runs (bounded by the
  retention cap).
- A batch run that fails before any snapshot is written (e.g. config
  validation) still leaves nothing on disk — correct, there is nothing to review.
