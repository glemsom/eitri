# 0025 — Unified run engine across sub-agents and batch

**Status**: Accepted
**Date**: 2026-08-05

## Context

ADR-0024 unified *parent-run* preparation (UI, batch) behind `prepareRun`, but
sub-agent runs (`SpawnSubAgent`) continued to assemble their own tool registry
(`buildBaseToolRegistry`) and system prompt off the shared seam, and the
per-turn snapshot/compaction logic was duplicated between `subAgentSnapshotter`
and `batchTurnCompleter`. Batch and sub-agent both run the `loop.RunAgent`
engine with their own context window; keeping near-identical prep, snapshot,
and compaction paths in two places invites the same drift ADR-0024 set out to
kill. The unifying concept is a **run** — one isolated agent-loop execution
with its own context window — with sub-agent and batch as transports around a
single engine.

Note: despite the shared engine, the *delegation* asymmetry is intentional and
unchanged: batch is a full parent (can `delegate`); a delegated run is a leaf
(cannot). This is not "two names for one thing to unify," but the same
execution core with a transport/toolset discriminator.

## Decision

Make one run engine, consumed by both sub-agent and batch:

- **`prepareRun` is the single preparation seam for sub-agent + batch + UI.** It
  gains an `allowDelegate` option. A parent run (UI or batch) sets it `true`
  (registers `delegate`/`collect`); a delegated run sets it `false` (leaf
  registry — base tools + `skill`, no `delegate`/`collect`/`render_quick_replies`).
  `buildBaseToolRegistry` no longer assembles a separate parallel registry;
  recursion/leaf gating is a config value, not a registry omission.
- **One merged per-run snapshotter/compactor.** `subAgentSnapshotter` and
  `batchTurnCompleter` collapse into a single `runCompleter` that persists
  per-turn + terminal snapshots, writes the run timeline, and triggers the
  shared `autoCompactAfterTurn` step. The only parameterized bit is the
  *conversation source* — a small seam behind `loop.HistoryMgr` selecting the
  request-based or session-manager-backed history (ADR-0011 seam, unchanged).
- **One shared run-ID generator.** Session IDs for UI parent, batch, and
  sub-agent runs come from a single `runJobID` helper (generation + path-safety
  validation unified). The old `EITRI_BATCH_SESSION_ID` env override is removed;
  batch session IDs are auto-generated like the UI path. `scripts/agent-loop.sh`
  and `docs/agents/batch.md` update to reference the `session_id` the run reports
  back rather than a predetermined directory name.
- Result delivery stays transport-specific: `collect` returns per-task
  status/result/turn_count for in-process fan-out; batch returns a `(string,
  error)`. Both derive the final assistant text from the same
  extract-final-message helper.

## Considered options

| Decision | Chosen | Rejected alternative |
|----------|--------|----------------------|
| Engine boundary | In-process `prepareRun` + `loop.RunAgent`, thin wrappers | Re-spawning the `eitri -b` binary per delegate (process isolation; lost in-process `collect`/cascade-cancel/shared traces, and is not a goal — consolidation is about code, not fault containment) |
| History impl | Keep both behind the existing `loop.HistoryMgr` seam | Force sub-agents onto session-manager history (heavier than a leaf needs) |
| Sub-agent registry | `prepareRun` with `allowDelegate=false` (config value) | A separate `buildBaseToolRegistry` assembly (the duplication this ADR removes) |
| Snapshot/compaction | One merged `runCompleter`, conversation-source seam | Keep two near-identical snapshot/compact paths |
| Run ID | One shared `runJobID`; auto-generated everywhere; drop `EITRI_BATCH_SESSION_ID` | Keep env override for batch (named `sessions/<id>/` dir; rejected — an auto-generated ID, like the UI, is sufficient) |
| Result shape | Transport-specific (`collect` map vs `(string, error)`) | One forced result type (makes either fan-out or scalar awkward) |

## Consequences

Positive:

- One prep seam, one per-turn persistence/compaction path, one ID rule across
  UI, batch, and sub-agent runs — no drift surface between near-identical
  codepaths.
- Recursion becomes a config knob (`allow_delegate`), not an implicit registry
  difference; sub-agents remain leaf runs for v1.
- The shared `autoCompactAfterTurn` step, snapshot, and timeline behave
  identically for every run kind by construction.

Negative:

- `EITRI_BATCH_SESSION_ID` is removed (breaking for any caller relying on a
  predetermined batch session directory; the run's `session_id` is returned
  instead).
- The merged `runCompleter`/conversation-source seam is a new abstraction over
  two call sites; new run-kind knobs must live there, not in a wrapper.
- Terminology shifts: "sub-agent" and "batch run" are now spoken of as kinds of
  a single **run** concept (see CONTEXT.md glossary).
