# Eitri — AI Coding Agent for Linux (Specification)

- Status: ready-for-agent
- Repository: `glemsom/eitri`
- Related: wayfinder map [Eitri — wayfinder map (#9)](https://github.com/glemsom/eitri/issues/9) and its child decision tickets (#10–#21); ADRs `docs/adr/0001/0002/0003`; product vision `eitri.md`; domain glossary `CONTEXT.md`.

This document is the single reference spec for building Eitri, a self-hosted, single-binary AI coding agent for Linux anchored on the OpenCode Go provider (`deepseek-v4-flash`). It consolidates every decision resolved in the wayfinder effort into one handoff-ready form. Builders implement against this; a domain glossary and ADRs hold the canonical vocabulary and the rationale behind the binding architectural choices.

Unless stated otherwise, every technical constraint here is normative.

---

## Problem Statement

Writing code interactively with an LLM assistant currently ties the user to a hosted or heavy-setup tool. The user wants a coding agent that:

- runs **locally** — all code, conversation transcripts, config, and execution stay on the user's own machine, with no third-party cloud processing their source;
- is **one binary** with no runtime dependencies to install and maintain (no Docker, no Node, no Kubernetes, no tmux);
- is **private and self-hosted**, talking to the model provider over a single API key;
- is **token-frugal** so long agent sessions stay cheap and fast;
- is **safe by default** — arbitrary commands the agent runs are sandboxed, and risky operations in unattended mode are refused.

Today that combination is unavailable: hosted agents leak context off-machine, and local harnesses either demand a heavy runtime or compromise on safety and cost.

## Solution

Eitri is a single Go binary the user installs to `$PATH` and launches from inside a target workspace. It provides a fullscreen TUI (and a headless batch mode) over one agent run engine. The engine drives an LLM in a stateless tool-call loop — `bash` (sandboxed), file read/write/edit, `web_fetch`, `open_in_browser`, and `skill` activation — while aggressively managing token cost via session-scoped prompt caching, deterministic tool-output compression, and proactive context compaction. Reasoning from capable models is first-class (shown in the TUI, suppressed in batch). Agent Skill packs are auto-discovered and injectable mid-session. The bwrap sandbox is a hard prerequisite, never silently bypassed.

## User Stories

1. As a developer, I want to install Eitri by copying one binary to my `$PATH`, so that I start working without installing Docker, Node, or any other runtime.
2. As a developer, I want to launch Eitri from inside a workspace directory, so that the agent reads, writes, and executes in exactly that directory.
3. As a developer, I want a fullscreen TUI, so that I can converse with the agent, see its output, and act on multiple results without leaving my terminal.
4. As a developer, I want to run Eitri headless in batch mode with a prompt, so that I can script and automate jobs and pipe results into CI.
5. As a privacy-conscious user, I want all conversation data, session transcripts, config, and HTTP traces stored locally under `~/.eitri/`, so that my code and chats never leave my machine.
6. As a developer, I want to configure a provider once with an API key, so that I can start chatting immediately without server setup.
7. As a developer, I want the agent to run shell commands in my workspace, so that it can build, test, and operate on my code.
8. As a security-conscious user, I want the agent's shell commands to run inside a sandbox, so that arbitrary code the agent runs cannot modify my whole system.
9. As a user, I do **not** want Eitri to secretly fall back to unsandboxed execution when bwrap is missing, so that a misconfiguration never downgrades my security silently.
10. As a developer, I want the agent to read files by explicit line ranges, so that it does not dump entire (large) files into the model's context.
11. As a developer, I want the agent to read, write, and edit files with tool calls, so that it can make changes to my codebase in a reviewable way.
12. As a developer, I want the agent to fetch a URL and get Markdown, so that it can consult web documentation within a session.
13. As a developer, I want the agent to open files or URLs in my host browser, so that I can inspect results outside the terminal.
14. As a developer, I want the agent's `bash` output for noisy commands (`ls`, `find`, `grep`, `rg`) to be compressed deterministically, so that long sessions stay cheap and readable.
15. As a developer, I want compressed tool output to be lossless-in-recoverable, so that I can fetch the original output when I need the full detail.
16. As a user, I want long sessions to stay near-constant in cost, so that an afternoon of agent work is not wasted on re-billing a huge context.
17. As a user, I want the model's thinking to be visible in the TUI as a collapsible stream, so that I can watch reasoning without it polluting the final answer.
18. As a user, I want thinking suppressed from batch stdout by default, so that script output stays clean (and `-v` brings it back for debugging).
19. As a developer, I want Eitri to automatically compact a session before it hits the context limit, so that the agent does not crash or refuse as it nears the model ceiling.
20. As a developer, I want compaction to keep the most recent turns verbatim, so that ongoing work and its reasoning are not lost mid-task.
21. As a developer, I want the agent to apply Agent Skill packs, so that I can drop domain expertise into a workspace and have the agent use it.
22. As a developer, I want skills discovered automatically from well-known directories, so that I can share skill packs across projects or scope them per project.
23. As a developer, I want to activate a skill mid-session with a slash command, so that I can pull in instructions without restarting.
24. As a user, I want skills that are disabled or permission-denied to be hidden rather than listed-but-blocked, so that the agent never tries to run something it cannot.
25. As a security-conscious user, I want skill pack content injected as tool results — never elevated into a system message — so that untrusted/packaged instructions do not gain operator-level authority.
26. As a developer, I want Eitri to stop after a configurable max-turns cap by default, so that runaway loops are bounded (default 250 turns, then an interactive prompt).
27. As a user, I want risky operations to be refused in batch/headless mode, so that an unattended automation never confirms something dangerous.
28. As a developer, I want a full session transcript saved under `~/.eitri/sessions/<GUID>/`, so that I can troubleshoot and audit what the agent did.
29. As a developer, I want a `-d` debug flag that additionally writes full HTTP traces to/from the provider, so that I can deep-dive a misbehaving integration.
30. As a user, I want model discovery to surface the available models from the chosen provider in Settings, so that I can pick a model without hand-editing config.
31. As a developer, I want Eitri to read each model's wire dialect and base URL from provider discovery rather than hardcoding a route table, so that model lineups can change without code changes.
32. As a developer, I want the provider's streamed `usage` parsed for cache-hit/miss tokens, so that I can observe and surface cost and cache behavior.
33. As a user, I want a cache hit-ratio indicator in the TUI, so that I can see how well long-session caching is working.
34. As a developer, I want the agent's request head (system prompt + tools + verbatim prior turns) kept byte-identical all session, so that deepseek's prompt cache keeps hitting.
35. As a user, I want live session state (like the current time or directory) delivered as a tail message, never baked into the stable system prompt, so that the cached prefix stays stable.
36. As a developer, I want a `[compacted]` marker shown in the TUI when a session has been compacted, so that surprising cache re-warms are explained.
37. As a developer, I want Eitri to honor the Anthropic/OpenAI tool-dialect differences across models, so that switching between a DeepSeek (Chat Completions), a Qwen (Anthropic Messages) and a GPT (Responses) model works from one design.
38. As a developer, I want one canonical JSON-Schema per tool re-expressed per wire dialect, so that tool definitions are authored once and serialized appropriately per transport.
39. As a developer, I want tool schemas strict-shaped (`additionalProperties:false`, all-required), so that the model cannot emit impossible tool arguments.
40. As a developer, I want malformed tool-call arguments returned to the model as a structured error (wrapped `INVALID_JSON`), not crash the loop, so that the model self-corrects on the next round.
41. As a developer, I want the initially available tool set kept small (< ~20), so that tool definitions stay cheap and selection stays accurate.
42. As a user, I want Eitri to keep the primary model `deepseek-v4-flash` at a sensible default reasoning effort, so that I get good results without fiddling.
43. As a developer, I want reasoning content always re-emitted on deepseek assistant messages, so that tool-call turns do not trigger a 400 from the provider.
44. As a developer, I want compaction never to evict reasoning from in-flight or unsummarized tool-call turns, so that a compact does not break an active tool loop.
45. As a user, I want Eitri to keep my skill content protected from being silently dropped during compaction, so that skills stay active for the whole session.
46. As a user, I want a non-destructive way to pick up where I left off or copy a fix into my editor, so that the terminal agent flows naturally into my normal workflow.

## Implementation Decisions

The following decisions are locked. Where a bullet cites an ADR or ticket, that artifact holds the full rationale; this spec states the resolved, normative form.

### 1. Provider & wire protocol

- **Primary provider: OpenCode Go**, model `deepseek-v4-flash`, config id `opencode-go/deepseek-v4-flash`, credential env `OPENCODE_API_KEY`. Endpoint `POST https://opencode.ai/zen/go/v1/chat/completions`, auth `Authorization: Bearer $OPENCODE_API_KEY`. (Ticket #11)
- **Wire dialect target: OpenAI Chat Completions** for the primary provider — the documented route for DeepSeek/Kimi/GLM/MiMo/Grok/Hy. Chat Completions is the bootstrap default only for `deepseek-v4-flash`; everything else is **data-driven off model discovery**, not a hardcoded prefix table. (Ticket #11)
- **Routing is per-model metadata.** Read `GET /v1/models` (base `https://opencode.ai/zen/go/v1`) and dispatch on each model's `npm`/`url`. Supported families: Chat Completions (open models), Anthropic `/v1/messages` (Qwen/MiniMax), Responses `/v1/responses` (GPT-only). (Ticket #11)
- **Streaming.** All families are SSE. Chat Completions consumes `choices[].delta.{content|tool_calls|reasoning_content}` + terminal `data: [DONE]`. `includeUsage`/`stream_options.include_usage` is force-on; parse `usage` from the stream (incl. final chunk) for cache-hit/miss and token telemetry. (Ticket #11)
- **Provider breadth (T11, ADR-0004).** A factory (`provider.FromConfig`) builds the running provider from the saved `config.Provider`: opencode-go and custom-openai map to the OpenAI-compatible client; github-copilot maps to a Copilot provider on the same Chat-Completions wire. One canonical schema per tool is re-expressed per dialect (`ReExpress`) — no per-provider copies. Copilot batch resolves a stored-valid token → non-interactive refresh → else a clean re-auth-in-TUI error; the interactive device-flow handshake is TUI-only.

### 2. Tool exposure & dispatch loop

- **One canonical JSON per tool, re-expressed per dialect:** OpenAI Chat `parameters`, Anthropic `input_schema`, Responses as the proprietary function shape. Never author per-dialect copies. (Ticket #10)
- **Strict mode is the primary reliability lever.** Schemas built with `additionalProperties:false` and every field `required`; emulate optionals with `["<type>","null"]` unions; target the **intersection** of supported strict keywords so one canonical schema is valid across dialects. Where a provider/model is non-strict, rely on the invalid-JSON error tool-result of §2. (Ticket #10)
- **Tool-set size & stability.** Keep the live tool set < ~20 (tool defs are billed input tokens). Keep `tools`/`system` **byte-stable for the session** to preserve the prompt cache (#12); express "what's callable now" via `tool_choice`/`allowed_tools`, never by editing `tools`/`system` mid-session.
- **Dispatch loop (Chat Completions).** Maintain one mutable `messages` list: append the assistant message (with `tool_calls`), then one `role:"tool"` message per call (matching `tool_call_id`, result as a string), resubmit `messages`+`tools`, repeat until `finish_reason` is not `tool_calls`. Iterate **all** returned calls in one pass; never assume a singleton. (Ticket #10)
- **Malformed/truncated args.** Accumulate streamed argument fragments; parse only when the block closes. On parse failure, return `{"INVALID_JSON": "<raw>"}` as an error tool-result, wrapping with a JSON library (never string concatenation) so quotes/escapes stay correct. Never crash, never silently skip. (Ticket #10)
- **Tool set is the agent engine's fixed surface:** `bash`, `read`, `write`, `edit`, `web_fetch`, `open_in_browser`, `skill`, plus a max-turns cap where Eitri pauses at 250 turns and prompts to continue. (eitri.md §2.1)

### 3. The `skill` activation tool

- **Dedicated `skill` tool; `name` constrained to an `enum`** of valid, filtered skill names; omitted entirely when zero skills. Model sees skill instructions only via a tool result, never via a system rewrite. (Tickets #10, #14)
- **Hold-back by hiding.** Filtered/disabled/`disable-model-invocation` skills are omitted from the catalog and the tool enum — never listed to be blocked at call time. Zero skills → omit the catalog and unregister the tool. (Ticket #14)
- **Payload mechanics (agentskills.io spec):** `skill` returns `<skill_content name="...">` with body-only (frontmatter-stripped) markdown + `<skill_resources>` listing bundled files. Discovery parses frontmatter leniently; activation always strips it; unparseable-at-discovery → omit (fail-closed, warn). (Ticket #20)
- **Scopes:** user-global `~/.agents/skills` + project `.agents/skills`, **project shadows user** on exact-name collision, trust-gated per scope. (Ticket #20)
- **Resources are advertised, not eagerly read** (tier 3). The model fetches on demand via its own sandboxed/compressed `read`/`list` tools; paths resolved against the skill dir; skill-dir reads pre-approved in the cage. (Ticket #13/#20)
- **Dedupe & protect:** re-activating an already-in-context skill skips re-injection; the restructuring tags double as the compaction ring-fence (`["skill"]`), so compaction never drops durable instructions. (Tickets #14, #16, #20)

### 4. Prompt caching

- **Byte-identical prefix is the goal.** Keep stable `system` + stable `tools` + verbatim prior-turn history byte-identical all session; all mutation at the tail. Hit ≈ 50× cheaper than miss on deepseek-v4-flash. (Ticket #12)
- **Bans:** no `system`/`tools` edits mid-session; no history reformatting; no live state (time/cwd) in `system` — route those to a **tail** user message post-turn. Express variation via `tool_choice`/`allowed_tools`; skills delivered as a tail tool result. (Ticket #12, #14)
- **Opt deepseek into session cache:** set `setCacheKey=true` so the body carries `prompt_cache_key=<sessionID>`. Treat it as advisory/content-addressed; keep it as a namespace/telemetry key. (Ticket #12/#11)
- **Telemetry:** instrument per-turn `usage.prompt_cache_hit/miss_tokens` and surface a hit-ratio gauge in the TUI; forces the post-compaction re-warm to be observable. (Tickets #12, #21)

### 5. Tool-output compression

- **Deterministic + idempotent**, compressed at the tool-result boundary (compressed bytes land in cache). Never-inflate gate: if compressed ≥ raw, return raw. `+N more` tail marker — never silent truncation. (Ticket #13)
- **Lossless-by-escape:** on-demand `fetch_original`/re-run recovers full output (Headroom-style). (Ticket #13)
- **Filters:** strip ANSI/progress, group-by-directory/file, dedupe, truncate with `+N more`, applied to `ls`/`find`/`grep`/`rg` reads. All file/list/search reads route through Eitri's own compressible tools (no bypass). (Ticket #13)
- **Honest economics:** this is structurally bounded — the dominant long-session cost is cache reads of the immutable prefix, not fresh tail output. Measure the real billed delta, not a self-reported counter. (Ticket #13)

### 6. Thinking / reasoning model interaction

- **Default: DeepSeek OpenAI-compatible thinking mode** for `deepseek-v4-flash`; `thinking` default-enabled, effort controlled by `reasoning_effort` (`low`/`medium`/`high`/`max` are first-class tiers that pass through unchanged; `xhigh` is remapped → `high`; default **low**). (Ticket #17)
- **Mode toggle:** reasoning is a per-session on/off mode (`thinking_enabled`, default on) surfaced in the TUI Settings panel alongside the `reasoning_effort` dial; turning it off omits the thinking toggle and `reasoning_effort` from the wire (non-thinking runs), while the effort selection is retained so re-enabling restores it. (Tickets #54/#55/#56)
- **Surfacing:** reasoning arrives on streamed `reasoning_content`, hoisted per-turn into the assistant message — never merged into answer `content`. TUI: collapsible thinking block (auto-collapse after the turn). Batch stdout: suppressed by default; `-v` enables. (Ticket #17)
- **The hard 400 constraint:** DeepSeek requires **all** assistant messages to carry `reasoning_content` (empty-ok); and real reasoning **must** persist on every intermediate tool-call turn until the final answer. Eitri always sets the field and never evicts reasoning from tool-call turns. (Tickets #17, #21)
- **Budget:** reasoning is billed output tokens; `max` effort can emit ~384k. Effort is the cost/quality dial; drop effort, keep thinking on. (Ticket #17)

### 7. Session compaction budget & eviction

- **Auto-compact silently at 80% context utilization** (default; fraction configurable). No ask-the-user prompt; effective reserve ≈ `0.2 × ctx` (≈200k at 1M ctx). (Ticket #21 / ADR-0003)
- **One unified engine, two triggers:** proactive 80%-threshold, plus an emergency overflow path firing on a provider `400`/context-overflow **below** the threshold. Both = evict oldest + rebuild summary head; no separate code path. (Ticket #21 / ADR-0003)
- **Eviction:** keep last `tail_turns=2` assistant+user pairs verbatim as a **hard floor** (never evicted even if > token budget) inside a soft `keep_recent_tokens≈8k`; the tail budget includes `reasoning_content` (#17). (Ticket #21 / ADR-0003)
- **Anchored LLM summary:** evicted body → `## Objective` / `## Next Move` summary, capped 4,096 tokens, re-injected at the **head** of the new prefix (becomes the new byte-stable cache prefix, #12). Generated by the same `deepseek-v4-flash` as a separate non-tool call (sub-cent); **fail-safe skip** if the summary prompt alone won't fit. (ADRs #21/#12 / Ticket #21)
- **Cache effect:** compaction = hard cache break; keep the session cache key, treat content prefix as cold (one cold ~20k re-warm ≈ $0.0028). (Ticket #21 / ADR-0003)
- **Ring-fence:** optional `prune` (default off); when on never removes `["skill"]` and never truncates silently. (Ticket #21 / ADR-0003)
- **TUI:** `[compacted]` status entry + live cache hit-ratio gauge; read-only, never blocks the agent. (Ticket #21 / ADR-0003)

### 8. Sandbox, network, & path namespace

- **`bash` runs inside bwrap with host network** (`--share-net`): defense-in-depth for filesystem/PID, not a network censor. Root read-only; workspace RW at its host path; separate PID namespace. (ADR-0001)
- **`web_fetch` is a separate execution path, network-unrestricted** — not a `bash` invocation. `open_in_browser` is host-side, outside the cage. (ADR-0001)
- **No fallback to unsandboxed execution.** bwrap is a **hard prerequisite**: if absent at launch, Eitri errors out with an install-bubblewrap message. It never degrades to direct execution. (ADR-0001) *(This corrects the earlier eitri.md "falls back to direct execution" line.)*
- **Path namespace:** host paths are canonical; workspace mounted at its host path (no rewrite); the only remapped root is session temp: sandbox `/tmp` ↔ host `/tmp/eitri-<GUID>`. One shared `PathTranslator` in the tool registry routes `bash`/`write`/`edit`/`open_in_browser` + validation through a bidirectional, reversible, idempotent prefix-map. The model always sees sandbox `/tmp/...`; the GUID is internal — only a host-side launch translates outward. (ADR-0002 / Ticket #22)
- **Validation on the translated host form.** Writable roots (workspace + configured `extra_writable_paths`) host-absolute; targets outside everything are hard errors. (ADR-0002)

### 9. TUI

- **Charm stack:** Bubble Tea + Lip Gloss + Bubbles; **Glamour over goldmark** (custom-renderer seam) for Markdown→ANSI. (Ticket #18)
- **Display context:** **Ghostty** (Kitty-compatible) is the project's primary console; the core TUI is terminal-agnostic (Charm + primary buffer), and Ghostty/Kitty is relevant only as the graphics-protocol display target for the (deferred) Mermaid work. (Ticket #18)
- **Primary-buffer differential rendering** by default (pi/Claude-Code/Codex pattern) to preserve native selection/scrollback/search via Bubble Tea's swappable `Renderer` seam. Alt-screen reserved for modal settings/dashboard. (Ticket #18)
- **Thinking:** collapsible stream per-turn (auto-collapse after turn). **Compaction:** `[compacted]` marker + cache hit-ratio gauge. **Skills:** show detected and currently-activated skills; slash-command activation (`/skillname`) and `/` completion in the composer. (Tickets #17/#18/#21, eitri.md §2.3)

### 10. Batch / headless mode

- `eitri -b "prompt"` runs the agent once, emits the answer to `stdout`, exits. Thinking suppressed by default; `-v` enables. (eitri.md §2.6)
- Shares the same run engine, sandbox, skills, and on-disk review trail as TUI runs; sessions persist, report, and auto-compact identically. (eitri.md §2.6)
- **Batch guard:** headless runs auto-deny confirmation requests, preventing risky operations from proceeding unsupervised. (eitri.md §2.5)

### 11. Config & storage

- Config at `~/.eitri/config.json`; data under `~/.eitri/` (sessions `~/.eitri/sessions/<GUID>/`). Env: `EITRI_CONFIG` (config path), `EITRI_DIR` (data dir). (eitri.md §2.7)
- `-d` debug flag writes full HTTP traces to/from the provider for deep-dive debug. (eitri.md §2.5)

### 12. Single-binary / no-deps constraint (applies everywhere)

- Compiled Go, no tmux/Node/npm/Docker/Kubernetes as runtime deps. This constraint rules out shelling out to Node-based tools (e.g. `mmdc` for rendering) and any headless-Chromium/CDP dance. (eitri.md §1, Ticket #18)

### 13. Generation-control capability negotiation (special turns)

- **Special turns.** Internal, non-tool generations (e.g. the compaction summary; later JSON-Object-Mode finalizations) are *special turns* distinct from ordinary agent/tool turns. (Issue #58 foundation; issues #59–#62 add the wire emission for each control.)
- **Capability surface, not model/endpoint sniffing.** A provider declares which generation controls it can honor through an optional capability interface (`provider.GenerationControlProvider`) — higher layers type-assert and consume it instead of inspecting model ids or endpoint strings. Absence of the surface means the provider honors no generation controls. (Issue #58)
- **Four controls.** JSON Object Mode, Generation Budget, Sampling Policy, and provider-side Tool Schema Enforcement, keyed by `provider.GenerationControl`. (Issues #59–#62)
- **Required vs optional.** A special turn marks each control it wants required or optional. `provider.NegotiateGenerationControls` pre-flights the plan against the provider capability **before any wire call**: an unsupported **required** control fails fast with a clean `UnsupportedRequiredControlError`; an unsupported **optional** control is dropped, and its absence from the returned honored set is the observable degradation. A control requested multiple times is honored once, and `required` wins over `optional`. The engine exposes the same seam as `Engine.NegotiateGenerationControls` for special turns. (Issue #58)
- **Generation Budget wiring.** The compaction summary is the first special turn to emit a control: it requests `generation_budget` as **required** and, on a supporting provider, carries a hard wire-backed `max_completion_tokens` cap mirroring `SummaryMaxTokens` (issue #60). The local `capTokens` floor remains the mandatory output safety net regardless; ordinary agent/tool turns never carry a budget. A provider that cannot honor the required budget fails the negotiation contract, and the summary is skipped by the existing fail-safe path (eviction still frees context).
- **JSON Object Mode wiring.** `Engine.RunJSONObjectMode` is an internal finalization path (issue #59): it runs a non-tool special turn that requires `json_object_mode` — an unsupported provider fails fast with `UnsupportedRequiredControlError` before any wire call — and, on a supporting provider, carries wire-backed `response_format:{type:json_object}` so the final answer is a valid JSON object. Ordinary agent/tool turns never carry `response_format` and are unaffected.
- **Sampling Policy wiring.** `Engine.RunSamplingPolicy` is an internal special-turn path (issue #61): it runs a non-tool turn that requires `sampling_policy` — an unsupported provider fails fast with `UnsupportedRequiredControlError` before any wire call — and, on a supporting provider, carries exactly one wire sampling knob: `temperature` for temperature-based sampling or `top_p` for nucleus-based sampling. A special turn always selects a single mode, so a provider request **never** carries both `temperature` and `top_p` together; ordinary agent/tool turns carry neither and stay on provider defaults. Copilot does not support this control and degrades per the negotiation contract.
- **Tool Schema Enforcement wiring.** `AgentOptions.ToolSchemaEnforcement` opts a tool-capable agent loop into provider-side Tool Schema Enforcement (issue #62): `Engine.RunAgent` pre-flights `tool_schema_enforcement` as an **optional** requirement once, before any wire call — a supporting provider has each request's tool manifest re-emitted with `strict:true` beside every function's parameters, so the provider rejects schema-violating tool arguments at generation time; an unsupported provider degrades deterministically (strict is omitted) without ever blocking the loop. `Eitri`'s local tool-argument validation (`execToolCall` via the strict-shaped canonical schema) remains the mandatory safety floor before execution regardless, and malformed-JSON/schema-violation paths stay test-covered even with provider-side enforcement active. Ordinary agent/tool turns without the opt-in never carry `strict` and stay byte-identical.

## Testing Decisions

- **Test external behavior, not internals.** A good test drives the agent engine end-to-end against a deterministic fake provider and asserts on the *observable* request/response stream (the `messages`/`tools`/`tool_choice` bodies emitted, the parsed `usage`, the tool results returned) — not on the inside of a given struct or loop.
- **Primary test seam: the run engine, via a fake LLM.** This is the one high seam — TUI and batch both sit on the same engine (§ Implementation 10, eitri.md §2.1/$2.6), so driving the engine with a canned/fake Chat-Completions SSE stream tests the dispatch loop, tool exposure, reasoning hoisting, compression, and compaction together. Newly-introduced seams for the fake provider belong here (the provider interface is the natural boundary); add no per-widget seams that duplicate engine tests.
- **Modules to test:**
  - **Provider client**: wire round-trips (Chat Completions request/SSE parse), streamed `usage` and `reasoning_content` extraction, `setCacheKey`/`prompt_cache_key`, per-model `npm`/`url` routing from a stub `/v1/models`.
  - **Tool registry & dispatch loop**: multi-call iteration, `role:"tool"` resubmission, malformed-args `INVALID_JSON` error tool-result.
  - **`PathTranslator`**: bidirectional reversible prefix-map for `/tmp` ↔ `/tmp/eitri-<GUID>`, idempotence (never double-applies), no rewrite for workspace host paths.
  - **Tool-output compressor**: deterministic/idempotent output, never-inflate gate, `+N more` marker, `fetch_original` recovery.
  - **Compaction engine**: 80%-trigger, overflow trigger, tail-floor eviction (incl. reasoning retention), anchored-summary re-injection into the head, fail-safe-skip, `["skill"]` ring-fence.
  - **Skill activation**: enum-constrained `name`, hide-not-block filtering, frontmatter-strip-on-activation, scope shadowing, dedupe, tag-ring-fence.
  - **Generation-control negotiation**: required-control fail-fast vs optional-control degradation, capability-surface absence, dedupe/required-wins semantics (§13 / issue #58).
  - **JSON Object Mode finalization**: `RunJSONObjectMode` wire-emits `response_format` on a supporting provider and fails fast on an unsupported one, while ordinary turns carry no `response_format` (§13 / issue #59).
  - **Sampling Policy special turns**: `RunSamplingPolicy` pre-flights `sampling_policy` as required — a supporting provider wire-emits exactly one of `temperature`/`top_p` (never both), an unsupported provider fails fast before any wire call, and ordinary turns carry neither (§13 / issue #61).
  - **Tool Schema Enforcement**: a tool-capable loop with `AgentOptions.ToolSchemaEnforcement` pre-flights the optional `tool_schema_enforcement` control — a supporting provider wire-emits `strict:true` tool manifests, an unsupported provider degrades (strict omitted, loop still runs), and the local validation floor stays mandatory (§13 / issue #62).
- **Prior art in-repo:** none yet (spec-first project, no `src/`). Mirror the decision-doc rigor of `docs/research/*` and `docs/adr/*`, and keep the fake-provider fixtures as source-of-truth request/response samples alongside the tests the way the research docs carry canonical primary-source claims.

## Out of Scope

- **Consumers of the tracker:** platform packaging isn't planned (see wayfinder map's Out-of-scope: non-Linux, Docker/K8s/tmux/Node, hosting, multi-tenancy/auth). Within the product spec specifically: OS packaging, cloud orchestration, and multi-user auth are out.
- Secondary/duplicate providers beyond the three documented dialect families (OpenCode Go, GitHub Copilot device-flow, custom OpenAI-compatible).
- Vision/image inputs, voice, or multimodal tool output.
- A separate/free embedding model for compaction summaries (explicitly rejected: same provider, sub-cent per event).
- Web-search as a tool (network is host-shared via `bash`; `web_fetch` covers fetching).

## Further Notes

- **Single authority:** where this spec, `eitri.md`, or a research doc disagree with a wayfinder decision ticket or ADR over a technical constraint, the **decision ticket / ADR** wins — this spec was written to reflect them (e.g. bwrap "no fallback" supersedes eitri.md §3's old "falls back to direct execution" line).
- **Default `reasoning_effort: low`** for agent loops; `max` reserved for hard multi-step tasks; keep the effort setting per-session (byte-stable request head).
- **Prompt-caching is the lynchpin economics.** Almost every optimization above (stable head, skill-as-tail-result, tail-message live state, `allowed_tools`) exists to preserve the byte-identical cache prefix. Preserve that invariant when adding anything to the request.
- **Always keep the model's per-turn `usage` parsed** — it is the telemetry behind the cache gauge, the compaction trigger, and cost accounting.
- The agent tool surface should be treated as a small, stable, strict-shaped set; grow it deliberately, because every added tool is paid in billed input tokens and dilutes tool-selection accuracy.
