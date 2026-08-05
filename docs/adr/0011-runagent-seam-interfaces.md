# 0011 — HistoryManager and Confirmer seam interfaces for RunAgent

**Status**: Accepted

## Context

`RunAgent` (the agent turn loop in `internal/runner/loop/`) accepted a flat list of 12 parameters and drove two parallel history paths: a session-backed path (`*history.SessionManager`) for the browser UI and a direct `req.Messages` path for headless runs, via ~8 duplicated `if sessionMgr != nil` branches in the loop. The confirmation flow (user-path approval for the edit tool) was exercised only through browser E2E tests — `ConfirmationFunc` was always `nil` in unit tests, leaving the approve/deny path untested, and `loop_test.go` paid the cost of covering both paths in every combination.

## Decision

Introduce two internal seam interfaces in the runner package and refactor `RunAgent` to accept them.

```go
type HistoryManager interface {
    History() []message.EitriMessage
    AppendAssistant(content string, toolCalls []litellm.ToolUseBlock)
    AppendTool(toolCallID, content, rawContent string, isError bool)
    ReplaceHistory(messages []message.Message)   // used by auto-compaction
    RequestBased() bool
}

type Confirmer interface {
    Confirm(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error)
}
```

Two adapters each: session-backed vs request-backed history managers (UI component replay and quick replies stay on the UI session manager, outside the interface), and `RunService` (channel-based rendezvous with the API endpoint) vs a canned-result test stub.

`RunAgent` now takes `RunSpec` (transport/config: client, request, tools, SSE writer, turn/history caps) plus `RunOpts` (runtime/UI: history manager, confirmer, UI session manager, session ID, run ID, context window, crash-dump func, turn pointer, debug dir, turn completer, calibration store). UI-only concerns stay out of the seam interfaces.

## Considered Options

- **ConfirmationFunc callback instead of Confirmer interface**: less ceremony, but no named type for stubs — harder to test in isolation.
- **Merge uisessionMgr into HistoryManager**: rejected — component replay and quick replies are a genuinely different concern from conversation history.
- **Big-bang signature change**: faster but riskier; an incremental inside-out migration let each step be verified independently.

## Consequences

- The ~8 conditional branches disappear from `RunAgent`, replaced by uniform interface calls.
- The confirmation flow becomes unit-testable via a stub; the test matrix collapses (each adapter tested independently, no both-path combinations).
- Adding a third history backend (e.g. persistent database) means a new adapter, not more branching.
- Two new types/adapters add a little indirection — a reader consults two places for history operations instead of one.
