# OpenCode Go Chat Endpoints — Decision-Ready Findings

**Ticket:** #11 — "Verify OpenCode Go endpoint facts & decide which endpoint shape Eitri targets for its primary provider (deepseek-v4-flash)."
**Question:** How does OpenCode Go expose chat? Which endpoint shape should Eitri target for `deepseek-v4-flash`?
**Sources (primary, verified this ticket):** `sst/opencode` at commit `1f94d8a3` (2026-08-12), checked out at `/tmp/opencode-src`:
- `packages/web/src/content/docs/go.mdx` — canonical endpoint table (lines ~196-232), auth, usage, privacy.
- `packages/web/src/content/docs/zen.mdx` — pay-per-token sibling, wider model set (same routing pattern).
- `packages/web/src/content/docs/providers.mdx` — `/connect` flow (line 123+).
- `packages/opencode/src/provider/provider.ts` — `resolveSDK`, per-model `npm` + `url` dispatch, `includeUsage`.
- `packages/opencode/src/provider/transform.ts` — reasoning_effort, interleaved reasoning_content (DeepSeek), prompt-cache keys.
- `packages/core/src/v1/config/provider-options.ts` — the "lowerer": npm package → wire protocol serialization.
- `packages/core/src/plugin/provider/opencode.ts` — `OPENCODE_API_KEY` env (line 167), OAuth, `/api/config`.

