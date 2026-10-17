# Declare prompt caching only for the wire the operator chooses

Eitri carries the full prompt-cache emission machinery behind `ProviderCapability` bits on `Request.Capabilities`. `ChatCompletionsDialect.Build` emits `prompt_cache_key`, `prompt_cache_retention` and `prompt_cache_options`, and stamps head/tail breakpoints, only when the matching bit is set; `AnthropicDialect.marshalAnthropicBody` passes the Anthropic-style `cache_control` marker through only when `ProviderCapabilityCacheControl` is set; `ResponsesDialect.marshalResponsesBody` gates `prompt_cache_key` and the `prompt_cache_options`/`prompt_cache_breakpoint` pair the same way. No shipped adapter declares any of the four cache bits, so none of the machinery reaches a wire: the byte-stable static prompt head that the engine painstakingly preserves buys nothing directly, and caching is left to undocumented gateway behaviour. We decided the matrix. The OpenCode Go provider — in both the Chat Completions and the Anthropic Messages dialect it fronts — honours none of Eitri's prompt-cache capabilities, and so does GitHub Copilot in both its dialects. A custom OpenAI-compatible endpoint honours exactly the capabilities of the wire the operator chose: `PromptCacheKey` and `PromptCacheRetention` on a Chat Completions endpoint, `CacheControl` on an Anthropic Messages endpoint. The `PromptCacheOptions`/`PromptCacheBreakpoint` pair is honoured by no shipped endpoint and is deleted, along with the OpenCode Go breakpoint stamping, whose comment claims behaviour the wire contract forbids.

| Provider | Dialect(s) it fronts | Capabilities honoured on the wire | Capabilities to declare |
| --- | --- | --- | --- |
| opencode-go | Chat Completions (`POST /v1/chat/completions`) | none | none |
| opencode-go | Anthropic Messages (`POST /v1/messages`) | none | none |
| github-copilot | Chat Completions (`POST /chat/completions`) | none | none |
| github-copilot | Responses (`POST /responses`) | none | none |
| custom-openai | Chat Completions | `PromptCacheKey`, `PromptCacheRetention` | `PromptCacheKey \| PromptCacheRetention` |
| custom-openai | Anthropic Messages | `CacheControl` | `CacheControl` |

## OpenCode Go honours none of the four capabilities

`NewOpenCodeGo` (internal/provider/openai.go) fronts two wires behind one adapter: a Chat Completions endpoint built by `normalizeChatCompletionsURL`, and a sibling `/messages` endpoint derived from it. `openCodeAnthropicModel` routes the documented qwen3.x and minimax IDs to the Anthropic Messages wire; every other model rides Chat Completions. The client declares exactly one capability, `ProviderCapabilityOpenCodeSessionHeader`, so every cache bit is absent on both wires.

The binding evidence is `internal/provider/opencode_go_cache_contract_test.go`. `TestOpenCodeGoPromptCacheContract` drives a cold turn and a repeated turn against the live gateway over both dialects, records the outbound request, and asserts — through `jsonContainsKey` — that the body contains none of `prompt_cache_key`, `prompt_cache_retention`, or `cache_control`; it names them "unverified … extension". The same test pins `X-Opencode-Session` constant across turns and requires a terminal usage blob, so the gateway stays attributed and observable without any client-side cache marker. That is not an incidental gap but a stated contract: OpenCode Go tunes caching itself.

The comment on `eitriUserAgent` in openai.go says why: the gateway requires a specific identity header so its "abuse monitoring and prompt-cache tuning can attribute traffic". Caching is a gateway-side property keyed by identity, not something the client steers with markers. `provider.Usage.UnmarshalJSON` still decodes the gateway's cache telemetry — the DeepSeek `prompt_cache_hit_tokens`/`prompt_cache_miss_tokens` shape, or OpenAI's `prompt_tokens_details.cached_tokens` — so Eitri sees cache hits it never asked for and cannot shape. The verdict for both OpenCode Go dialects is therefore **honours none** of `CacheControl`, `PromptCacheKey`, `PromptCacheRetention`, and the `PromptCacheOptions`/`PromptCacheBreakpoint` pair.

