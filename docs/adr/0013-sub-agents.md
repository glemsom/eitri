# 0013 — Sub-agent support via delegate/collect tools

- **Status:** Accepted (amended ADR-0013a — sub-agents gain the `skill` tool, issue #1092)
- **Date:** 2025-07-17

## Context

Eitri has a single agent per session — one turn loop, one LLM instance, one sequence of tool calls. For complex multi-step workflows (e.g. "research three approaches in parallel and compare results"), the agent must do everything sequentially. This is slow and wastes context window on tasks that could be parallelised.

Other AI harnesses (OpenCode, among others) support **sub-agents** — subordinate agent loops that a parent agent can delegate tasks to. The sub-agent runs with its own turn loop, tool registry, and system prompt, then reports back.

## Decision

Add two new built-in tools: `delegate` and `collect`.

### Tool design

**`delegate(task: string, max_turns: int = 250)`** — non-blocking. Fires off a sub-agent in a background goroutine, returns a `taskID` immediately. The parent can fan out multiple delegates in one turn.

**`collect(task_ids: string[])`** — blocking. Waits for all listed sub-agents to complete (or for the parent context to cancel). Returns a structured JSON map keyed by task ID, each entry containing `status` (completed/error/cancelled), `result` (final assistant text), and `turn_count`.

### Sub-agent behaviour

- Same model, workspace, and command timeout as the parent
- Toolset: base tools + `skill` (no `delegate`, `collect`, `render_quick_replies` — no recursion, no UI-only tools). `skill` is a leaf capability: loading skill content does not enable delegation or any other parent-only tool. *(Amended 2026-08-05, issue #1092 — `skill` was previously excluded from the sub-agent toolset.)*
- System prompt: the sub-agent inherits the same prompt contract as a parent — the persona (the parent's active persona, or a persona named explicitly via `delegate`'s `persona` parameter), repository instructions, and skills catalog are assembled by the shared prompt builder. A persona with required skills therefore emits the `<required_skills>` directive, and the sub-agent loads each required skill via `skill()` on its first turn, exactly like a parent. The task description is appended as the final instruction: "{system prompt}\n\nYou are performing the following task: {task}". *(Amended 2026-08-05, issue #1092 — previously described as starting fresh with the default prompt.)*
- Uses `requestHistoryManager` for history (no session persistence by default)
- No confirmation prompts (confirmer is nil — confirmation-dependent operations return errors)

### Lifecycle and cancellation

- Sub-agents are tracked on `RunService` in a `sync.Map` keyed by `taskID`
- Parent cancellation cascades: cancelling the parent run cancels all child sub-agents
- `collect` returns partial results when context is cancelled (finished tasks return completed, unfinished return cancelled)

### UI visibility (optional)

Sub-agents can optionally create child sessions in the `SessionManager`. A `ParentID` field on `UISession` enables a sidebar tree view where child sessions appear nested under the parent. Each child session gets its own SSE stream for real-time tool card rendering. Child sessions are navigable by clicking — switching to a child shows its ongoing run.

Child sessions are created only when `delegate` is called from a parent that has a browser session (`uiSessionMgr != nil`).

### When to delegate: context management pattern

Sub-agents have their **own independent context window**, separate from the parent.
The parent's conversation history is **not** forwarded — the sub-agent starts
from the persona prompt contract (see above) plus the task description.

This makes delegation ideal for data-intensive work that would bloat the
parent's context window:

- **Fetching multiple URLs** — delegate parallel `web_fetch` calls to a sub-agent
  and collect summaries back. Raw page content stays in the sub-agent's context.
- **Reading large files** — delegate file analysis to a sub-agent so file contents
  don't accumulate in parent history.
- **Wide searches** — delegate `grep` across large trees to keep results
  contained.

Pattern:
```
1. delegate(task: "fetch URL1, URL2, URL3 and summarize each")
2. delegate(task: "read big_file.go and extract the AuthService type")
3. collect(task_1, task_2)  →  returns concise results
```

Parallel delegates can be fired in the same parent turn. `collect` blocks
until all are done.

### Non-goals

- Recursive sub-agents (sub-agents cannot spawn further sub-agents)
- Configurable model override per sub-agent
- Ad-hoc agent definitions (no agent config files — sub-agents are purely ad-hoc)
- Explicit per-task `cancel` tool (parent-level cancellation is sufficient)

## Considered options

| Decision | Chosen | Rejected alternatives |
|----------|--------|----------------------|
| **Sync vs async** | Non-blocking delegate + blocking collect | Fully blocking (can only spawn one per turn), fully async with result polling (more complex) |
| **Toolset** | Base + `skill` (no delegate/collect/quick_replies) | Read-only (too restrictive for real work), full with recursion (risk of infinite loops), base without skill (sub-agent cannot honor a persona's required skills) |
| **Sub-agent prompt** | Persona prompt contract (persona/repo-instructions/skills catalog) + task description | Inherit full parent conversation (bloated, distracts sub-agent), generic default only (no task context, drops persona-required skills) |
| **Recursion depth** | No nesting | Unlimited (risk of resource explosion), depth-capped (more complex) |
| **Cancellation** | Cascade from parent context | No cancel (sub-agents run to completion or max turns), explicit per-task `cancel` tool |
| **UI model** | Sidebar tree with parent/child session nesting | Inline within parent conversation (cluttered), no UI (invisible sub-agents) |
