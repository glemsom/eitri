# Sub-agent support via delegate/collect tools

## Problem Statement

Eitri's agent runs one thought process at a time — a single turn loop, one LLM call after another, sequential tool execution. When a task involves multiple independent sub-tasks ("research approach A, approach B, and approach C, then compare"), the agent must context-switch through each one sequentially. This wastes context window tokens on irrelevant details and takes longer wall-clock time than parallel execution would.

There is no way for the agent to delegate sub-tasks to subordinate agent loops that run independently and report back.

## Solution

Add two new built-in tools — `delegate` and `collect` — that let a parent agent spawn sub-agents to handle sub-tasks in parallel.

- **`delegate`** fires off a sub-agent in the background and returns a task ID immediately. The parent can call `delegate` multiple times in one turn to fan out work.
- **`collect`** blocks until all specified sub-agents complete, then returns their results as structured JSON.

Sub-agents run as full agent loops with the same tools, model, and workspace as the parent, but without the ability to spawn further sub-agents or activate skills. When the parent has a browser session, sub-agents optionally create child sessions visible in the sidebar tree, so the user can follow their progress in real time.

## User Stories

1. As a developer using Eitri, I want to delegate independent research tasks to sub-agents, so that they execute in parallel and I get results faster.
2. As a developer using Eitri, I want to call `delegate` multiple times in one turn, so that I can fan out work across several sub-agents without waiting between each.
3. As a developer using Eitri, I want `collect` to wait for all my sub-agents to finish, so that I receive all results in one structured response.
4. As a developer using Eitri, I want sub-agents to have access to the same tools (read, write, edit, bash, etc.), so that they can do real work without restrictions.
5. As a developer using Eitri, I want sub-agents to NOT be able to spawn further sub-agents, so that I don't accidentally create infinite recursion or resource explosions.
6. As a user watching a sub-agent in the browser UI, I want it to appear as a child session in the sidebar tree under the parent session, so that I can follow its progress in real time.
7. As a user watching a sub-agent, I want to click into its child session to see its tool cards and conversation, so that I can debug what it's doing.
8. As a developer using Eitri, I want cancelling the parent run to cancel all in-flight sub-agents, so that I don't leave orphan processes behind.
9. As a developer using Eitri, I want `collect` to return partial results when the parent context is cancelled, so that I can see whatever work completed before cancellation.
10. As a developer using Eitri, I want a configurable `max_turns` on `delegate`, so that I can control how many turns a sub-agent can spend on a task.
11. As a developer using Eitri, I want the sub-agent's system prompt to be the default prompt plus the task description, so that it stays focused without inheriting all the parent's context.

## Implementation Decisions

### Tool definitions

Two new tools in `internal/tool/`:

**`delegate`**:
- **Parameters**: `task` (string, required), `max_turns` (int, default 50)
- **Returns**: a JSON object with `task_id` (string) — a unique identifier for the sub-agent run
- **Non-blocking**: returns immediately after spawning the sub-agent goroutine

**`collect`**:
- **Parameters**: `task_ids` (array of string, required) — the task IDs to wait for
- **Returns**: a JSON object keyed by task ID, each entry containing `status` (string: "completed" | "error" | "cancelled"), `result` (string: final assistant text), and `turn_count` (int)
- **Blocking**: waits on channels until all tasks complete or the parent context is cancelled

### Sub-agent lifecycle

1. `RunService` gains a `sync.Map` (or mutex-guarded map) for tracking in-flight sub-agents by `taskID`
2. When `delegate` is called, `RunService.SpawnSubAgent()` creates:
   - A new `litellm.Request` with `requestHistoryManager`
   - A sub-agent loop goroutine running `RunAgent` with:
     - Same LLM service, workspace, cmd timeout as parent
     - Tool registry that excludes `delegate`, `collect`, `render_quick_replies`, `skill`
     - System prompt: default system prompt + `"\n\nYou are performing the following task: " + task`
     - `nil` confirmer (confirmation-dependent tool calls return errors)
   - An optional child `UISession` if the parent has a `uiSessionMgr` (see UI below)
3. `collect` blocks awaiting each task's completion channel, then assembles results
4. When parent context is cancelled, `cancel()` on each sub-agent's `RunState` is called
5. Sub-agent records are reaped after `collect` returns results (or after a TTL)

### RunService changes

`RunService` gains:

