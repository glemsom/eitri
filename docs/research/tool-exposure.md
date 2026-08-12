# Exposing Tool Calls to LLMs (OpenAI-compat) — Research Findings

**Ticket:** #10 — "Expose tool calls to LLMs (OpenAI-compat)"
**Question:** Best practice for exposing tool calls (function calling) to LLMs over OpenAI-compatible endpoints (the OpenCode Go provider). How tools should be declared, how the dispatch loop should handle tool-call responses, and what the reliability pitfalls are (schema bloat, malformed args). Anticipates the `skill` tool that activates a skill pack mid-session.

Builds on the wire-protocol ground truth in `docs/research/opencode-provider.md` §3 (the three dialects: Chat Completions `tool_calls` + appended `tool` messages; Responses `function_call` output items + `function_call_output`; Anthropic `tool_use` content blocks + `tool_result`). This document adds the design-level best practice on top of that protocol ground truth.

**Sources:** primary only — OpenAI Function Calling guide (`platform.openai.com/docs/guides/function-calling`), OpenAI Structured Outputs guide (`/guides/structured-outputs`), OpenAI Using tools (`/guides/tools`), Anthropic Tool use with Claude (`platform.claude.com/docs/en/agents-and-tools/tool-use/overview`), Anthropic Define tools (`…/tool-use/define-tools`), Anthropic Strict tool use (`…/tool-use/strict-tool-use`), Anthropic Fine-grained tool streaming + Handling invalid JSON in tool responses (`…/tool-use/fine-grained-tool-streaming`), Anthropic Mid-conversation system messages and tool changes (`platform.claude.com/docs/en/build-with-claude/mid-conversation-system-messages`), and the Agent Skills spec + client-implementation guide (`agentskills.io`). Proven-runtime context from the Skillware project announcement. Snapshot: 2026-08-12.

*This document surfaces facts only. It does not decide the Eitri-level skill-pack payload mechanics — that is ticket #20.*

---

## 1. Declaring tools to minimize schema bloat and maximize reliability

### Tool definitions are billed as input tokens and count against context

Every `tools` array payload is injected into the model's input and billed. From the OpenAI function-calling guide (Token Usage):

> "Under the hood, functions are injected into the system message in a syntax the model has been trained on. This means callable function definitions count against the model's context limit and are billed as input tokens."

Mitigations named by the same page: limit the number of functions loaded up front, shorten descriptions, or use tool search so deferred tools load only when needed. Tool-schema bloat is therefore a *paid* problem, not just a prompt-engineering one.

### Keep the initially-available tool set small (< ~20)

OpenAI "Best practices for defining functions" (function-calling guide):

> "Keep the number of initially available functions small for higher accuracy. … Aim for fewer than 20 functions available at the start of a turn at any one time, though this is just a soft suggestion. Use tool search to defer large or infrequently used parts of your tool surface instead of exposing everything up front."

Implication for a catalog tool (e.g. `skill`): expose a small stable registry front-door, and defer the per-skill detail until activation, rather than listing every capability as a first-class function.

### Name / description / parameters conventions

- Anthropic tool `name` must match `^[a-zA-Z0-9_-]{1,64}$` (Anthropic Define tools, tool reference). Same 64-char ceiling appears in the Agent Skills spec for a skill `name` (1–64 chars, lowercase alphanumeric + hyphens only).
- Anthropic recommends *long and specific* descriptions: "Aim for at least 3–4 sentences for each tool description, more if the tool is complex" (Anthropic Define tools, best practices).
- OpenAI recommends explicit per-parameter descriptions, `enum` + object structure "to make invalid states unrepresentable", the "intern test", and offloading burden by not forcing the model to fill args you already know (function-calling guide).
- The Agent Skills spec binds a skill's `description` to max 1024 chars and treats it as the sole trigger signal; skip a skill whose description is missing.

### One shared JSON-Schema-generated tool set, re-expressed per dialect