## GitHub Copilot honours none of the four capabilities

`CopilotProvider` fronts `https://api.githubcopilot.com/chat/completions` (`factory.go`, `DefaultCopilotURL`) on the Chat Completions dialect, falling back to the sibling `/responses` endpoint for models Copilot rejects on chat (`retryResponses`, `usesResponses`). `CopilotProvider.Stream` hard-sets `req.Capabilities = 0` before either wire, so no cache bit can pass; `NewCopilot` builds its chat dialect with `copilotRequestPolicy: true`, and `Build` skips every cache step for that policy — the prompt key, retention, and options stay empty and no breakpoint is stamped. The Responses path shares the zeroed capabilities, so `promptCacheKey` and `responsesPromptCacheOptions` return empty and nil. The identity headers Copilot requires (`Copilot-Integration-Id`, `Editor-Version`) gate response shape — whether reasoning deltas stream — not caching.

Copilot's backend is a proxying aggregator, not a first-party inference API: GitHub exposes it as an OpenAI-compatible surface, and the widely used community proxy (ericc-ch/copilot-api) documents that it fronts both `/v1/chat/completions` and `/v1/messages`. GitHub's own documentation names no client-facing prompt-cache parameter, and Eitri already declines to send one. The verdict is **honours none** of the four capabilities in both Copilot dialects, conservatively — the layer's caching is opaque to the client and Eitri has no evidence to rely on it.

## A custom OpenAI-compatible endpoint honours the wire the operator chose

`ProviderCustomOpenAI` (factory.go) builds `NewOpenAICompatible`, which picks its wire from the base URL alone: an Anthropic Messages endpoint when the path ends in `/messages` (`isAnthropicMessagesURL`), and a Chat Completions endpoint otherwise. It is the one shipped adapter whose endpoint is chosen deliberately by the operator, and each wire carries a documented, vendor-supported cache mechanism.

Anthropic's Messages API caches prompt prefixes through `cache_control`: the marker `{"type": "ephemeral"}`, with an optional `ttl` of `5m` (default) or `1h`, placed on individual content blocks and tool definitions. Writes happen only at a breakpoint and their hash is cumulative over everything before it; reads walk back up to twenty positions per breakpoint; a request may carry up to four breakpoints (anthropic prompt-caching docs). That is exactly the shape `AnthropicDialect` already knows how to serialize — `anthropicContent.CacheControl` and `anthropicTool.CacheControl` — so a custom Messages endpoint honours `ProviderCapabilityCacheControl`, breakpoints on the message head and on the tool manifest included.

OpenAI's Chat Completions API documents `prompt_cache_key` ("used by OpenAI to cache responses for similar requests to optimize your cache hit rates"), `prompt_cache_retention` (`"in_memory"` or `"24h"`, now deprecated in favour of `prompt_cache_options.ttl`), and the `prompt_cache_options`/`prompt_cache_breakpoint` pair ("supported for gpt-5.6 and later models"). A custom endpoint declared OpenAI-compatible honours the first two on that wire; Eitri's stable per-session key is precisely the routing hint they take, and the engine already sets `Request.SetCacheKey`/`SessionKey` for every turn. The verdict for custom-openai is therefore **honours the capabilities of the wire the operator chose**: `PromptCacheKey` and `PromptCacheRetention` on Chat Completions, `CacheControl` on Anthropic Messages.

## The `PromptCacheOptions`/`PromptCacheBreakpoint` pair is honoured by no shipped endpoint

Eitri gates the pair on both bits together (`chatPromptCacheOptions`, `responsesPromptCacheOptions`) and emits `prompt_cache_options: {"mode":"explicit"}` at the body top level plus `prompt_cache_breakpoint: {"mode":"explicit"}` on a content block. It is OpenAI's explicit-breakpoint protocol for gpt-5.6 and later, and OpenAI's own reference restricts it to those models. Eitri has no per-model capability gate — it declares a capability per provider, not per model — so it cannot know whether the endpoint's selected model supports the pair, and sending it to an older model risks a 400. The only Responses wire Eitri speaks is Copilot's, which declares nothing. No shipped endpoint honours the pair, so it is deleted rather than declared.

## Consequences

