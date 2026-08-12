# Thinking / Reasoning Interaction Pattern — Decision-Ready Findings

**Ticket:** #17 — "Thinking/reasoning-model interaction pattern"
**Question:** How should Eitri interact with chain-of-thought / reasoning models (primary `deepseek-v4-flash`) over OpenAI-compatible Chat Completions — how is `thinking` surfaced, controlled, and budgeted, and what first-class reasoning handling should Eitri adopt (thinking shown in TUI, suppressed in batch stdout by default, `-v` to enable)?

**Sources (primary):**
- DeepSeek API docs — **Thinking Mode guide** and its streaming sample: `api-docs.deepseek.com/guides/thinking_mode`, `api-docs.deepseek.com/api_samples/thinking_mode_api_example_streaming`, and the Chat Completions API reference. Captured via Context7 (docRefs `ctx7:docs:9f067b678d8eb2b40dee3cb1`, `0d71454d2425c2581edb539d`, `0aa4c39ad36de716a4f8297a`).
- `sst/opencode` at commit `1f94d8a` (2026-08-12), checked out at `/tmp/opencode-src`: `packages/opencode/src/provider/transform.ts` — the `deepseek` assistant-message reasoning padding (`~302-314`) and the interleaved `reasoning_content` hoist (`~315-353`), plus the model filter conditionals (`~1248`, `provider.ts:1486`).
- DeepSeek V4 model page — thinking/non-thinking default and `reasoning_effort` tiers (via the pricing/quick_start archive and release write-ups; relayed, not first-party source).

Every claim below is cited to a primary source. Supersedes/enriches nothing in `docs/research/`; leaves existing files unmodified and unlinked.

---

## TL;DR — Recommendation

**Eitri treats reasoning as a first-class, always-present-but-conditionally-visible stream.** Adopt DeepSeek's OpenAI-compatible thinking-mode contract (`reasoning_content` + `thinking` + `reasoning_effort`) as the default, since `deepseek-v4-flash` is the primary provider and routes to Chat Completions (see #11). Specifics:

1. **Control**: `reasoning_effort` in the body (`low`/`high`/`max`; DeepSeek maps `low`/`medium`→`high`, `xhigh`→`max`). Keep `thinking` default-enabled for agent work; let the operator downgrade effort, not toggle thinking off.
2. **Surface on the wire**: consume streamed `choices[].delta.reasoning_content` separately from `content`; accumulate into a per-turn thinking buffer, then hoist into the assistant message.
3. **Round-trip (the hard constraint)**: DeepSeek **400-errors** if any `assistant` message is missing `reasoning_content`. The provider requires **all** assistant messages to carry a reasoning field — even empty — so Eitri must (a) always set the field, and (b) include real `reasoning_content` on every intermediate assistant turn that participates in a **tool-call loop**. Non-tool-call turns ignore/ignore-passed reasoning.
4. **UI**: show thinking **in the TUI** as a collapsible/replaceable stream; **suppress from batch stdout by default**; `-v` enables it. Thinking is **never echoed back into the final answer** — it is a separate channel.
5. **Token budget**: thinking tokens are billed output tokens; `max` effort can emit up to ~384k output tokens on V4. Effort is the binding budget lever; default to a mid tier and let the operator raise it deliberately.

