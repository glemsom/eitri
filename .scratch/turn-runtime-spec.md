## Problem Statement

As a TUI maintainer, I need one coherent owner for a live Run's lifecycle and event projection. Today `TurnRuntime` accepts and drains events, while `TurnSession`, `Fold`, and `TurnFlow` separately own start/stop/commit and transcript projection. The shallow seam forces callers and tests to understand ownership across several modules, increasing the chance that cancellation, stale events, transcript state, and timeline finalization diverge.

## Solution

Deepen `TurnRuntime` into the single TUI-facing owner of one live Run. It will coordinate beginning, event acceptance, event projection, queued-event draining, stopping, and completion. `TurnSession`, `Fold`, and `TurnFlow` remain implementation details behind the runtime seam. The existing observable TUI behavior and domain contracts remain unchanged.

## User Stories

1. As a TUI user, I want starting a Run to initialize the user message, busy state, stream state, timeline, and cancellation context consistently, so that the TUI immediately reflects the active Run.
2. As a TUI user, I want provider stream deltas to appear in the TUI transcript in arrival order, so that streamed answers and reasoning remain coherent.
3. As a TUI user, I want tool starts and results to appear in the tool log and timeline, so that I can see what the Run is doing.
4. As a TUI user, I want stale events from an earlier Run ignored, so that an old Run cannot corrupt the current transcript.
5. As a TUI user, I want direct package-local events without a provider Run ID to continue working, so that local event delivery remains compatible.
6. As a TUI user, I want bursts of queued events drained before the next expensive render, so that fast streams remain responsive.
7. As a TUI user, I want stopping a Run to cancel only the active Run, so that later Runs cannot be affected by an old cancellation.
8. As a TUI user, I want a successful Run to reconcile streamed and final provider content into one assistant message, so that no duplicate answer appears.
9. As a TUI user, I want a stopped Run to preserve partial streamed content and mark it stopped, so that useful work is not lost.
10. As a TUI user, I want a failed Run to preserve any partial stream and display the failure consistently, so that the transcript explains what happened.
11. As a TUI user, I want the completed Run's timeline attached to its assistant message, so that its stream and tool history remain available after completion.
12. As a TUI user, I want post-completion tool observations to extend the correct assistant timeline, so that late results remain associated with their Run.
13. As a TUI user, I want thinking-enabled state to be applied consistently when a Run creates messages, so that transcript rendering matches settings.
14. As a TUI maintainer, I want Model to coordinate a Run through TurnRuntime rather than several lower-level modules, so that lifecycle knowledge has locality.
15. As a TUI maintainer, I want tests to drive one TurnRuntime through begin, observation, stop, completion, and failure, so that the highest seam verifies externally visible behavior.
16. As a TUI maintainer, I want internal implementation modules to remain replaceable without changing Model's Run coordination, so that future changes have leverage.
17. As a TUI maintainer, I want the runtime to preserve typed engine event semantics and stale Run-ID filtering, so that the existing event contract remains explicit.

## Implementation Decisions

- Deepen the existing `TurnRuntime` module rather than introducing a new module or public abstraction.
- Make TurnRuntime the TUI-facing owner of live Run lifecycle and event projection: begin, accept, observe, drain, stop, and commit.
- Keep `TurnSession`, `Fold`, and `TurnFlow` behind the TurnRuntime seam as private implementation collaborators. Model and transcript rendering code must not need to coordinate their ownership directly.
- Preserve the existing EventFeed behavior, including draining stale queued events before a new Run and non-blocking draining of ready events.
- Preserve Run-ID rules: the active engine-reported Run ID is accepted; stale nonzero IDs are rejected; direct zero-ID events remain accepted for package-local delivery and tests.
- Preserve the existing transcript contracts for busy state, stream cursor, reasoning fragments, tool logs, timeline ordering, stopped messages, errors, and final assistant reconciliation.
- Route TUI lifecycle callers through TurnRuntime wherever they currently reach into TurnSession or Fold for live Run behavior.
- Do not add a new adapter or generalized interface. There is one concrete implementation and no demonstrated variation requiring an adapter.
- Keep rendering concerns outside the runtime. TurnRuntime owns state transitions and projection; the TUI remains responsible for rendering the resulting transcript state.
- Keep provider, engine, session persistence, and domain terminology unchanged. This is an internal TUI architecture improvement.

## Testing Decisions

- Test only externally visible behavior through TurnRuntime and the transcript/timeline state it produces; do not assert private collaborator calls or internal field layout.
- Add or adapt table-driven tests covering begin, Run-ID acceptance, stale-event rejection, zero-ID events, stream/tool interleaving, queued-event draining, and no-event-feed operation.
- Add lifecycle tests covering successful completion, provider error, context cancellation/user stop, streamed and non-streamed answers, and repeated start/stop safety.
- Verify that committed timelines preserve arrival order and that post-completion tool observations attach to the correct assistant message.
- Verify thinking-enabled and thinking-disabled behavior, including tool busy pulses where that is externally observable.
- Verify that beginning a new Run clears stale queued events and resets live timeline state without altering committed history.
- Use existing TUI test conventions and prior art in `turn_runtime_test.go`, `turn_session_test.go`, `turn_flow_test.go`, `fold_test.go`, `turn_session_commit_test.go`, `turn_runtime_drain_test.go`, and stale-event/lifecycle tests.
- Run the complete Go test suite after focused tests pass.

## Out of Scope

- Changing provider event types or engine event semantics.
- Changing the persisted transcript format or session storage.
- Redesigning Bubble Tea rendering, transcript layout, composer behavior, or timeline visuals.
- Introducing multiple TurnRuntime implementations, a public interface, or a new adapter seam.
- Refactoring the engine Run loop or prompt/history policy.
- Changing cancellation, maximum-turn, context-overflow, or provider behavior beyond preserving the TUI's existing observable handling.
- Removing useful internal seams from the implementation if they improve focused tests; only caller-visible ownership is being consolidated.

## Further Notes

The deletion test supports this change: deleting the current TurnRuntime would cause event acceptance and draining to return to Model while lifecycle and projection calls remain split, so the current seam is shallow. The desired deep module concentrates those rules behind one interface and gives both callers and tests more leverage.
