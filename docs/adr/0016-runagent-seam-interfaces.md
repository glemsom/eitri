# Extract HistoryManager and Confirmer seam interfaces from RunAgent

## Status

Accepted

## Context

`RunAgent` (the agent turn loop in `internal/runner/loop.go`) currently accepts a flat list of 12 parameters, including `sessionMgr *history.SessionManager` and `uisessionMgr *uisession.Manager`. The session manager drives two parallel history-management paths inside the loop:

```go
if sessionMgr != nil {
    sessionMgr.AppendAssistant(sessionID, content, toolCalls)
} else {
    req.Messages = append(req.Messages, litellm.Message{...})
}
```

There are **8 such `if sessionMgr != nil { ... } else { ... }` branches** in the loop, each duplicating the same intent through different types. The non-session path mutates `req.Messages` directly; the session path delegates to the session manager.

The confirmation flow (`confirm.go`) — which gates user-path approval for the edit tool — has **zero unit tests**. It is only exercised through browser E2E tests (chromedp). The `ConfirmationFunc` callback type is always passed as `nil` in existing unit tests, so the entire approve/deny path is untested.

Test volume reflects this friction: `loop_test.go` is 1949 lines (5.3× the 363-line `loop.go`), much of that overhead coming from having to cover both session and non-session paths.

### Existing dependencies behind the seam

| Loop operation | Session path | Non-session path |
|---|---|---|
| Load history | `sessionMgr.History(sessionID)` | `req.Messages` (already populated) |
| Append assistant | `sessionMgr.AppendAssistant(...)` | `req.Messages = append(...)` |
| Append tool result | `sessionMgr.AppendTool(...)` | `req.Messages = append(...)` |
| UI component replay | `uisessionMgr.AppendComponent(...)` | Skipped (no UI) |
| Quick replies | `uisessionMgr.SetQuickReplies(...)` | Skipped (no UI) |

## Decision

Introduce two internal seam interfaces in the `runner` package, and refactor `RunAgent` to accept them in place of the raw session-manager parameters.

### HistoryManager interface

```go
type HistoryManager interface {
    History(sessionID string) []litellm.Message
    AppendAssistant(sessionID string, content string, toolCalls []litellm.ToolCall)
    AppendTool(sessionID string, toolCallID string, content string, isError bool)
}
```

Two adapters:

- **`sessionHistoryManager`** — wraps `*history.SessionManager`, `*uisession.Manager`, and `sessionID`. Satisfies all three methods and also handles component replay/quick-replies for the browser UI path.
- **`requestHistoryManager`** — wraps `*litellm.Request`. Satisfies the three methods by mutating `req.Messages` directly. No-op for UI component operations.

### Confirmer interface

```go
type Confirmer interface {
    Confirm(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error)
}
```

Two adapters:

- **`RunService`** — the existing `confirmPath` method implements this interface (channel-based blocking, rendezvous with the API endpoint).
- **`testConfirmerStub`** — returns a canned result for unit tests.

### RunAgent signature

Before (12 params):

```go
func RunAgent(
    ctx context.Context, llm litellm.LLMService, req *litellm.Request,
    maxTurns int, maxHistory int, sseWriter *runstate.Writer,
    tools *tool.Registry, sessionMgr *history.SessionManager,
    uisessionMgr *uisession.Manager, sessionID string,
    confirmFn ConfirmationFunc, contextWindow int,
) error
```

After (10 params + 2 interface deps replace the 4 session/confirm params):

```go
func RunAgent(
    ctx context.Context, llm litellm.LLMService, req *litellm.Request,
    maxTurns int, maxHistory int, sseWriter *runstate.Writer,
    tools *tool.Registry, historyMgr HistoryManager,
    confirmer Confirmer, contextWindow int,
) error
```

### Migration strategy (inside-out)

1. Define `HistoryManager` and `Confirmer` interfaces. Write the two adapters each. Write unit tests for each adapter.
2. Rewrite `RunAgent`'s internals to use the adapters (constructed from existing params). No signature change. All existing tests pass.
3. Change `RunAgent`'s signature to take the interfaces. Update callers (`run.go`, `service_test.go`, `loop_test.go`). Delete the 8 conditional branches.
4. Add confirmation-flow tests using `testConfirmerStub`. Remove `confirm.go` (fold `Confirm` method onto `RunService`).

## Considered Options

- **ConfirmationFunc callback instead of Confirmer interface** — less ceremony, but inconsistent with HistoryManager pattern and harder to test in isolation (no named type for stub implementations). Rejected for consistency and testability.

- **Keep uisessionMgr as a separate parameter** (not merged into HistoryManager) — component replay and quick-replies are a genuinely different concern from conversation history. The session-backed HistoryManager adapter touches uisessionMgr internally, but the interface doesn't expose it. Clean separation.

- **Big-bang signature change** — faster but riskier. Incremental inside-out approach lets each step be verified independently.

## Consequences

- **Positive**: The 8 conditional branches disappear from `RunAgent`, replaced by uniform interface calls. Easier to reason about the loop's control flow.
- **Positive**: Confirmation flow becomes unit-testable via `testConfirmerStub`, eliminating the need for browser E2E tests to cover approve/deny paths.
- **Positive**: Test matrix collapses — `loop_test.go` no longer needs to test both session and non-session paths in every combination. Each adapter is tested independently.
- **Positive**: Adding a third history storage backend (e.g. persistent database) means a new adapter, not more branching in the loop.
- **Negative**: Two new types and adapters add indirection. A reader must look at two places to understand history operations instead of one. Worth the cost given the branch-count reduction.
- **Negative**: `RunService` gains a `Confirmer` interface implementation in addition to its existing responsibilities, but this is a thin delegation to the existing channel map.
