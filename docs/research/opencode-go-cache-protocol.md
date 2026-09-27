# OpenCode Zen prompt-cache protocol

> **Historical source snapshot — not a current endpoint claim.** This note describes Zen documentation and first-party TypeScript at pinned revision `b471c2b`, not a verified OpenCode Go wire contract or Eitri’s current routing. Its final assessment describes the policy that was replaced by the conservative implementation. See [Prompt-cache current status](prompt-cache-status.md) and [OpenCode Go prompt-cache contract evidence](opencode-go-prompt-cache-contract.md).

**Scope.** This concerns Eitri's `opencode-go` provider name, not an official Go
client: OpenCode's first-party implementation is TypeScript. Findings below are
limited to OpenCode's official documentation and source, pinned to
[`b471c2b`](https://github.com/anomalyco/opencode/tree/b471c2b4495747353af768fbf2e0790c9d820ce2).
“Documented acceptance” means the Zen endpoint table identifies that wire
protocol; it does **not** imply that an undocumented extension field is
accepted.

## Zen routes and documented dialects

Zen documents the model-specific routes below (the complete table is the
source of truth):

| Route / dialect | Documented model families | Cache-field conclusion |
| --- | --- | --- |
| `/zen/v1/responses` — native OpenAI Responses | GPT, Grok, Muse | This is the only documented OpenAI-native route. OpenCode lowers its internal `promptCacheKey` option to wire `prompt_cache_key` for the Responses protocol ([lowering, lines 458–470](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/protocols/openai-responses.ts#L458-L470)). It does **not** configure `prompt_cache_retention`. |
| `/zen/v1/messages` — Anthropic Messages | Claude; Qwen 3.8 Flash and listed Qwen 3.7/3.6/3.5 | The protocol accepts Anthropic-style `cache_control`, as implemented by OpenCode's Anthropic lowering. It has no OpenAI `prompt_cache_key` or `prompt_cache_retention` lowering. |
| `/zen/v1/chat/completions` — OpenAI-compatible Chat Completions | DeepSeek, MiniMax, GLM, Kimi, Qwen 3.8 Max, etc. | Zen documents the dialect, but OpenCode's public docs/source cited here do **not** document either `prompt_cache_key`, `prompt_cache_retention`, or Anthropic-style `cache_control`/`ttl` as accepted Zen Chat-Completions fields. Do not infer acceptance from the route being OpenAI-compatible. |

The official endpoint table establishes both the routes and the notable
exceptions: Qwen 3.8 Max is Chat Completions, while Qwen 3.8 Flash is Messages;
MiniMax is Chat Completions ([`zen.mdx`, lines 57–143](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/web/src/content/docs/zen.mdx#L57-L143)).
Its documented base is `/zen/v1`, and model discovery is
`GET /zen/v1/models` ([lines 151–157](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/web/src/content/docs/zen.mdx#L151-L157)).

## What OpenCode itself sends

### Cache key and retention

For every core session request, OpenCode derives a key from the session ID
(removing `ses_` only from `ses_` plus 64 lowercase hexadecimal characters) and
passes it as `providerOptions.openai.promptCacheKey`
([runner, lines 204–218](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/core/src/session/runner/llm.ts#L204-L218)).
The OpenAI Responses lowering serializes a nonempty option as
`prompt_cache_key` ([lines 458–470](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/protocols/openai-responses.ts#L458-L470)).

There is no request-side `prompt_cache_retention` policy or configuration in
that revision. Its only repository occurrence is a recorded Responses fixture's
*response* value of `"24h"`, not a request option
([fixture, line 28](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/test/fixtures/recordings/openai-responses/openai-responses-gpt-5-5-reasoning.json#L28)).
Thus no official OpenCode source establishes that Zen accepts a caller-selected
`prompt_cache_retention`, including `24h`.

### Anthropic cache controls, TTLs, and breakpoints

OpenCode's cache policy is `auto` by default: it retains manual hints and
marks the last tool, last system part, and latest user message. `none` merely
disables automatic placement ([policy, lines 18–110](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/cache-policy.ts#L18-L110)).
The configurable policy has tool/system/message selections and `ttlSeconds`
([schema, lines 245–275](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/schema/options.ts#L245-L275)).

On the Anthropic Messages wire, however, the supported emitted values are only:

* `{ "type": "ephemeral" }` — provider-default **5-minute** bucket; and
* `{ "type": "ephemeral", "ttl": "1h" }` — selected whenever
  `ttlSeconds >= 3600`.

`persistent` may be present in the internal hint type but is not emitted; no
other TTL, particularly `24h`, is emitted. See the bucket function
([`cache.ts`, lines 1–16](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/protocols/utils/cache.ts#L1-L16))
and Anthropic lowering ([lines 234–251](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/protocols/anthropic-messages.ts#L234-L251)).
It emits at most **four** explicit `cache_control` breakpoints, prioritizing
tools, system, then messages; excess markers are dropped with a warning
([lines 506–537](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/protocols/anthropic-messages.ts#L506-L537)).

### Session headers and usage fields

OpenCode sets `x-session-affinity` and `X-Session-Id` to the session ID on each
core-runner request, plus `x-parent-session-id` for a child session
([runner, lines 204–214](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/core/src/session/runner/llm.ts#L204-L214)).
This documents the client's headers, **not** an `X-Opencode-Session` Zen
contract; no first-party source found here documents that latter header.

The normalized usage contract exposes `inputTokens`, `outputTokens`,
`nonCachedInputTokens`, `cacheReadInputTokens`, `cacheWriteInputTokens`,
`reasoningTokens`, `totalTokens`, and raw `providerMetadata`
([events schema, lines 45–74](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/schema/events.ts#L45-L74)).
For Responses, cached input comes from `input_tokens_details.cached_tokens`;
reasoning comes from `output_tokens_details.reasoning_tokens`
([mapping, lines 503–520](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/llm/src/protocols/openai-responses.ts#L503-L520)).
Published session usage names these as `tokens.input`, `.output`, `.reasoning`,
`.cache.read`, and `.cache.write` ([publisher, lines 16–28](https://github.com/anomalyco/opencode/blob/b471c2b4495747353af768fbf2e0790c9d820ce2/packages/core/src/session/runner/publish-llm-event.ts#L16-L28)).

## Assessment of Eitri's current policy

Eitri defaults to `https://opencode.ai/zen/go/v1/chat/completions`
([`factory.go:11–15`](../../internal/provider/factory.go#L11-L15)), unconditionally
puts `prompt_cache_retention:"24h"` on its OpenCode Chat-Completions body, and
stamps `cache_control:{type:"ephemeral",ttl:"24h"}` at its static-prefix and
latest-message breakpoints ([`chatdialect.go:179–278`](../../internal/provider/chatdialect.go#L179-L278)).
It uses the session GUID as `prompt_cache_key` and sends `X-Opencode-Session`
([`engine.go:326–338`](../../internal/engine/engine.go#L326-L338),
[`openai.go:204–215`](../../internal/provider/openai.go#L204-L215)).

**Verdict: not documented as correct.** The 24-hour retention and 24-hour
marker TTL conflict with the only implemented Anthropic TTL buckets (default
5m or `1h`) and lack OpenCode evidence for Zen Chat Completions. The OpenCode
cache key is documented for the Responses lowering, but Eitri never selects
the documented `/responses` route; its static routing also misroutes current
Qwen 3.8 Max and MiniMax relative to Zen's endpoint table. A stable-prefix
breakpoint is a sensible cache shape, but the present *two `24h` markers,
retention field, key field, and session header* must be treated as
route/dialect-specific and unverified, not a documented universal Zen policy.