Supersedes/enriches `docs/research/opencode-provider.md` (ticket #2); leaves it unmodified. Every claim below is cited to a primary source file + line.

---

## TL;DR — Recommendation

**Eitri's primary provider `deepseek-v4-flash` routes to OpenAI Chat Completions:**
`POST https://opencode.ai/zen/go/v1/chat/completions` via `@ai-sdk/openai-compatible` (go.mdx endpoint table, `deepseek-v4-flash` row).

**→ Eitri should target the OpenAI Chat Completions shape (`/v1/chat/completions`) for its primary provider.**

Justification, and everything below, in section 5.

---

## 1. Chat Completions vs Responses vs Messages on OpenCode Go

OpenCode Go is **one host with three protocol-conformant endpoint families**, routed per model. From `docs/go.mdx`:

> "You can also access Go models through the following API endpoints." *(go.mdx, Endpoints section)*

Endpoints **table (go.mdx, Endpoints section, empty-stub above table, rows below)** — abridged to routes:

| Endpoint path (base `https://opencode.ai/zen/go/v1`) | Wire protocol | AI SDK package | Go models |
| --- | --- | --- | --- |
| `/v1/messages` | **Anthropic Messages** | `@ai-sdk/anthropic` | MiniMax M3/M2.7/M2.5, Qwen3.8 Max, Qwen3.7 Max/Plus, Qwen3.6 Plus |
| `/v1/chat/completions` | **OpenAI Chat Completions** | `@ai-sdk/openai-compatible` | DeepSeek V4 Pro/**V4 Flash**, DeepSeek, GLM-5.2/5.1, Kimi K3/K2.7/K2.6, MiMo-V2.5/Pro, Grok 4.5, Hy3 |
| `/v1/responses` | **OpenAI Responses** | `@ai-sdk/openai` | GPT 5.6 Luna (the only Responses model in Go) |

Full authoritative rows (go.mdx Endpoints table): `deepseek-v4-flash | https://opencode.ai/zen/go/v1/chat/completions | @ai-sdk/openai-compatible`; `gpt-5.6-luna | /responses | @ai-sdk/openai`; all Qwen/MiniMax → `/v1/messages` `@ai-sdk/anthropic`.

The same assignment pattern holds across the wider Zen table (zen.mdx Endpoints): Claude/Gemini/Qwen/MiniMax → messages or google; GPT/Grok → responses; DeepSeek/GLM/Kimi/MiMo/Hy/Laguna/etc → chat/completions. So the three-route split is stable by family, not specific to Go's small list.

### Auth / headers — per endpoint (from the lowerer, `provider-options.ts`)

- **OpenAI Chat Completions** (`@ai-sdk/openai-compatible` lowerer): header `Authorization: Bearer <apiKey>` (provider-options.ts `openaiCompatible`); body gets `reasoning_effort` from providerOptions (see §4). `includeUsage` is force-set on for openai-compatible (provider.ts:1694).
- **OpenAI Responses** (`@ai-sdk/openai` lowerer): `Authorization: Bearer`, optional `OpenAI-Organization`/`OpenAI-Project`; reasoning → body `reasoning {effort, summary}` (provider-options.ts `openai`).
- **Anthropic Messages** (`@ai-sdk/anthropic` lowerer): header `x-api-key: <apiKey>`; `effort`/`taskBudget` → body `output_config` (provider-options.ts `anthropic`).

**API key** for all three: the OpenCode Go console key, pasted via `/connect` (providers.mdx:123 "How it works"; go.mdx "How it works"). Env-var credential: `OPENCODE_API_KEY` (opencode.ts:167 `process.env.OPENCODE_API_KEY`).

---

## 2. Model routing by prefix — how it's really expressed

**The routing is NOT a client-side hardcoded prefix table.** It is expressed as **per-model `npm` (AI SDK package) + `baseURL` metadata** supplied by the Go service, then turned into a concrete wire protocol by the "lowerer" map.

- Discovery metadata is per-model: `provider.api` / `model.api.url` (base URL) and `provider.npm` / `model.api.npm` (protocol family). OpenCode maps them: `fromModelsDevModel` sets `api:{ id, url: model.provider?.api ?? provider.api, npm: model.provider?.npm ?? provider.npm ?? "@ai-sdk/openai-compatible" }` (provider.ts:1210-1213). So a Qwen3.7 Max model comes back with `npm:"@ai-sdk/anthropic"` + `url:…/v1/messages`, DeepSeek with `openai-compatible` + `…/v1/chat/completions`, GPT with `@ai-sdk/openai` + `…/v1/responses`.
- Client dispatch is data-driven off `model.api.npm`: `resolveSDK` picks the SDK factory for `@ai-sdk/anthropic` / `@ai-sdk/openai` / `@ai-sdk/openai-compatible` and sets `options.baseURL` from `model.api.url` (provider.ts:110-117 import map, 1696-1734 resolveSDK).
- The **lowerer** `provider-options.ts` is the authoritative npm → wire-serialization map (see §1 header table).

**The "Qwen/MiniMax → Anthropic, rest → OpenAI" shorthand is correct at the family level but the "rest → OpenAI" branch SPLITS into two OpenAI dialects:**

- **Qwen 3.x + MiniMax → Anthropic Messages** (`/v1/messages`).
- **Most open models → Chat Completions** (`/v1/chat/completions`): DeepSeek, GLM, Kimi, MiMo, Grok, Hy3, etc.
- **GPT-only → Responses** (`/v1/responses`): GPT 5.6 Luna.

So the resilient route for Eitri is **read each model's `npm`/`url` from discovery (`/v1/models`)** rather than hardcode a prefix→protocol table. The go.mdx table is only the snapshot-in-time, and the monthly lineup "may change."

---

## 3. The Go client integration API

- **Base host:** `https://opencode.ai/zen/go/v1/…` (all three endpoint families share it).
- **Auth:** API key from the OpenCode console (`opencode.ai/zen`, sign in → Go subscription → copy key), pasted via `/connect` in the TUI. Env var **`OPENCODE_API_KEY`** (opencode.ts:167). OAuth device flow exists for the console account (`/auth/device/code`, `/auth/device/token`; opencode.ts) but the Go *API* path is API-key/Bearer.
- **Discovery:** `GET https://opencode.ai/zen/go/v1/models` — "fetch the full list of available models and their metadata" (go.mdx "Models" section). Per-model metadata includes id, name, context/output limits, cost, modalities, capabilities.
- **Config id convention:** a Go model is referenced as **`opencode-go/<model-id>`** (e.g. `opencode-go/deepseek-v4-flash`). (go.mdx:221-222: "uses the format `opencode-go/<model-id>`… use `opencode-go/kimi-k3`".) The Zen grammar is `opencode/<model-id>` (zen.mdx).

---

## 4. Caveats that matter for the Chat Completions path

- **`reasoning_effort`:** For `@ai-sdk/openai-compatible`, `reasoning_effort` is the body control; tiers default `low|medium|high`, DeepSeek adds `max` (transform.ts:1147-1150, 1765). Sent as body key `reasoning_effort` (provider-options.ts openaiCompatible request mapping).
- **DeepSeek interleaved reasoning:** DeepSeek-family emits reasoning on the **streamed `reasoning_content` field** — opencode forces interleaved field to `reasoning_content` for openai-compatible models whose id includes `deepseek` (provider.ts ~1485: `apiNpm==="@ai-sdk/openai-compatible" && apiID.includes("deepseek") ? { field: "reasoning_content" }`; transform.ts `interleaved` handler).
- **Usage in streamed chunks:** opencode force-sets **`includeUsage: true`** for all `@ai-sdk/openai-compatible` (provider.ts:1694) — so a Chat Completions Go client **must** expect+parse `usage` in the stream (incl. final chunk) to read cost/cached-token telemetry. Go bills "Cached Read"/"Cached Write" (go.mdx price table) and pricing is dollar-limited, so streamed usage is the cheap-cost telemetry source.
- **Prompt cache key:** For `@ai-sdk/openai-compatible` specifically, the transform does NOT set `prompt_cache_key` by default — that field is set only for `@ai-sdk/deepinfra`/`@ai-sdk/cerebras`, and `promptCacheKey` only for `@ai-sdk/openai`/azure/xai/mistral etc., **or any model when `setCacheKey === true`** (transform.ts:1258-1271). So to opt DeepSeek-v4-flash into a stable session-scoped cache key, Eitri must set the **`setCacheKey` provider option = true**, which produces `prompt_cache_key = sessionID`. Otherwise deepseek via openai-compatible gets no automatic session cache key (although the gateway may still cache by request prefix — caching is per-model).
- **No strict prefix fallback:** route from `/v1/models` metadata (`npm`/`url`), not a hardcoded table.
- **Streaming:** all families are SSE (`text/event-stream`); Chat Completions deltas use `choices[].delta.{content|tool_calls|reasoning_content}` + terminal `data: [DONE]`.

---

## 5. ENDPOINT RECOMMENDATION — deepseek-v4-flash

**Recommendation: target OpenAI Chat Completions, `POST https://opencode.ai/zen/go/v1/chat/completions`, `Authorization: Bearer <OPENCODE_API_KEY>`.**

Sources & justification:

1. **DeepSeek V4 Flash's documented route is Chat Completions.** go.mdx Endpoints table: `DeepSeek V4 Flash | deepseek-v4-flash | https://opencode.ai/zen/go/v1/chat/completions | @ai-sdk/openai-compatible`. Same in zen.mdx. This is the canonical, authoritative fact.
2. **Chat Completions is the shared dialect for the DeepSeek/GLM/Kimi/MiMo/Grok/Hy family.** Targeting it makes Eitri's single OpenAI-dialect client immediately cover Eitri's *entire* open-model roster (whatever Eitri chooses across the cheap open families), not just deepseek. Only GPT would need the Responses shape, and MiniMax/Qwen would need the Anthropic shape.
3. **Min-cost coding turns don't need the Responses API.** Responses (`/v1/responses`) is the heavyweight GPT-only route (with `reasoning.encrypted_content`, encrypted client-side-decrypt reasoning). DeepSeek V4 Flash is the cheap open model (go.mdx price table: $0.14/$0.28 per 1M + free-tier variants in zen.mdx; ~31.6k requests/5h estimate — the most per-limit). Chat Completions is the simpler, streamed-usage-friendlier shape; it matches DeepSeek's `reasoning_content` interleaving and supports `includeUsage`.

**Concrete target for Eitri:**
```
POST https://opencode.ai/zen/go/v1/chat/completions
Authorization: Bearer $OPENCODE_API_KEY
Content-Type: application/json
{
  "model": "deepseek-v4-flash",
  "messages": [...],
  "tools": [...], "tool_choice": "...",
  "stream": true, "stream_options": { "include_usage": true },
  "reasoning_effort": "low" | "medium" | "high" | "max",
  "prompt_cache_key": "<stable session id>"   // only if caching honored; opaque, per-model
}
```
Consume SSE deltas: `choices[].delta.content`, `choices[].delta.reasoning_content`, `choices[].delta.tool_calls[]`, terminal `data: [DONE]`, plus `usage` (incl. cached tokens) in the stream when `include_usage` on. Config-id for the model: `opencode-go/deepseek-v4-flash`.

**Precondition:** confirm the model's current `npm`/`url` from `GET /v1/models` at integration time; hardcode Chat Completions only as the bootstrap default for deepseek-v4-flash (the go.mdx table is snapshot-in-time).

---

## Prior file (`opencode-provider.md`) — verified; nothing stale found

Checked its claims against primary sources this ticket:

- ✅ Three protocol families & endpoint paths table — matches go.mdx Endpoints table.
- ✅ Routing by per-model npm + baseURL, not a hardcoded prefix table — matches provider.ts `fromModelsDevModel` + lowerer.
- ✅ "Qwen/MiniMax → Anthropic, rest → OpenAI" with OpenAI splitting into Chat Completions for open models / Responses for GPT — confirmed; correctly flagged the split.
- ✅ Discovery `GET https://opencode.ai/zen/go/v1/models` — matches go.mdx "Models".
- ✅ Config id convention `opencode-go/<model-id>` — matches go.mdx:221.
- ✅ `OPENCODE_API_KEY` env var — matches opencode.ts:167.
- ✅ `includeUsage: true` forced for openai-compatible — matches provider.ts:1694.
- ✅ DeepSeek `reasoning_content` interleaved field — matches provider.ts/transform.ts.
- ✅ Lowerer map (`provider-options.ts`) headers/body mappings — matches.

No stale or wrong claims found. The only nuance worth carrying forward (not an error in the prior file): prompt cache key is **not** auto-set for `@ai-sdk/openai-compatible` by default; it requires the `setCacheKey=true` option (transform.ts:1258-1271). This ticket's file expands that detail (§4).
