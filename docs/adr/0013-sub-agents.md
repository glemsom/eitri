# 0013 — Sub-agent support via delegate/collect tools

**Status**: Accepted (amended — sub-agents gained the `skill` tool, ADR-0013a / issue #1092)
**Date**: 2025-07-17

## Context

Eitri has a single agent per session — one turn loop, one LLM instance, one sequence of tool calls. Complex multi-step workflows ("research three approaches in parallel and compare results") run sequentially, which is slow and wastes the parent context window on parallelizable work. Other harnesses (OpenCode, among others) support sub-agents — subordinate agent loops with their own turn loop, tool registry, and system prompt that run a delegated task and report back.

## Decision

Add two built-in tools:

- **`delegate(task: string, max_turns: int = 250)`** — non-blocking. Fires a sub-agent in a background goroutine, returns a `taskID` immediately. The parent can fan out multiple delegates in one turn.
- **`collect(task_ids: string[])`** — blocking. Waits for all listed sub-agents (or parent context cancellation) and returns a structured JSON map keyed by task ID with `status` (completed/error/cancelled), `result` (final assistant text), and `turn_count`.

### Sub-agent behaviour

- Same model, workspace, and command timeout as the parent.
- Toolset: base tools + `skill` — no `delegate`/`collect`/`render_quick_replies` (no recursion, no UI-only tools). `skill` is a leaf capability: loading skill content enables no other parent-only tool.
- System prompt: the same prompt contract as a parent — persona (inherited, or named explicitly via `delegate`'s `persona` parameter), repository instructions, and skills catalog assembled by the shared prompt builder — with the task appended as the final instruction. A persona with required skills therefore emits the `<required_skills>` directive, and the sub-agent loads each required skill via `skill()` on its first turn, exactly like a parent.
- `requestHistoryManager` for history (no session persistence); no confirmation prompts (confirmer nil — confirmation-dependent operations error).

### Lifecycle and cancellation

Sub-agents are tracked on `RunService` in a `sync.Map` keyed by `taskID`. Parent cancellation cascades to all children; `collect` returns partial results on cancellation. Sub-agents can optionally create child sessions in the `SessionManager` (`ParentID` on `UISession`) for a sidebar tree view with per-child SSE streams — only when the parent has a browser session (`uiSessionMgr != nil`).

### When to delegate

Sub-agents have their own independent context window; parent history is not forwarded. Delegation suits data-intensive work that would bloat the parent: parallel `web_fetch` calls, large-file analysis, wide `grep` searches. Pattern: fire multiple `delegate` calls in one parent turn, then one blocking `collect`.

### Non-goals

Recursive sub-agents, configurable per-sub-agent model override, ad-hoc agent definition files, explicit per-task `cancel` tool (parent-level cancellation suffices).

## Considered options

| Decision | Chosen | Rejected alternatives |
|----------|--------|----------------------|
| Sync vs async | Non-blocking delegate + blocking collect | Fully blocking (one spawn per turn), fully async with result polling (more complex) |
| Toolset | Base + `skill` | Read-only (too restrictive), full with recursion (infinite loops), base without skill (persona required skills unloadable) |
| Sub-agent prompt | Persona prompt contract + task description | Inherit full parent conversation (bloated), generic default only (no task context) |
| Recursion depth | No nesting | Unlimited (resource explosion), depth-capped (more complex) |
| Cancellation | Cascade from parent context | No cancel, explicit per-task `cancel` tool |
| UI model | Sidebar tree with parent/child session nesting | Inline in parent conversation (cluttered), no UI (invisible sub-agents) |