```go
type subAgentTask struct {
    ID        string
    RunState  *RunState  // for cancellation
    Done      chan struct{}
    Result    atomic.Value  // stores *taskResult once done
    SessionID string        // child session ID (empty if no UI)
}

func (s *RunService) SpawnSubAgent(ctx context.Context, parentSessionID, task string, maxTurns int, parentConfig RunConfig) (string, error)
func (s *RunService) CollectSubAgents(ctx context.Context, taskIDs []string) (map[string]SubAgentResult, error)
```

The `SpawnSubAgent` method will need access to:
- The same LLM config as the parent (provider, model, base URL, API key)
- The workspace path and command timeout
- The parent's `uiSessionMgr` (if any) to create child sessions
- A unique task ID generator (e.g. `task_<uuid>`)

### Tool wiring

In `run.go`'s tool registration block, after registering all standard tools, conditionally register `delegate` and `collect`:

- Parent runs get both tools
- Sub-agent runs get neither (the `SpawnSubAgent` method constructs a registry without them)

The tools will need a reference to `RunService` (or a narrow `SubAgentManager` interface) to call `SpawnSubAgent` and `CollectSubAgents`.

### UI integration

- Add `ParentID string` field to `UISession` struct
- When `SpawnSubAgent` is called and the parent has a `uiSessionMgr`, create a new `UISession` with `ParentID` set to the parent's session ID
- Child sessions appear in the sidebar tree with indentation/nesting
- Each child session gets its own SSE stream via `runstate.State`, wired through `RunState`
- Browser SSE event stream already handles multi-session events — no new infrastructure needed
- Switching to a child session in the sidebar navigates to its `/sessions/{childID}` URL, which shows the sub-agent's conversation streaming in real time

### Cancellation design

- `SpawnSubAgent` stores the child's `context.CancelFunc` on the `subAgentTask`
- The parent `RunState.Cancel` iterates all child tasks and calls their cancel funcs
- The sub-agent's `Done` channel is closed when its `RunAgent` returns
- `CollectSubAgents` uses a `select` on each task's `Done` channel and the parent context — if the parent context is cancelled, unfinished tasks return status "cancelled"

## Testing Decisions

A good test for this feature tests only external behaviour: given a task, does the sub-agent produce a result? Tests should not inspect the internal state of `RunService.subAgents` or other implementation details.

### Testing seams (highest first)

1. **RunService seam** (`internal/runner/service_test.go`) — Test `SpawnSubAgent` + `CollectSubAgents` lifecycle. Mock the LLM service to return known responses quickly. Verify:
   - Sub-agent completes and returns correct result
   - Context cancellation marks unfinished tasks as "cancelled"
   - Multiple sub-agents run concurrently and are all collected
   - Default `max_turns` is applied when not specified
   - Sub-agent gets the correct restricted tool registry

2. **Tool handler seam** (`internal/tool/delegate_test.go`, `internal/tool/collect_test.go`) — Test argument parsing, validation, and error handling:
   - `delegate` with empty task returns error to LLM
   - `delegate` returns a valid task ID
   - `collect` with unknown task ID returns error
   - `collect` with empty array returns empty result

3. **API/browser E2E seam** (`internal/api/browser_test.go`) — Optional, for UI child-session tests:
   - A parent session shows child sessions in the sidebar
   - Clicking a child session navigates to its conversation view
   - Sub-agent tool cards render in the child session

### Prior art

- `internal/tool/skill_test.go` — unit tests for tool handlers with mocked dependencies
- `internal/runner/service_test.go` — lifecycle tests for `RunService` with mocked LLM
- `internal/runner/loop_test.go` — agent loop tests with `requestHistoryManager`

## Out of Scope

- Recursive sub-agents (sub-agents spawning further sub-agents). Deferred to a future iteration with depth limits.
- Pre-configured sub-agent definitions (agent config files). All sub-agents are ad-hoc in v1.
- Model override per sub-agent. Sub-agents inherit the parent's model. Custom model selection deferred.
- Temperature or other LLM parameter override per sub-agent.
- Explicit `cancel(task_id)` tool. Parent-level cancellation cascade is sufficient for v1.
- Persistent sub-agent sessions across server restarts. Child sessions are in-memory only, matching v1 session semantics.
- Session compaction for child sessions (they accumulate tokens like any session).

## Further Notes

- Recursion is prevented at the tool registration level — sub-agents simply don't have `delegate`/`collect` in their registry. No runtime depth tracking needed.
- The `collect` tool's blocking behaviour means the parent agent will pause mid-conversation until results arrive. This is by design — the parent cannot make progress until it has the sub-agent results to reason about.
- Child sessions are cleaned up when the parent session is deleted (cascade delete in `SessionManager`).
