# 0028 — Fold UI per-turn completion into the unified runCompleter

**Status**: Accepted
**Date**: 2026-08-08

## Context

ADR-0025 merged the batch turn-completer and the sub-agent snapshotter into a
single `runCompleter` serving batch and sub-agent runs. UI runs kept their own
per-turn completion (`RunService.OnTurnComplete`), so the per-turn cycle —
snapshot the run conversation, run the shared auto-compaction step, re-snapshot
when compaction rewrites the history — still existed twice with different code,
and the "strip leading system message" invariant (the system prompt lives in a
separate field, never in `Messages`) was hand-rolled at four sites: the UI
live-history sync, the UI compaction sync, `CompactSession`, and
`buildUISession`. Every per-turn path is the same persistence/compaction logic
differing only in where the snapshot facade comes from.

## Decision

`RunService` stops implementing `loop.TurnCompleter`; all three run transports
— UI, batch, and sub-agent — complete turns through the single `runCompleter`
path. The merged `runCompleter` gains a per-transport **snapshot-source seam**:

- **UI mode** — live-sync the UI conversation from the run's live history,
  then snapshot the UI session via `CopySession`, preserving the full
  UI-session fidelity (`ActiveSkills`, `ClosedAt`, `RenderedMessageIDs`) that
  the history-derived facade omits.
- **Batch / sub-agent mode** — build the facade from history via
  `buildUISession` as before.

The strip-system-message invariant collapses into one place — the
`stripLeadingSystemMessage` helper inside the completer — used by
`buildUISession`, the UI live-sync, and `CompactSession`; the other hand-rolled
sites are deleted. UI parent runs keep their compaction SSE events (warning
toast on failure, `compaction_complete` on success), emitted by the unified
completer when a `RunState` is present (batch and sub-agent runs have none and
only log). No user-visible behavior change; a pure internal refactor guarded
by the existing snapshot / compaction / message-ordering test suite.

## Considered options

| Decision | Chosen | Rejected alternative |
|----------|--------|----------------------|
| UI per-turn completion | Unified `runCompleter` with a snapshot-source seam | Keep `RunService.OnTurnComplete` (two near-identical per-turn paths, four strip sites — the duplication this ADR removes) |
| UI snapshot source | Live-sync then `CopySession` | History-derived facade via `buildUISession` (drops `ActiveSkills`/`ClosedAt`/`RenderedMessageIDs` from UI snapshots) |
| Strip invariant | One `stripLeadingSystemMessage` helper in the completer | Leave hand-rolled copies at each sync site (drift surface) |
| Compaction SSE events | Unified completer emits them when a `RunState` exists | Transport-specific notifier hook (extra plumbing for the same gate) |

## Consequences

Positive:

- One per-turn snapshot + auto-compaction + re-snapshot path across UI, batch,
  and sub-agent runs — no drift surface between near-identical codepaths.
- The strip-system-message invariant lives in exactly one place.
- UI snapshots keep full `CopySession` fidelity, including mid-run snapshots
  that now reflect the just-completed turn's live-synced conversation.

Negative:

- `RunService` no longer exposes a public `OnTurnComplete`; callers (tests,
  future transports) must construct a `runCompleter` with the appropriate
  snapshot source instead.
- The UI per-turn snapshot now carries the post-sync conversation (it
  previously serialized the pre-sync facade), so on-disk `session.json`
  reflects the current turn rather than lagging one turn.
