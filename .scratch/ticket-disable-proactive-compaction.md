# Ticket: Disable proactive compaction before provider requests

## Type
behavior change

## Problem
Eitri currently may compact history before a provider request when previous prompt usage crosses a configured fraction of the context window. This breaks cache and performs lossy summarization before the provider has actually rejected the request.

## Goal
Normal runs should preserve full history and cache. Compaction should not run proactively based on token usage.

## Scope
- Remove or bypass the pre-request proactive compaction call equivalent to:

```go
messages, _ = e.maybeCompact(ctx, req, opts, messages, false, turn)
```

- Keep the forced compaction path available for provider context-overflow recovery.
- Internal names such as `maybeCompact` and `CompactionConfig` may remain for now.

## Likely files
- `internal/engine/engine.go`
- `internal/engine/compact.go`
- `internal/engine/compact_test.go`
- `internal/engine/events_test.go`

## Acceptance criteria
- No history summarization/eviction occurs merely because previous provider usage exceeded the old threshold.
- Existing forced compaction tests still pass or are updated to target overflow recovery.
- Add/update a test proving high previous prompt-token usage does not trigger compaction before the next request.
- `go test ./...` passes.

## Resolution
Resolved in this branch:
- Removed the pre-request proactive `maybeCompact(..., force=false, ...)` call from `RunAgent`.
- Kept the forced compaction path for provider context-overflow recovery.
- Updated engine tests so high prompt-token usage proves no summary request, compaction callback, or summary message is produced without provider overflow.
- Kept direct forced-compaction coverage for summary budget and compaction event behavior.
- Verified `go test ./...`.
