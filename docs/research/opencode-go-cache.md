# OpenCode Go — LLM caching support in Eitri

Question: Does the OpenCode Go LLM provider support caching? Is Eitri making use
of it? Can we get cache statistics?

Findings, traced against the provider seam in `internal/provider/` and the TUI
telemetry in `internal/tui/` (the authoritative source for how Eitri drives this
provider).

## 1. The provider supports caching — yes

Eitri's primary provider (`ProviderOpenCodeGo = "opencode-go"`, `internal/provider/provider.go:157`)
targets the OpenAI-compatible endpoint `https://opencode.ai/zen/go/v1/chat/completions`
(`internal/provider/factory.go:13`; POST-only, hence the GET 404). It is used with
DeepSeek-family models (the fixtures model `deepseek-v4-flash`). The provider
advertises **server-side prompt caching** through two independent shapes that Eitri
writes:

- **Session prompt cache** (`internal/provider/chatdialect.go:95-96`, `211-217`):
  the request carries `prompt_cache_key` (a session-scoped key) and
  `prompt_cache_retention: "24h"` so the gateway keeps the session cache alive
  for a day (`promptCacheRetention24h`).
- **Anthropic-style cache breakpoints** (`internal/provider/chatdialect.go:219-268`):
  messages carry `cache_control: {type: "ephemeral", ttl: "24h"}` markers that
  tell the backend where to begin/continue cache segments.

Both are DeepSeek-native surfaces — the code names them `prompt_cache_*` and
"DeepSeek's session cache".

## 2. Eitri makes use of the cache — yes, three ways

1. **Server-side session cache on the prompt cache key** (`internal/engine/engine.go:237-238`).
   Every turn with a non-empty `SessionKey` sets `SetCacheKey` and passes the key;
   the engine keeps the accumulated session history server-side under that key so
   subsequent turns hit the cache (`sessionHistory`/`storeSessionHistory`,
   `engine.go:374-406`).
2. **Cache breakpoints stamped per turn** (`stampCacheBreakpoints`, `chatdialect.go:230-268`):
   on a fresh copy it stamps the stable system prefix (up to two leading system
   messages), the moving tail (last tool message plus the last two user/assistant
   turns), and appends `prompt_cache_retention`. Deliberately **skipped** for
   Zhipu GLM models (`isGLMModel`), whose API rejects Anthropic-style markers, and
   for any request already carrying a marker (no double-stamp).
3. **Telemetry reconciliation** — it reads the cache figures back (see below).

## 3. Cache statistics are available — yes

- **Per turn** — parsed from the usage blob at the provider seam
  (`internal/provider/provider.go:217-265`): `Usage` carries
  `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens`. `UnmarshalJSON` tracks
  which keys were present, and `finalize()` applies an honest fallback when the
  proxy omits the cache shape (no keys present → every input token counts as a
  cold miss: `Hit=0, Miss=PromptTokens`; hit-only is inferred as
  `Miss = PromptTokens - Hit`). Covered by fixtures `usage-nocache`,
  `usage-cache-hitonly`, `usage-cache-missonly`, `usage-final` in
  `internal/provider/testdata/`.
- **Persisted ground truth** — every cycle's `usage` (with cache hits/misses) is
  written to the `messages.jsonl` message-layer transcript
  (`docs/sessions.md:37`, `internal/provider/messagelog.go`), navigable via
  `eitri session show`.
- **Live aggregate** — the TUI right-rail STATS section shows a **cache hit %**
  along with tokens in/out (`internal/tui/rail.go:129-166`), backed by
  `Telemetry` which sums hits/misses per turn (`internal/tui/telemetry.go`,
  `hitPercent()`). The Settings surface shows the same live `cache:%` readout
  (`internal/tui/settings.go:447-451`).

## Summary

| Question | Answer |
|---|---|
| Provider supports caching? | Yes — DeepSeek-native session prompt cache + Anthropic-style cache breakpoints |
| Is Eitri using it? | Yes — sets prompt-cache key + retention, stamps breakpoints, keeps session history under the key |
| Cache statistics obtainable? | Yes — per-turn (parsed usage), persisted (`messages.jsonl` / `eitri session`), and live (rail STATS cache %, Settings readout) |