The three dialects differ only in outer wrapper: OpenAI functions put the schema under `parameters`; Anthropic puts it under `input_schema` with `strict` beside it; both are plain JSON-Schema objects. The proven open-runtime pattern is to keep **one canonical schema per tool** and serialize it into the per-dialect wrapper at request time. The Skillware project announcement states its skill interface is "a tool schema adapted at load time" for "Gemini, Claude, OpenAI-compatible hosts … whether cloud APIs or local inference" — i.e. the schema is generated once and adapted per transport, not authored per dialect.

### Strict mode is the primary reliability lever

- OpenAI (function-calling guide, Strict mode): "Setting `strict` to `true` will ensure function calls reliably adhere to the function schema … We recommend always enabling strict mode." `strict: true` requires `additionalProperties: false` on every object and every field in `properties` marked `required`; emulate optionals with a `["<type>", "null"]` union. It works via Structured Outputs (grammar/decoder-constrained sampling), so it eliminates type-mismatch and missing-field tool calls at the source.
- Anthropic (Strict tool use): `strict: true` on a custom tool "guarantees Claude's tool inputs match your JSON Schema by constraining the model's token sampling to schema-valid outputs (a technique called grammar-constrained sampling)". Same effect as OpenAI.
- Anthropic explicitly ties strict to agent reliability: "Building reliable agentic systems requires guaranteed schema conformance. Without strict mode, Claude might return incompatible types (`"2"` instead of `2`) or omit required fields, breaking your functions."

### Hard schema size ceilings (the true bloat constraint)

From OpenAI Structured Outputs guide (supported-schema limits):

- A schema may have up to **5000 object properties**, up to **10 levels of nesting**.
- Total string length of all property names, definition names, enum values, and const values cannot exceed **120,000 characters**.
- A schema may have up to **1000 enum values** across all enum properties; a single enum property with >250 values is capped at **15,000 chars** total.
- `allOf`, `not`, `if`/`then`/`else`, and other composition keywords are unsupported in strict mode; passing an unsupported schema with `strict: true` returns an error.

These are ceilings, not targets — the *billed input-token* cost (above) is reached long before them.

---

## 2. Dispatch loop shape per dialect

### OpenAI Responses (`/v1/responses`)

Canonical loop (function-calling guide, "Handling function calls" + "Incorporating results"):

1. Send `input` + `tools`.
2. Model returns `output` items; tool calls are `type: "function_call"` entries each carrying `call_id`, `name`, and a **JSON-encoded string** in `arguments` (_not_ an object).
3. Append the whole `response.output` to your running `input`, then for each `function_call` execute it and append a `type: "function_call_output"` item with the matching `call_id` and a string `output` (`result.toString()` or `json.dumps(result)`).
4. Resubmit the **same** `input` list + `tools`. Repeat until no more `function_call` items.

Key details:

- `output` may contain zero, one, or many `function_call` items — "it is best practice to assume there are several."
- The result `output` should typically be a string; format is your choice (JSON, plain text, error codes). If a function has no return value, return a success/failure string (e.g. `"success"`).
- The pattern reuses one mutable `input` slice — this is what makes the call *stateless*: each HTTP request carries its own complete context, and the server holds nothing between rounds (server-side persistence like `store: true` is optional).

### OpenAI Chat Completions (`/v1/chat/completions`)

Protocol ground truth (from `opencode-provider.md` §3): `tools: [{type:"function", function:{name, description, parameters}}]` + `tool_choice`; response tool calls appear as `choices[].message.tool_calls[]` (`{id, type:"function", function:{name, arguments}}`), streamed as `choices[].delta.tool_calls[]` delta fragments. The multi-step flow mirrors Responses but uses **role-based messages** instead of typed input items:

1. Send `messages` + `tools`.
2. Model returns a `message` whose `tool_calls[]` each have an `id`.
3. Append the assistant message, then for each tool call append a **`role: "tool"`** message carrying the matching `tool_call_id` and the result string.
4. Resubmit the full `messages` + `tools`. Repeat until `finish_reason` is not `tool_calls`.

### Anthropic Messages (`/v1/messages`)

Protocol + flow (Anthropic Tool use with Claude; `opencode-provider.md` §3):