- The cache capability bits collapse from five to three: `ProviderCapabilityPromptCacheKey`, `ProviderCapabilityPromptCacheRetention`, and `ProviderCapabilityCacheControl` survive; `ProviderCapabilityPromptCacheOptions` and `ProviderCapabilityPromptCacheBreakpoint` are removed, and with them `chatPromptCacheOptions`, `responsesPromptCacheOptions`, `chatMessagesWithCacheBreakpoints`, `stampResponsesCacheBreakpoints`, the `promptCacheOptions` and `promptCacheBreakpoint` types, and the `prompt_cache_options`/`prompt_cache_breakpoint` wire fields.
- `NewOpenAICompatible` sets its capability set at construction from the base URL: `PromptCacheKey | PromptCacheRetention` for a Chat Completions endpoint, `CacheControl` for a Messages endpoint. `NewOpenCodeGo` keeps `ProviderCapabilityOpenCodeSessionHeader` alone; `CopilotProvider` keeps `req.Capabilities = 0`.
- The OpenCode Go breakpoint stamping — `cacheMarker` and `stampCacheBreakpoints` in chatdialect.go, gated on `ProviderCapabilityCacheControl` — is deleted, comment included. It is unreachable (OpenCode Go declares no `CacheControl`) and its comment ("stamped on OpenCode Go turns so long sessions stay cheap") describes behaviour the contract test forbids. It is also misplaced: `cache_control` is the Anthropic marker, yet the stamping lives in the Chat Completions dialect.
- The Anthropic Messages dialect gains the breakpoint placement it lacks: it must stamp the head/tail breakpoints itself when `CacheControl` is declared, since today it only passes markers through. The Chat Completions dialect keeps emitting the key and retention for custom-openai; it no longer stamps anything.
- `Request.StaticPrefixLen`, `Request.SetCacheKey`, `Message.CacheControl`, and `Tool.CacheControl` all stay, now consumed only by the custom-openai paths. The engine's byte-stable-head discipline and its `proxy_cache_test.go`/`cache_test.go` invariants are unchanged.
- Usage cache telemetry stays. `Usage.UnmarshalJSON` and its `finalize` reconcile real cache-hit/miss/write numbers whether or not Eitri emits a marker, and the OpenCode Go gateway reports them regardless.

## Considered options

- Declare `CacheControl` for OpenCode Go's Anthropic-wire models too (rejected: the contract test forbids `cache_control` on that gateway, which tunes caching server-side).
- Declare cache capabilities for GitHub Copilot (rejected: GitHub documents none and Eitri already hard-zeroes the set; the backend's caching is opaque).
- Declare all four for custom-openai (rejected: the `PromptCacheOptions`/`PromptCacheBreakpoint` pair is model-gated to gpt-5.6+ and unverifiable from an arbitrary endpoint).
- Delete all four capabilities and the whole machinery (rejected: a custom endpoint on the Anthropic wire genuinely honours `cache_control`, and that is the one shipped path where Eitri's breakpoint discipline can pay off).
- Leave the dead code in place, gated but unreachable (rejected: the `cacheMarker` comment and the engine's cache-hygiene work claim behaviour no wire exhibits, and a maintainer reading them is misled).

## Target capability declarations

This is the unambiguous target for the follow-on tickets.

| Provider | Wire | Declaration |
| --- | --- | --- |
| opencode-go | Chat Completions | `ProviderCapabilityOpenCodeSessionHeader` |
| opencode-go | Anthropic Messages | `ProviderCapabilityOpenCodeSessionHeader` |
| github-copilot | Chat Completions | `0` |
| github-copilot | Responses | `0` |
| custom-openai (base URL ends in `/messages`) | Anthropic Messages | `ProviderCapabilityCacheControl` |
| custom-openai (any other base URL) | Chat Completions | `ProviderCapabilityPromptCacheKey \| ProviderCapabilityPromptCacheRetention` |

Delete outright: `ProviderCapabilityPromptCacheOptions`, `ProviderCapabilityPromptCacheBreakpoint`, and every gate, field, and type they reach; and the `cacheMarker`/`stampCacheBreakpoints` OpenCode Go stamping with its comment.