This feeds future spec work on the TUI (#15/#18), compaction (#16/#21), and tool-call dispatch (#10): reasoning must survive those paths.

---

## 1. The wire contract (DeepSeek Chat Completions, primary)

From the DeepSeek **Thinking Mode** guide and the Chat Completions API reference:

- **Request controls**
  - `thinking` (object): `{"type": "enabled"}` toggles thinking on; **defaults to enabled**.
  - `reasoning_effort` (string): sets effort level. DeepSeek accepts OpenAI-style `high`/`max`, and for compatibility maps `low`/`medium`→`high`, `xhigh`→`max`. (Anthropic-format alias is `output_config.effort`.)
  - On the V4 model page: thinking mode supports **low / high / max** reasoning settings; max outputs up to 384k tokens.
- **Response**: reasoning comes back in **`reasoning_content`** alongside the final answer in **`content`** (stream deltas: `choices[].delta.reasoning_content` / `.content`). Reasoning is a separate channel, not part of the answer.

### The tool-call round-trip constraint (the one that bites)

From the Thinking Mode guide, verbatim semantics:

> "It is crucial to correctly pass back the `reasoning_content` in all subsequent requests for turns involving tool calls; failure to do so will result in a 400 error from the API."

> "If the model did not perform a tool call between two user messages, the intermediate `assistant`'s `reasoning_content` does not need to be included in the context for subsequent turns and will be ignored if passed. However, if a tool call was performed, the `reasoning_content` must be included in the context for all subsequent user interaction turns."

**Implication for Eitri:** any assistant turn that fires a tool call, and every following turn until the model answers without a tool, is a tool-call turn — its `reasoning_content` must be persisted in history and re-sent. This is a hard correctness constraint, not a nicety: violating it is a 400, not a silent loss. Eitri's message history layer and its compaction/eviction logic must treat that reasoning text as non-optional on tool-call turns.

## 2. How opencode implements it (primary source, the reference harness)

`sst/opencode` `transform.ts` shows the operational shape Eitri is modeled on:

- **DeepSeek requires all assistant messages to carry a reasoning part.** opencode pads every DeepSeek assistant message with an empty reasoning block if none is present (`transform.ts:~302-314`):
  - DeepSeek-only: `if (msg.content.some(part => part.type === "reasoning")) return msg; else append { type:"reasoning", text:"" }`. A comment states this directly: *"Deepseek requires all assistant messages to have reasoning on them."*
- **Interleaved reasoning is hoisted out of `content` into a dedicated field** (`transform.ts:~315-353`): reasoning parts are filtered out of content, concatenated, and put on `providerOptions.openaiCompatible.reasoning_content`. The comment is explicit that the field must **always** be set, *"even when empty — some providers (e.g. DeepSeek) may return empty reasoning_content which still needs to be sent back in subsequent requests."*
- **Model detection** keys off the model id containing `deepseek` (`transform.ts:~1248`; `provider.ts:1486` selects `field:"reasoning_content"` when `@ai-sdk/openai-compatible` && id includes `deepseek`).

So the interaction pattern is: **read reasoning into a per-turn buffer → keep it OUT of answer content → (for DeepSeek) always re-emit a reasoning field on every assistant message, empty or not, and require real content on tool-call turns.** Both come straight from primary sources.

## 3. Display & output policy (the Eitri-specific contract)

The ticket's own contract is the right frame, and nothing in the sources contradicts it:

- **TUI (fullscreen)**: render thinking as a live, collapsible stream distinct from the answer — opencode/LM-Studio-style "thinking blocks." Auto-collapse after the turn; expandable on demand. The answer appears only in `content`, so the two never interleave in the delivered message.
- **Batch/stdout**: suppress thinking by default; `-v` (verbose) prints it. Rationale: reasoning is scratch work — long (up to 384k output tokens at `max` effort), high-entropy, and not the deliverable. Printing it by default would drown the answer and bloat logs.
- **Never confuse the channels**: `reasoning_content` must not be merged into the answer, dumped into a system prompt, or echoed as user/assistant `content`. It is its own field end-to-end.

## 4. Budgeting

- Reasoning tokens are billed **output** tokens (V4: $0.28/1M output, vs $0.14/1M input uncached). `max` effort can nearly match the input budget per turn, so effort level is the primary cost/quality dial.
- **Recommended default: `reasoning_effort:high`** for agent loops (`max` reserved for hard multi-step tasks; `low`≈`high` on DeepSeek, so the meaningful tiers are `high`/`max`). Let the operator raise/lower per session rather than per request, keeping the request head byte-stable for the prompt cache (#12).
- `thinking` stays **enabled**; an operator who wants cheap fast turns drops effort, not thinking. (DeepSeek maps the legacy `deepseek-chat`=`non-thinking`, `deepseek-reasoner`=`thinking`; both are being deprecated in favor of `deepseek-v4-flash` with the `thinking`/`reasoning_effort` body flags.)

## 5. Non-goals / notes

- Tool-call reasoning round-trip is a **provider correctness** requirement (400s), distinct from the display policy in §3 — both must hold, separately.
- OpenAI Responses' encrypted `reasoning.encrypted_content` is out of scope for Eitri's primary provider (Chat Completions route; see #11). DeepSeek's own Responses support is newer and secondary; the Chat Completions `reasoning_content` path is the target.

## What downstream tickets depend on this

- **Compaction (#16/#21)**: eviction must not drop `reasoning_content` from unsummarized tool-call turns (or it 400s after a compact mid-tool-loop); the LLM summary at the head must preserve the constraint going forward.
- **Tool-call dispatch (#10)**: the resubmitted tool-turn history must carry per-turn reasoning per §1.
- **TUI (#15/#18)**: thinking-block rendering and the `-v` stdout gate are concrete TUI/spec requirements.