1. Send `tools` (each `{name, description, input_schema}`) + `messages`.
2. Model responds with `stop_reason: "tool_use"` and one or more `tool_use` content blocks `{type:"tool_use", id, name, input}` (`input` is an object, already parsed).
3. Append the assistant message containing those blocks, then a `role: "user"` message whose content is one `tool_result` block per call: `{type:"tool_result", tool_use_id: <id>, content}`.
4. Resubmit full `messages` + `tools` (or `tool_choice`).

The Anthropic reference example sets `tool_choice: {"type":"auto","disable_parallel_tool_use":true}` and appends `response.content` as the assistant turn before the `tool_result` user turn — the `tool_result` must be in a role that *follows* the assistant `tool_use`, so parallel results group under one user message.

### Parallel calls

- OpenAI: `parallel_tool_calls: false` forces exactly zero-or-one call; multiple calls in one turn are otherwise allowed. A fine-tuned model that calls multiple functions disables strict mode for those calls, and a `gpt-4.1-nano` snapshot can emit duplicate tool calls for the same tool in parallel (recommend disabling parallel for that snapshot) — function-calling guide "Parallel function calling".
- Anthropic: parallel tool use is on by default; `tool_choice: {"type":"auto","disable_parallel_tool_use":true}` limits to one per turn.
- The dispatch loop must iterate all returned calls in one pass, never assume a singleton.

---

## 3. Reliability pitfalls and mitigations

### Malformed / truncated JSON arguments from streaming

The single hardest reliability issue is that **tool-call arguments are not guaranteed valid JSON when read from a stream**. Anthropic's fine-grained tool streaming page is the authoritative primary statement:

> "Because the API does not buffer or validate a tool's input before streaming it, you might receive partial or invalid JSON. A response that ends with the stop reason `max_tokens` can also cut a parameter off midway. Accumulate the fragments, guard the parse …"

The stream mechanics (Anthropic): a `tool_use` block begins with `content_block_start` carrying a placeholder `input: {}` (empty object — this marks the slot); the real input arrives as `content_block_delta` with `type: "input_json_delta"`, each carrying a `partial_json` string fragment. You must concatenate the `partial_json` fragments per content-block index and `JSON.parse`/`json.Unmarshal` only when the block closes. On `max_tokens` stop at a boundary the arguments can be cut off mid-parameter.

Canonical mitigation for unparseable input (Anthropic "Handling invalid JSON in tool responses") — do not crash and do not silently skip; feed the error back to the model as a tool result:

- Wrap the raw unparseable string as `{"INVALID_JSON": "<the unparseable input you received>"}`.
- Return it as the `content` of a `tool_result` with `is_error: true`.
- Critically: "Build the wrapper with your JSON library rather than by concatenating strings, so quotes and other special characters in the invalid input are escaped correctly." Escaping via a real JSON encoder is what prevents a second malformed round-trip.
- `content` of a tool result does not itself have to be JSON, but the single-key JSON wrapper makes it unambiguous to the model that you received invalid JSON and preserves the original text for debugging.

OpenAI-side (Responses/Chat Completions) arguments are delivered as a JSON-encoded **string**; the same guard applies: attempt `json.loads(toolCall.arguments)`, and on failure route the raw string back through a `function_call_output` / `tool` message describing the parse failure rather than raising.

Retry-with-escaped-args and JSON repair are variants of the same theme: keep the original fragment, encode it safely via a JSON library, and return it to the model as a structured error so the model self-corrects on the next round. Prefer strict mode (which constrains sampling) to *prevent* malformed args, and reserve the invalid-JSON wrapper as the safety net for the non-strict / streaming path.

### Schema bloat as a reliability failure

Bloat degrades selection accuracy and raises cost (see §1). Mitigations, per primary sources:

- Keep fewer than ~20 tools up front (OpenAI best practice).
- Use "tool search" / deferral so large or infrequent tool surfaces load only when needed (OpenAI; the function-calling "Token Usage" and "Best practices" sections both point here).
- Keep schemas strict-shaped so the model cannot emit invalid states (enums, required fields, `additionalProperties:false`).

### `tool_choice` handling and prompt-cache coupling

`tool_choice` has a direct prompt-cache interaction, which matters because the OpenCode Go surface bills cache reads/writes (see `opencode-provider.md` §6):

