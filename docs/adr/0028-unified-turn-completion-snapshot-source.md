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

- **UI mode** — snapshot the UI session via `CopySession`, preserving the full
  UI-session fidelity (`ActiveSkills`, `ClosedAt`, `RenderedMessageIDs`) that
  the history-derived facade omits. Originally the UI source live-synced the
  run's live history into the UI conversation first; since issue #1241 the
  loop's session-backed history adapter reads and writes the canonical
  conversation store directly, so the UI conversation *is* the run's live
  history and the per-turn history→conversation copy is gone from this path.
- **Batch / sub-agent mode** — build the facade from history via
  `buildUISession` as before.

The strip-system-message invariant collapses into one place — the
history→conversation sync module `internal/message/sync.go`
(`message.SyncHistoryToConversation`, `message.StripLeadingSystemMessage`,
extracted from the completer by issue #1235) — used by `buildUISession`, the UI
live-sync, and `CompactSession`; the other hand-rolled sites are deleted.
(That module was itself deleted by issue #1242 once the old LLM-history store
was gone: the invariant now lives at the canonical-store boundaries — the
session-backed adapter's `ReplaceHistory` extracts the leading system message
into the session's separate `SystemPrompt` field, and `buildUISession` strips
it from the snapshot facade.) UI
parent runs keep their compaction SSE events (warning toast on failure,
`compaction_complete` on success), emitted by the unified completer when a
`RunState` is present (batch and sub-agent runs have none and only log). No
user-visible behavior change; a pure internal refactor guarded by the existing
snapshot / compaction / message-ordering test suite and the sync module's own
unit tests.

## Considered options

| Decision | Chosen | Rejected alternative |
|----------|--------|----------------------|
| UI per-turn completion | Unified `runCompleter` with a snapshot-source seam | Keep `RunService.OnTurnComplete` (two near-identical per-turn paths, four strip sites — the duplication this ADR removes) |
| UI snapshot source | Live-sync then `CopySession` | History-derived facade via `buildUISession` (drops `ActiveSkills`/`ClosedAt`/`RenderedMessageIDs` from UI snapshots) |
| Strip invariant | One sync module (`message.SyncHistoryToConversation` / `message.StripLeadingSystemMessage` in `internal/message/sync.go`, extracted by issue #1235; deleted with the old history store by issue #1242 — the invariant now lives at the canonical-store boundaries: the session-backed adapter's `ReplaceHistory` extraction and `buildUISession`'s facade strip) | Leave hand-rolled copies at each sync site (drift surface) |
| Compaction SSE events | Unified completer emits them when a `RunState` exists | Transport-specific notifier hook (extra plumbing for the same gate) |

## Consequences

Positive:

- One per-turn snapshot + auto-compaction + re-snapshot path across UI, batch,
  and sub-agent runs — no drift surface between near-identical codepaths.
- The strip-system-message invariant lives in exactly one place (the sync
  module, issue #1235; after issue #1242, at the canonical-store boundaries —
  the session-backed adapter's `ReplaceHistory` extraction and `buildUISession`'s
  facade strip).
- UI snapshots keep full `CopySession` fidelity, including mid-run snapshots
  that now reflect the just-completed turn's live-synced conversation.

Negative:

- `RunService` no longer exposes a public `OnTurnComplete`; callers (tests,
  future transports) must construct a `runCompleter` with the appropriate
  snapshot source instead.
- The UI per-turn snapshot now carries the post-sync conversation (it
  previously serialized the pre-sync facade), so on-disk `session.json`
  reflects the current turn rather than lagging one turn.
- The per-turn snapshot is gated on a disk persister (snapshot writes share
  `runCompleter.persist`). On persister-less configurations — the embedded run
  service browser E2E test servers use, and any host that disables
  persistence — the per-turn snapshot never runs, so `startRunWithConfig`
  restores a run-end fallback (`syncRunResultToUISession`) that appends the
  run's streamed reply to the UI conversation at completion, preserving
  components/quick replies attached to the last assistant message during tool
  execution and suffix-deduping against the already-committed final assistant
  message (issue #1217). Since issue #1241 the loop commits each turn to the
  canonical conversation store directly, so components/quick replies attached
  during tool execution stay attached to the tool-calling assistant message
  even when a persister is present (the old live-sync copy dropped them from
  the on-disk conversation).
