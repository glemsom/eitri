# OpenCode Go: Anthropic-route caching contract

> **Historical routing research.** Retained for source attribution. Current Eitri behavior and its provider-validation blocker are in [Prompt-cache current status](prompt-cache-status.md) and [OpenCode Go prompt-cache contract evidence](opencode-go-prompt-cache-contract.md).

**Scope.** This records what OpenCode Go itself publicly commits to, separated from
Anthropic Messages semantics. It does not treat an Anthropic-compatible endpoint as
proof that every Anthropic cache feature is implemented by the gateway. Sources were
checked 2026-10-05.

## Established facts

### Routing

OpenCode Go's [endpoint table](https://opencode.ai/docs/go/#endpoints) is the
primary routing authority:

| Model IDs currently documented | Endpoint / dialect |
| --- | --- |
| `qwen3.6-plus`, `qwen3.7-{max,plus}`, `qwen3.8-{max,flash}` | `https://opencode.ai/zen/go/v1/messages`; `@ai-sdk/anthropic` (Anthropic Messages) |
| `minimax-{m3,m2.7,m2.5}` | `.../v1/messages`; `@ai-sdk/anthropic` |
| Listed DeepSeek, GLM, Kimi, LongCat, MiMo, Hy, and Space Bunny models | `.../v1/chat/completions`; `@ai-sdk/openai-compatible` |

The table currently contains **no Union model or ID**. Therefore `union-*` must not
be inferred to use Messages from the present public contract; it may be a removed,
private, or differently named model. The table says model metadata is available at
[`GET /zen/go/v1/models`](https://opencode.ai/zen/go/v1/models), but its documented
endpoint metadata is not shown in the public documentation.

### Session identity, keys, and retention

Go requires a stable per-conversation session ID in the `x-opencode-session` header
so it can optimize **routing and prompt caching**. It also requires a specific client
User-Agent ([Go requirements](https://opencode.ai/docs/go/#how-it-works)). This is
the sole documented client-supplied cache identity; the Go page specifies neither a
request-body `prompt_cache_key` nor a cache-key derivation algorithm. Send the same
session ID for every turn in an Eitri **Session**, and do not reuse it for a distinct
Session.

No Go source documents prompt-cache TTL, eviction, retention, cache scope (account,
model, region, or session), invalidation, or whether the session header is a hard
cache partition rather than a routing/cache hint. The page's separate
[privacy retention table](https://opencode.ai/docs/go/#privacy) is data retention,
not a prompt-cache lifetime, and must not be used as one.

### `cache_control` and TTL

The gateway advertises Messages models as `@ai-sdk/anthropic`, but the Go documents
do **not** say it accepts Anthropic `cache_control`, which block kinds are allowed,
or which TTL values it honors. Nor do they mention the Anthropic prompt-caching beta
header. Thus there is no verified Go contract for putting `cache_control` (including
`ttl`) on system/message/tool blocks.

For comparison only, Anthropic's own [prompt-caching documentation](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
describes `cache_control: {"type":"ephemeral"}`, 5-minute default and 1-hour
cache durations, and cache-read/cache-creation usage. Those are Anthropic's API
semantics, **not evidence that OpenCode Go implements them**. Implementing the same
shape against Go needs an observed successful request and response contract from
OpenCode.

### Cache telemetry

Go publishes per-model prices for **Cached Read** and, for some models, **Cached
Write** (including Qwen and MiniMax variants) in its
[usage-limits table](https://opencode.ai/docs/go/#usage-limits). Its request-count
estimates also explicitly include cached-token volumes. This establishes that Go
accounts for caching and that read/write are economically distinct where a write
price is listed.

It does not document the JSON/SSE usage fields, whether they appear on both Messages
and Chat Completions streams, whether `input_tokens` includes cache reads/writes, or
whether zero/missing means no caching versus unavailable telemetry. Anthropic's
[Messages API](https://platform.claude.com/docs/en/api/messages) is relevant only
for the native Anthropic field names; it is not a Go response-schema guarantee.

## Implementation blockers / questions for OpenCode

1. **Route source of truth:** What are the exact current `union-*` IDs and endpoint?
   Does `GET /models` expose endpoint/dialect capability metadata, and what is its
   schema? A prefix classifier will silently misroute a renamed model.
2. **Messages cache acceptance:** For each Qwen and MiniMax Messages ID, does Go
   accept `cache_control` on system, message content, and tools? Which values and
   maximum breakpoints are accepted? Is an Anthropic beta/version header required?
3. **TTL and retention:** Does `ttl: "5m"` or `"1h"` work, does Go offer another
   TTL, and what is the actual cache lifetime/eviction behavior? Is cache lifetime
   related to session lifetime at all?
4. **Key semantics:** Is `x-opencode-session` mandatory on both `/v1/messages` and
   `/v1/chat/completions`; is it a cache key, a namespace, or only a routing hint?
   Is a body-level `prompt_cache_key` / `prompt_cache_retention` accepted, ignored,
   or rejected on Chat Completions?
5. **Telemetry schema:** Capture final successful SSE events from both dialects for
   a cold request and a repeated same-session request. Confirm exact cache read and
   write fields, their accounting relationship to input tokens, and their absence/
   zero semantics before displaying or aggregating cache telemetry.

## Practical conclusion

Route currently documented Qwen and MiniMax IDs to Messages and the documented
remaining compatible IDs to Chat Completions. Always preserve a stable
`x-opencode-session`. Do not send cache-control markers, select a TTL, assume a
24-hour retention, or claim cache hit/miss telemetry for Go until OpenCode confirms
the unanswered wire contract above.