- OpenAI Response `tool_choice` supports: `auto` (default), `required` (call one or more), `{"type":"function","name":...}` (forced), `"none"`, and **`allowed_tools`**. OpenAI documents `allowed_tools` specifically as the way to "make only a subset of tools available across model requests, but not modify the list of tools you pass in, so you can maximize savings from prompt caching" — i.e. keep the `tools` array stable per session and carve out access via `tool_choice`, so the stable `tools` prefix stays cached.
- Anthropic `tool_choice` options are `auto`, `any` (must use one), `tool` (forced specific), `none`. Anthropic explicitly warns: "changes to the `tool_choice` parameter will invalidate cached message blocks. Tool definitions and system prompts remain cached, but message content must be reprocessed." (Define tools.) So flipping `tool_choice` per turnaround costs you the message cache.
- Anthropic forced `any`/`tool` prefills the assistant message and suppresses natural-language preamble; `auto`/`none` are the only modes compatible with manual extended thinking. Anthropic notes forced tool use "should not reduce performance" (Define tools).

### The stateless-call constraint

Because a Chat Completions / Responses / Messages call is self-contained, all state (conversation, tool results, activated skills) must be carried in the resubmitted `input`/`messages` array. There is no harness-side global the model can reach into between rounds — which is exactly what the skill-activation tool must be designed around (§4).

---

## 4. The skill-activation tool (the `skill` tool mid-session)

The Agent Skills spec's client-implementation guide is the primary, directly-on-point source. Its core model is **progressive disclosure** in three tiers:

| Tier | What's loaded | When | Token cost |
| --- | --- | --- | --- |
| 1. Catalog | Name + description | Session start | ~50–100 tokens / skill |
| 2. Instructions | Full `SKILL.md` body | On activation | <5000 tokens (recommended) |
| 3. Resources | Scripts / references / assets | When instructions reference them | varies |

"The model sees the catalog from the start, so it knows what skills are available. When it decides a skill is relevant, it loads the full instructions."

### How the activation tool should be declared (per primary source)

- Register a **dedicated activation tool** (e.g. `skill` / `activate_skill`) that takes a skill name and returns the content. This is "required when the model can't read files directly, and optional (but useful) even when it can." Advantages over raw file reads named by the guide: control over what's returned, structured wrapping for context-management identification, listing bundled resources, permission gating, and activation analytics.
- **Constrain the `name` parameter to the valid skill names** (e.g. an `enum` in the tool schema):

> "If you use a dedicated activation tool, constrain the `name` parameter to the set of valid skill names (e.g., as an enum in the tool schema). This prevents the model from hallucinating nonexistent skill names. If no skills are available, don't register the tool at all."

This ties directly to §1's "make invalid states unrepresentable" and to the strict-schema requirement that all fields be `required` with `additionalProperties: false` — an enum-backed `name` is the single strongest guard against a bogus activation call.

- **Hide, don't block.** "Hide filtered skills entirely from the catalog rather than listing them and blocking at activation time." Disabled / permission-denied / `disable-model-invocation` skills should be omitted from the catalog and from the tool enum. When zero skills remain, omit the catalog and do not register the skill tool.

### What activation returns (injection into context)

The skill activation tool resolves a skill name to the pack's `SKILL.md` and returns the instructions. Two content options per the guide: the full file (frontmatter included) or body-only with frontmatter stripped — "Among existing implementations with dedicated activation tools, most take this approach [stripping the frontmatter]." Structured wrapping (e.g. `<skill_content name="...">…</skill_content>` around the body plus a bundled-resource listing) helps the harness identify and protect skill content during context compaction, and lets the model resolve relative references against the skill directory.

### Mid-session injection must not clobber the session prompt-cache prefix

This is the design fact that governs *how* the skill's instructions get into a stateless call. Anthropic's "Mid-conversation system messages and tool changes" primary source:

