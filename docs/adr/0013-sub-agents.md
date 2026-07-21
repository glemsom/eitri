# 0013 — Sub-agent support via delegate/collect tools

- **Status:** Accepted
- **Date:** 2025-07-17

## Context

Eitri has a single agent per session — one turn loop, one LLM instance, one sequence of tool calls. For complex multi-step workflows (e.g. "research three approaches in parallel and compare results"), the agent must do everything sequentially. This is slow and wastes context window on tasks that could be parallelised.

Other AI harnesses (OpenCode, among others) support **sub-agents** — subordinate agent loops that a parent agent can delegate tasks to. The sub-agent runs with its own turn loop, tool registry, and system prompt, then reports back.

## Decision

Add two new built-in tools: `delegate` and `collect`.

### Tool design

**`delegate(task: string, max_turns: int = 50)`** — non-blocking. Fires off a sub-agent in a background goroutine, returns a `taskID` immediately. The parent can fan out multiple delegates in one turn.

**`collect(task_ids: string[])`** — blocking. Waits for all listed sub-agents to complete (or for the parent context to cancel). Returns a structured JSON map keyed by task ID, each entry containing `status` (completed/error/cancelled), `result` (final assistant text), and `turn_count`.

### Sub-agent behaviour

- Same model, workspace, and command timeout as the parent
- Full toolset minus `delegate`, `collect`, `render_quick_replies`, and `skill` (no recursion, no UI-only tools)
- System prompt: default prompt + "You are performing the following task: {task}"
- Uses `requestHistoryManager` for history (no session persistence by default)
- No confirmation prompts (confirmer is nil — confirmation-dependent operations return errors)

### Lifecycle and cancellation

- Sub-agents are tracked on `RunService` in a `sync.Map` keyed by `taskID`
- Parent cancellation cascades: cancelling the parent run cancels all child sub-agents
- `collect` returns partial results when context is cancelled (finished tasks return completed, unfinished return cancelled)

### UI visibility (optional, v1)

Sub-agents can optionally create child sessions in the `SessionManager`. A `ParentID` field on `UISession` enables a sidebar tree view where child sessions appear nested under the parent. Each child session gets its own SSE stream for real-time tool card rendering. Child sessions are navigable by clicking — switching to a child shows its ongoing run.

Child sessions are created only when `delegate` is called from a parent that has a browser session (`uiSessionMgr != nil`).

### Non-goals for v1

- Recursive sub-agents (sub-agents cannot spawn further sub-agents)
- Configurable model override per sub-agent
- Ad-hoc agent definitions (no agent config files — sub-agents are purely ad-hoc)
- Explicit per-task `cancel` tool (parent-level cancellation is sufficient)

## Considered options

| Decision | Chosen | Rejected alternatives |
|----------|--------|----------------------|
| **Sync vs async** | Non-blocking delegate + blocking collect | Fully blocking (can only spawn one per turn), fully async with result polling (more complex) |
| **Toolset** | Full minus delegate/collect/quick_replies/skill | Read-only (too restrictive for real work), full with recursion (risk of infinite loops) |
| **Sub-agent prompt** | Default + task description | Inherit full parent prompt (bloated, distracts sub-agent), generic default only (no task context) |
| **Recursion depth** | No nesting (v1) | Unlimited (risk of resource explosion), depth-capped (more complex, deferrable) |
| **Cancellation** | Cascade from parent context | No cancel (sub-agents run to completion or max turns), explicit per-task `cancel` tool |
| **UI model** | Sidebar tree with parent/child session nesting | Inline within parent conversation (cluttered), no UI (invisible sub-agents) |
