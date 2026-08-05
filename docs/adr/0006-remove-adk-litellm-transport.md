# 0006 — Remove Google ADK, adopt litellm transport + custom agent loop

**Status**: Superseded by [ADR-0019](0019-adopt-litellm-client-for-transport.md)

ADR-0019 replaced the hand-rolled litellm transport with `litellm.Client`; the transport items below are superseded. The non-transport decisions remain live.

## Context

Eitri initially adopted Google ADK Go SDK (`google.golang.org/adk/v2`) for agent orchestration, tool dispatch, session management, and runner lifecycle. Over time the ADK surface became a burden: it pinned `google.golang.org/genai` and ~20+ transitive Google-cloud modules with breaking-upgrade risk, Eitri used none of ADK's multi-agent/streaming/evaluation features, and ADK's `model.LLM` interface plus `[]*genai.Content` session representation leaked into provider, runner, and API modules.

## Decision

1. **Remove all ADK dependencies** — `google.golang.org/adk/v2`, `google.golang.org/genai`, `google.golang.org/api`, and their transitive tree.
2. **Keep Eitri's provider auth/discovery/profile layer** (`internal/provider/`): GitHub OAuth device flow, token refresh, structured `provider_auth` state, model discovery/filtering, UI-facing provider metadata. Only the chat HTTP transport was replaced.
3. **Session management lives in `internal/history/`** — sliding window (last 50 exchanges) via a `SessionManager` API (`AppendUser`, `AppendAssistant`, `AppendTool`); loss-on-restart semantics.
4. **Synchronous agent loop** — `LLM → parse tool_calls → execute → feed back → LLM`, running in a goroutine with concurrent SSE fan-out to the UI. No state machine, no multi-agent routing.
5. **Tool definitions** in `internal/tool/` — explicit Go structs with `JSONSchema()` methods; `SchemaOf[T]` reduces boilerplate.
6. **OpenCode Go model routing** — prefix-based (`qwen*`/`minimax*` → Anthropic `/v1/messages`, everything else → OpenAI `/v1/chat/completions`); unknown prefixes default to OpenAI-compatible.

## Superseded details

The litellm transport adoption (provider adapters, GitHub Copilot adapter via `Config.Headers`, retry/error classification, SSE parsing) and its migration plan were superseded by ADR-0019, which adopted `litellm.Client` directly.