- The top-level `system` field and the `tools` array sit earliest in the request prefix; "editing it invalidates the prompt cache for the entire conversation." So the worst way to inject a skill is to rewrite the top-level system prompt or mutate the `tools` array mid-session when re-emitting each request — it burns your cache key for the whole conversation.
- Anthropic's own mid-conversation remedies (`role:"system"` messages appended at the tail, and beta `tool_addition`/`tool_removal` blocks with `defer_loading:true`, which keep the `tools` array byte-identical so the cached prefix holds) are Anthropic-Messages-specific and rely on beta headers not available across the other two dialects. They are **not** the portable answer for an OpenAI-compatible stateless surface.
- The portable answer, consistent with §2 across all three dialects, is that **whatever the activation tool returns becomes a tool result appended at the tail of the resubmitted `input`/`messages` array**, exactly like any other tool result. Because the activation output lands at the end of the message sequence and the tool-call history is resubmitted verbatim, the cached prefix (system + tools + prior turns) is untouched, and only the *new* tool result + continuation are reprocessed. On the OpenCode Go surface the per-session `promptCacheKey`/`prompt_cache_key` (see `opencode-provider.md` §6) then keeps reusing the unchanged prefix across activation rounds.

### Security: keep skill content as a tool result, not as system text

Anthropic's mid-conversation page adds a prompt-injection guard that directly constrains skill-injection design:

> "Not a place for untrusted content. … Do not place text from outside the conversation, such as raw tool output, retrieved documents, or web content, directly in a system message; doing so gives that text operator-level authority. Keep that data in `tool_result` blocks."

Skill packs frequently originate from user/project directories (often untrusted), so returning their markdown through the skill tool's **tool result** (not elevating it to a system message) is both the cache-safe *and* the injection-safe choice.

### Deduplication and compaction protection

Per the Agent Skills client guide:

- **Deduplicate activations** — track which skills are already in context and skip re-injection of the same pack, so the same instructions don't appear twice in the conversation.
- **Protect skill content from compaction** — flag skill tool outputs (or use the structured wrapping tags) so a context-compaction/pruning pass does not silently drop durable behavioral instructions mid-session ("losing them mid-conversation silently degrades the agent's performance").

### Explicitly out of scope

This document surfaces *how* tool exposure and skill activation should behave on the three dialects. The concrete payload mechanics of a skill pack (frontmatter parsing, body normalization, packaged resources, `.agents/skills` discovery scopes and collision rules) are the Eitri-level decision tracked as ticket #20 and are not decided here.

---

## Key numbers locked in the docs

- Anthropic tool/skill `name`: `^[a-zA-Z0-9_-]{1,64}$`; skill `description` max 1024 chars (spec).
- Anthropic recommends ≥3–4 sentences per tool description; OpenAI recommends few tools, exact `enum`-shaped schemas.
- Strict-mode schema ceilings (OpenAI Structured Outputs): 5000 properties, 10 nesting levels, 120k total property/def/enum/const chars, ≤1000 enum values total, ≤15k chars per single >250-value enum.
- Progressive disclosure token costs: ~50–100 tokens/skill catalog; <5000 tokens on activation (Agent Skills spec).
- OpenAI: aim <20 tools available at start of a turn; `allowed_tools` preserves prompt caching; Chat Completions non-strict by default, Responses normalizes to strict.
- Anthropic: `tool_choice` change invalidates cached *message blocks*; `tools`/`system` edit invalidates the whole cache.

## Open items / caveats for the Spec

- `strict: true` is per-vendor (OpenAI Structured Outputs vs Anthropic grammar-constrained sampling) and has different supported-schema subsets; the shared JSON-Schema generator must target the *intersection* of supported keywords so one canonical schema is valid across all three dialects, or fall back to `strict: false` where a schema uses unsupported keywords.
- Chat Completions remains non-strict by default; if a Go model routes to Chat Completions and a schema uses unsupported composition keywords, plan on the invalid-JSON-error-tool-result safety net rather than relying on strict guarantees.
- Whether a tool is honored, strict, or parallel-capable is **per-model**; the OpenCode Go model metadata includes a `tool_call` capability (see `opencode-provider.md` §1) that discovery should read before assuming a model speaks a given tool dialect.
- The `allowed_tools` / stable-`tools`-array pattern (OpenAI) and the never-edit-`system`/`tools` rule (Anthropic) both point the same way for the skill tool: keep the `tools` array stable per session and express "what's callable now" via `tool_choice`, so per-session prompt caching on the Go surface survives skill activation.
