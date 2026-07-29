# 0019 — Adopt litellm.Client for all LLM transport — replace hand-rolled adapters

**Status**: Accepted

**Supersedes**: [ADR-0006](0006-remove-adk-litellm-transport.md) — ADR 0006 adopted litellm types but kept hand-rolled transport. This ADR closes that gap.

## Context

[ADR-0006](0006-remove-adk-litellm-transport.md) committed Eitri to using `github.com/voocel/litellm` as the LLM transport library. What landed was only the *type surface* — `litellm.Block`, `litellm.Schema`, `litellm.Tool` — used by the tool package. The actual HTTP transport was still hand-rolled in `internal/llm/`:

- 11 files, ~1400 lines of custom provider adapters (OpenAI, Anthropic, OpenRouter, GitHub Copilot)
- Parallel type system: `llm.Request`, `llm.Response`, `llm.Message`, `llm.ToolDef`, `llm.Usage`, `llm.StreamEvent`
- Custom SSE parsing, error classification, HTTP client, and streaming channels
- Type conversion tax: `toolDefsFromRegistry` converts `[]litellm.Tool` → `[]llm.ToolDef` at every turn

Eitri uses only ~10% of litellm's surface area despite importing it. The library provides ready-made `Provider` implementations for all our backends, a `Client` with hooks, streaming helpers (`StreamText`, `StreamWith`, `Handle`), error classification (`IsAuthError`, `IsRetryableError`), request validation/repair, and a `ToolUseAccumulator`.

## Decision

Replace `internal/llm/` entirely with `litellm.Client` and its provider subpackages. Delete the hand-rolled transport and the parallel type system.

### What changes

1. **Factory** (`internal/provider/litellm.go`): Maps Eitri's `AdapterConfig` (provider ID, model, base URL, API key) to the corresponding litellm provider config (`openai.Config`, `anthropic.Config`, `openrouter.Config`). Returns `*litellm.Client`.

2. **Bridge** (temporary, in `internal/llm/bridge.go`): Wraps `*litellm.Client` behind the existing `llm.LLMService` interface so the existing agent loop is unaffected during rollout. Deleted when the loop migrates.

3. **Agent loop** (`internal/runner/loop/`): Migrates from `<-chan llm.StreamEvent` to `litellm.Stream` with `Next()` and a type-switch on `litellm.Event`. Uses `litellm.NewToolUseAccumulator()` for tool call delta aggregation. Retry uses `litellm.IsRetryableError()`.

4. **Message type** (`internal/message/message.go`): Defines `EitriMessage` — a thin wrapper around `litellm.Message` that adds `CreatedAt time.Time`, `Components []ComponentData`, and `QuickReplies []string`. These UI-only fields have no litellm equivalent and need a home.

5. **History/Session** (`internal/history/`): Migrates from `[]llm.Message` to `[]EitriMessage`. `HistoryManager` interface returns `[]EitriMessage`.

6. **Debug logging**: Replaces per-adapter `DebugPrompt`/`DebugRequest`/`DebugLLMDir` flags with a `DebugHook` implementing `litellm.Hook`.

### What stays

| Concern | Package | Status |
|---------|---------|--------|
| Provider auth/discovery/profiles | `internal/provider/` | Unchanged |
| Session management | `internal/history/` | Updated types only |
| Agent loop orchestration | `internal/runner/loop/` | Rewired to litellm stream |
| Tool registry & dispatch | `internal/tool/` | Unchanged (already uses litellm types) |
| SSE broadcast | `internal/runstate/` | Unchanged |
| UI message metadata | `internal/message/` | New wrapper, same fields |

### Files to delete (11 files, ~1400 lines)

