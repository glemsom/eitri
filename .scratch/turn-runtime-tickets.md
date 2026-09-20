# TurnRuntime implementation tickets

Parent spec: `.scratch/turn-runtime-spec.md`

Recommended order: T1 → T2 → T3 → T4 → T5. T6 is cleanup and can follow T3.

## T1 — Establish TurnRuntime as the complete lifecycle seam ✅ RESOLVED

**Depends on:** none

Move all TUI-facing live Run lifecycle coordination behind `TurnRuntime`. Preserve the existing observable behavior while routing begin, stop, completion, thinking configuration, and live timeline access through the runtime. Remove direct lifecycle ownership knowledge from Model and other callers where it is currently duplicated.

**Acceptance criteria**

- Model coordinates a live Run through TurnRuntime rather than directly coordinating TurnSession lifecycle operations.
- Begin, Stop, and Commit behavior remains unchanged for success, provider failure, and user stop.
- Thinking-enabled configuration remains consistent for messages created during a Run.
- No new public/generalized interface or adapter is introduced.
- Existing tests pass, with focused tests added at the TurnRuntime seam for lifecycle behavior.

**Test focus**

Use externally visible transcript, busy state, cancellation, and completion behavior. Adapt existing TurnSession lifecycle tests where their assertions describe the runtime contract.

## T2 — Move event projection ownership behind TurnRuntime ✅ RESOLVED

**Depends on:** T1

Make TurnRuntime the only caller-facing owner of stream and tool observation projection. Keep TurnSession, Fold, and TurnFlow as internal collaborators, but prevent Model and transcript lifecycle code from needing to know which collaborator applies an observation.

**Acceptance criteria**

- Stream observations reach the transcript through TurnRuntime.
- Tool observations update the tool log and timeline through TurnRuntime.
- Stream/tool interleaving preserves arrival order.
- Busy pulses and thinking-disabled progress behavior remain unchanged.
- Rendering code consumes resulting transcript state and does not coordinate Fold directly.
- Focused tests drive observations through TurnRuntime rather than constructing Fold for caller-level behavior.

**Test focus**

Cover answer/reasoning deltas, tool start/result pairs, tool-before-stream ordering, and timeline snapshots through one runtime.

## T3 — Make Run-ID acceptance and queued-event draining a runtime contract ✅ RESOLVED

**Depends on:** T2

Consolidate event acceptance, stale-event rejection, turn-start handling, and ready-event draining as one TurnRuntime behavior. Preserve the existing EventFeed semantics and direct zero-ID compatibility.

**Acceptance criteria**

- A new Begin drains stale queued events before the next Run starts.
- Turn-start events establish the active nonzero Run ID.
- Events with stale nonzero IDs are ignored.
- Events with zero Run ID remain accepted for package-local delivery and tests.
- DrainReady applies every currently queued event in arrival order without blocking.
- A runtime without an EventFeed remains supported.

**Test focus**

Use table-driven tests for accepted/rejected IDs, turn-start transitions, stale queues, burst draining, no-feed operation, and stream/tool mixed queues.

## T4 — Verify completion, stop, error, and timeline reconciliation at the runtime seam
 ✅ RESOLVED
**Depends on:** T2

Move the highest-value lifecycle regression coverage to TurnRuntime. Verify that the deepened seam preserves all transcript and timeline outcomes across terminal states.

**Acceptance criteria**

- Successful streamed output reconciles into one committed assistant message.
- Successful non-streamed output creates one assistant message.
- Stopped streamed output preserves partial content and marks it stopped.
- Provider errors preserve partial output and append the expected failure content.
- Completed timelines attach to the correct assistant message in arrival order.
- Post-completion tool observations attach to the correct assistant timeline.
- A later Run cannot be affected by cancellation or state from an earlier Run.

**Test focus**

Use table-driven runtime tests for terminal outcomes and timeline assertions. Do not assert private TurnSession, Fold, or TurnFlow fields.

## T5 — Remove shallow caller seams and document the resulting ownership
 ✅ RESOLVED

**Depends on:** T1, T2, T3, T4

Complete the architectural consolidation after behavior is covered. Remove redundant caller-visible forwarding or direct collaborator access, update comments and package documentation, and ensure the ownership model is clear to maintainers.

**Acceptance criteria**

- TUI callers have one live Run seam: TurnRuntime.
- Direct caller-level construction/use of Fold is limited to implementation or focused internal tests where appropriate.
- Transcript rendering does not depend on lifecycle ownership details.
- Comments describe TurnRuntime as the owner rather than documenting a temporary façade.
- `ARCHITECTURE.md` remains accurate if ownership descriptions changed.
- Full test suite passes.

**Test focus**

Run focused TUI tests, then `go test ./...`. Confirm no behavior-only regressions in snapshots, rendering, stop handling, or timeline tests.

## T6 — Retire or narrow obsolete direct tests and helpers
 ✅ RESOLVED

**Depends on:** T4

Review tests and helpers that test the old shallow ownership split. Keep low-level tests where they protect meaningful internal invariants, but move caller-behavior coverage to TurnRuntime and remove redundant setup that exposes implementation ownership.

**Acceptance criteria**

- Tests assert external behavior at the highest available seam.
- TurnFlow/Fold tests remain only where their internal invariants are valuable and not duplicated by runtime tests.
- No test requires direct access to private lifecycle state merely to verify user-visible behavior.
- Test names and comments use Run, TurnRuntime, transcript, and timeline terminology consistently.
