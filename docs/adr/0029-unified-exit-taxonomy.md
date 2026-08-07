# 0029 — Unified exit taxonomy across UI, batch, and sub-agent runs

**Status**: Accepted
**Date**: 2026-08-07

## Context

Run termination is classified three times in the runner: inline in the UI
goroutine (`run.go`), in `batchTermination` (`batch.go`), and inline in the
sub-agent goroutine (`subagent.go`). Each site performs the same
snapshot / broadcast / append / persist-timeline sequence on exit, but the
three classifications drifted: after the unified run-completer landed
(issue #1107), batch runs ended cancelled and max-turns executions with a
`StatusError` terminal snapshot while UI and sub-agent runs ended them
`StatusIdle`. Only true failures should persist an error status; cancelled
and max-turns runs are deliberate stops, not failures.

A single classification point removes the drift at its source: one function
decides, for any finished run, both the terminal snapshot status and the
timeline termination reason, and every transport feeds its exit through it.

## Decision

Introduce one exit-taxonomy function, `classifyRunExit(runErr, runCtx)`,
that classifies a finished run into:

- **Terminal snapshot status** — `StatusIdle` on completion, cancellation,
  and max-turns; `StatusError` on true failure.
- **Timeline termination reason** — `completed`, `cancelled`, `max_turns`,
  or `error`, with the user-facing message each carries (the cancelled
  message is shared; max-turns uses `uixt.MaxTurnsMessage`).

The classification order matches the pre-unification exit paths: a run whose
run context was cancelled is reported as `cancelled` even when the returned
error is a different (wrapped) error; otherwise `max_turns` is recognized
before falling through to `error`.

All three transports — the UI exit path (`run.go`), batch (`batch.go`), and
sub-agent (`subagent.go`) — derive their terminal status and termination from
this one function. Transports keep only their transport-specific exit work
(message appending, SSE events, crash dumps, sub-agent record status) and no
longer classify termination themselves; `batchTermination` and the
reason-only status helper are removed.

**Behavior change**: batch cancelled and max-turns runs now persist a
`StatusIdle` terminal snapshot instead of `StatusError`, matching UI and
sub-agent semantics. The batch CLI exit code is unaffected — it is driven by
`BatchRun`'s returned error, not the snapshot status.

## Consequences

- One code path decides every terminal snapshot status and timeline reason;
  future transports (or new termination reasons) cannot silently diverge.
- Batch persisted review trails for cancelled / max-turns runs no longer
  claim failure in their terminal `session.json` status, while the timeline
  still records the precise reason.
- The pre-existing distinction between "ended" (`StatusIdle`) and "failed"
  (`StatusError`) now holds consistently across UI, batch, and sub-agent
  runs (ADR-0025).