- `internal/llm/openai.go` — replaced by `provider/openai`
- `internal/llm/anthropic.go` — replaced by `provider/anthropic`
- `internal/llm/openrouter.go` — replaced by `provider/openrouter`
- `internal/llm/github_copilot.go` — replaced by `provider/openai` with headers
- `internal/llm/common.go` — HTTP helpers, error classification → litellm equivalents
- `internal/llm/wire_types.go` — wire types internal to provider subpackages
- `internal/llm/sse_scanner.go` — SSE parsing built into litellm providers
- `internal/llm/service.go` — `LLMService` interface replaced by `litellm.Client`
- `internal/llm/factory.go` — replaced by `internal/provider/litellm.go`
- `internal/llm/types.go` — `Request`, `Response`, `ToolCall`, `ToolDef`, `Usage`, `StreamEvent` → litellm types
- `internal/llm/message.go` — `Message` → `EitriMessage` wrapper
- `internal/runner/loop/stream.go` — `drainStream` replaced by inline `Next()` loop
- `internal/runner/loop/tool_call.go` — conversion code deleted (litellm types used directly)

Also: `internal/llm/doc.go`, `internal/llm/litellm_test.go`, `internal/llm/litellm_internal_test.go` — deleted or gutted.

## Considered Options

### Keep the bridge as permanent abstraction

Pro: Clean seams for testing. Con: Still pays the conversion tax. Litellm's `Client` is already testable via its `Provider` interface (mock the provider). Extra abstraction buys nothing.

### Incremental adoption (error types, retry, etc.)

Lower risk, but leaves the parallel type system in place. The type duplication causes ongoing friction (conversion functions, two message types in memory). Given that the bridge approach lets us verify correctness before deleting old code, the incremental path has worse risk/reward.

### Extend litellm upstream with UI fields

`CreatedAt`, `Components`, `QuickReplies` are Eitri-specific concerns. Upstreaming them would pollute litellm's type system and add a dependency cycle (litellm shouldn't know about UI components). The wrapper is the correct boundary.

## Consequences

### Positive

- **~1400 lines deleted** — replaces hand-rolled transport with library calls
- **Single type system** — one `Request`/`Response`/`Message`/`Tool` instead of two parallel trees
- **SSE bugs live upstream** — edge cases in streaming, tool-call delta aggregation, thinking/reasoning handled by litellm
- **Error classification** — litellm's `IsAuthError`, `IsRetryableError`, etc. are more comprehensive than our hand-rolled `classifyHTTPError`
- **Provider extensibility** — adding Gemini, Bedrock, Ollama becomes a config change, not a new adapter
- **Hooks** — observability hooks for metrics, tracing, audit logging via `litellm.Hook`
- **Request validation/repair** — litellm catches malformed histories before they reach the wire

### Risks

- **Bridge correctness** — the bridge must faithfully map litellm events to the existing `llm.StreamEvent` channel shape. Mitigation: integration tests against the bridge before cutting over.
- **Type migration blast radius** — `EitriMessage` touches session, history, persistence, and the loop. Mitigation: do it as one compiler-verified PR so the full migration is validated at once.
- **Litellm API stability** — pin to v1.8.8 and upgrade deliberately.
- **Tool call accumulation** — litellm's `ToolUseAccumulator` may produce slightly different `ToolUseBlock` values than our accumulator. Mitigation: compare outputs in tests with real provider traces.

## Migration Plan

### Phase A — Bridge (safe, revertible)
1. Create `internal/provider/litellm.go`: factory mapping `AdapterConfig` → litellm provider configs
2. Create `internal/llm/bridge.go`: `litellmBridge` implementing `LLMService` via `*litellm.Client`
3. Create `DebugHook` implementing `litellm.Hook`
4. Swap `NewLLMService()` → `newBridgeService()` in call site
5. Old adapter files become dead code — can be deleted or left as safety net

### Phase B — Loop migration
6. Rewrite agent loop to use `client.Stream()` directly with type-switch on `litellm.Event`
7. Use `NewToolUseAccumulator()` for tool call delta aggregation
8. Replace retry check with `litellm.IsRetryableError()`
9. Delete `drainStream()`, `stream.go`, old stream infrastructure

### Phase C — Type elimination (one compiler-verified PR)
10. Define `EitriMessage` wrapper type in `internal/message/`
11. Migrate `session.go` to store `[]EitriMessage`
12. Migrate `HistoryManager` interface + both adapters
13. Migrate agent loop to use `EitriMessage` / `litellm.Request` / `litellm.ToolUseBlock`
14. Delete `types.go`, `message.go`, `service.go`, tool_call conversion code, all remaining dead files
