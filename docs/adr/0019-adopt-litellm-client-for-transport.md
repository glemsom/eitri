# 0019 — Adopt litellm.Client for all LLM transport — replace hand-rolled adapters

**Status**: Accepted

**Supersedes**: [ADR-0006](0006-remove-adk-litellm-transport.md) — ADR-0006 adopted litellm types but kept hand-rolled transport. This ADR closes that gap.

## Context

ADR-0006 committed Eitri to `github.com/voocel/litellm` as the LLM transport library, but what landed was only the type surface (`litellm.Block`, `litellm.Schema`, `litellm.Tool`). The actual HTTP transport was still hand-rolled in `internal/llm/`: 11 files / ~1400 lines of custom provider adapters, a parallel type system (`llm.Request`, `llm.Response`, `llm.Message`, …), custom SSE parsing, error classification, and a per-turn type-conversion tax (`toolDefsFromRegistry`). Eitri used only ~10% of litellm's surface while the library already provides `Provider` implementations for all backends, a `Client` with hooks, streaming helpers, error classification (`IsAuthError`, `IsRetryableError`), request validation/repair, and a `ToolUseAccumulator`.

## Decision

Replace `internal/llm/` entirely with `litellm.Client` and its provider subpackages. Delete the hand-rolled transport and the parallel type system.

### What changed

1. **Factory** (`internal/provider/litellm.go`): maps Eitri's `AdapterConfig` (provider ID, model, base URL, API key) to the corresponding litellm provider config (`openai.Config`, `anthropic.Config`, `openrouter.Config`); returns `*litellm.Client`.
2. **Agent loop** (`internal/runner/loop/`): consumes `litellm.Stream` with `Next()` and a type-switch on `litellm.Event`; tool-call delta aggregation via `litellm.NewToolUseAccumulator()`; retry via `litellm.IsRetryableError()`.
3. **Message type** (`internal/message/message.go`): `EitriMessage` wraps `litellm.Message` adding UI-only fields (`CreatedAt`, `Components`, `QuickReplies`). History and the `HistoryManager` interface use it.
4. **Debug logging**: per-adapter debug flags replaced by a `DebugHook` implementing `litellm.Hook`.

### What stays

| Concern | Package | Status |
|---------|---------|--------|
| Provider auth/discovery/profiles | `internal/provider/` | Unchanged |
| Session management | `internal/history/` | Types updated only |
| Agent loop orchestration | `internal/runner/loop/` | Rewired to litellm stream |
| Tool registry & dispatch | `internal/tool/` | Unchanged (already litellm types) |
| SSE broadcast | `internal/runstate/` | Unchanged |

## Considered Options

- **Keep the bridge as permanent abstraction**: clean seams for testing, but still pays the conversion tax — litellm's `Client` is already testable via its `Provider` interface (mock the provider).
- **Incremental adoption** (error types, retry, etc.): lower risk, but leaves the parallel type system and its ongoing conversion friction in place.
- **Extend litellm upstream with UI fields**: `CreatedAt`, `Components`, `QuickReplies` are Eitri-specific; upstreaming would pollute litellm's type system and add a dependency cycle. The wrapper is the correct boundary.

## Consequences

Positive:

- ~1400 lines deleted; single type system instead of two parallel trees; SSE edge cases and tool-call accumulation live upstream; more comprehensive error classification; adding providers (Gemini, Bedrock, Ollama) becomes a config change; hooks for observability; request validation/repair before the wire.

Risks:

- **Migration blast radius** — `EitriMessage` touches session, history, persistence, and the loop; mitigated by migrating as one compiler-verified PR.
- **Litellm API stability** — pin to a known-good version and upgrade deliberately.
- **Tool-call accumulation differences** — `ToolUseAccumulator` may produce slightly different `ToolUseBlock` values than the old accumulator; verified in tests with real provider traces.
