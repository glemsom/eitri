# Session-scoped prompt caching via prompt_cache_key

Use `prompt_cache_key` (a session-scoped cache tag on OpenAI-compatible chat-completions requests) to reduce repeated prefix processing in multi-turn agent workflows, gated behind a `SupportsPromptCache` profile flag.

**Status**: Accepted

## Context

Eitri's multi-turn agent loop sends every tool-call round through the same LLM provider. Each round includes the full system prompt, tool schemas, and conversation prefix — often thousands of tokens that are identical across turns with only the latest user/assistant exchange changing.

OpenAI-compatible providers and gateways (OpenAI, OpenCode Go, LiteLLM, etc.) support a `prompt_cache_key` field in `POST /v1/chat/completions`. When set, the server can reuse cached prefix processing across requests sharing the same key — reducing latency and input-token cost.

The ADK session ID is stable per conversation, making it the natural cache key value.

## Decision

1. **Field**: `openAIReq` includes a `prompt_cache_key` JSON field, serialised as `"prompt_cache_key": "<value>"` when non-empty.
2. **Value**: Set to the ADK session ID, plumbed through `OpenAIModel.SessionID` (set by `Manager.Run()` before each conversation loop).
3. **Gate**: Guarded by a `supportsPromptCache bool` on `profile`. Only providers that benefit from this feature enable it.
4. **Scope**: Enabled for `opencode_go` and `custom_openai` profiles. Not sent to `github_copilot`.
5. **Truncation**: Session IDs exceeding 64 characters are truncated to 64 to avoid provider-side key-length limits.

## Rationale

- **Multi-turn win**: The agent loop repeats system prompt + tool schemas verbatim every turn. A cache key lets the provider skip recomputing those prefix KV-cache entries on rounds 2+.
- **OpenCode Go compatibility**: The OpenCode Go gateway accepts `prompt_cache_key` and responds favourably — cache hit rate confirmed by the `pi-opencode-go-cache` Pi extension.
- **Safe default**: Profiles default to `supportsPromptCache: false`. Only opt-in for providers known to accept the field.
- **No Anthropic-style caching**: We do not add `cache_control` breakpoint markers on content parts — only the simple session-scoped key. This keeps the implementation provider-agnostic.

## Consequences

### Positive

- Reduced latency on turn 2+ in agent conversations routed through caching-capable gateways.
- Lower input-token cost when the provider discounts cached tokens (OpenAI, Anthropic via gateways).
- Transparent to users — no config surface change.

### Risks

- **Unknown-field rejection**: Most OpenAI-compatible endpoints ignore unknown JSON fields silently. If a provider rejects requests with unknown fields (strict schema validation), that provider must either be excluded from the feature or we must detect and strip the field per-provider.
- **Cache poisoning**: Session IDs are opaque and conversation-scoped. Truncation (64-char limit) reduces but does not eliminate collision risk. In practice, ADK session IDs are UUIDs, making collision negligible.
- **No observable benefit on non-caching providers**: Sending an ignored field is harmless but adds ~40 bytes to every request. Currently deemed negligible.

## References

- [OpenCode Go source](https://github.com/opencode-ai/opencode) — supports `prompt_cache_key` in chat-completions bodies.
- [Pi extension `pi-opencode-go-cache`](https://github.com/glemsom/pi-opencode-go-cache) — confirmed cache-hit behaviour in multi-turn sessions.
- ADR-0002 — OpenAI-compatible model abstraction (this ADR extends the transport described there).
- Issue #141 (T1) — Added `prompt_cache_key` field and profile flag.
- Issue #142 (T2) — Threaded ADK session ID into `OpenAIModel`.
- Issue #143 (T3) — Enabled `supportsPromptCache` for `opencode_go` and `custom_openai`.
