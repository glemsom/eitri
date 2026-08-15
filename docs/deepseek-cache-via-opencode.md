# DeepSeek Prompt Cache via OpenCode

The byte-stability invariants that make DeepSeek's prompt cache keep hitting, the
engine seam that enforces them, and the proxy-shape questions to settle at
integration time. The test suite is the spec authority — this document only names
every invariant and points at the test that proves it. If prose and code ever
disagree, the code is right.

## Precondition: verify the OpenCode proxy shape

The engine targets DeepSeek through the OpenCode Go gateway at
`https://opencode.ai/zen/go/v1/chat/completions`. At integration time, confirm two
things with a real call before trusting the whole stack:

1. **`/v1/models` route** exists and reports the DeepSeek model(s) OpenCode exposes.
2. **Usage shape** — whether OpenCode passes through DeepSeek's native
   `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` keys on `usage`, or only
   OpenAI-style `prompt_tokens`. The parse logic is hardened for either
   (`TestOpenAIUsageWithoutCacheKeys`, `TestOpenAIUsagePartialCacheKeys` in
   `internal/provider/openai_test.go`).

The recorded-fixture approach (a committed 2-turn OpenCode-shaped session,
asserted deterministically, no live probe) is the way this is pinned down once the
real capture exists. Synthetic DeepSeek-shaped traces seed the fixtures
today (`testdata/proxy-turn1.sse`, `testdata/proxy-turn2.sse`); the real proxy
capture is swapped in when available. The bootstrap model remains hardcoded to
`deepseek-v4-flash`.

The proxy-verification tests are `internal/engine/proxy_cache_test.go` (head
byte-stability through a proxy-shaped session — the request body bytes are the
real marshaled wire body captured by the recorded fixture server,
`TestRunAgentKeepsByteIdenticalHeadThroughProxy`) and
`internal/provider/openai_test.go` (absent-key usage parse safety,
`TestOpenAIUsageWithoutCacheKeys`), driven by the D3 recorded fixtures under
`internal/provider/testdata/`.

## The seven invariants

Each entry: what must hold, where it lives in code, and which test proves it.

### 1. Static embedded system prompt at `[0]`

The request head opens with a system message whose content is byte-identical to
the checked-in `internal/engine/prompt.md` (compiled in via `//go:embed`). It is
constant text — no live session state (time, cwd) is interpolated; dynamic state
rides a tail message instead.

- Lives: `internal/engine/prompt.go` (`SystemPrompt`, `SystemPromptContent`),
  set at `[0]` by `systemPromptHead()` in `internal/engine/engine.go`.
- Proves it: `TestSystemPromptEmbedded`, `TestSystemPromptIsStatic`,
  `TestSystemPromptTokenBudget` in `internal/engine/prompt_test.go`;
  `TestRunOpensWithSystemPrompt` and `TestRunAgentOpensWithSystemPrompt` in
  `internal/engine/stable_head_test.go`.

### 2. Fixed-order struct marshaling

The request body and every message marshal from Go structs whose `json` tags fix
a stable field order (`role`, `content`, `tool_call_id`, `tool_calls`,
`reasoning_content`; body fields laid out in `chatCompletionBody`). A stable
field order guarantees the same logical head produces the same bytes every turn.

- Lives: `internal/provider/provider.go` (Message/Tool/ToolCall structs),
  `internal/provider/openai.go` (`chatCompletionBody`, `json.Marshal` at request build).
- Proves it: the byte-identity assertions in `cache_test.go` and
  `stable_head_test.go` would fail on any ordering or spacing drift.

### 3. Once-per-session tool manifest

Tool definitions and `tool_choice` are set from the agent's tool registry and
kept identical for the whole session. The tool list is part of the immutable head,
not rebuilt per turn.

- Lives: `AgentOptions.Tools` / `ToolChoice` in `internal/engine/engine.go`,
  attached to the request in `RunAgent`.
- Proves it: `TestRunAgentKeepsStableHeadAcrossTurns` in
  `internal/engine/stable_head_test.go` (tools stable across turns).

### 4. Append-only history

Across consecutive turns, prior turns are re-emitted verbatim at the head; the only
growth is the appended tool-result/assistant pair at the tail. Nothing in the shared
prefix is rewritten in place.

- Lives: the multi-turn loop appends in `internal/engine/engine.go`
  (`messages = append(...)` on the tool-result path).
- Proves it: `TestRunAgentMaintainsByteIdenticalCacheHead` in
  `internal/engine/cache_test.go` (shared prefix byte-identical, head only grows).

### 5. Reasoning echo

Every assistant message carries `reasoning_content` (empty-ok on the wire); real
reasoning persists on tool-call turns so the echoed reasoning is part of the stable
head.

- Lives: `internal/engine/engine.go` (reasoning appended to the assistant message,
  `ReasoningContent` field), marshaled by `provider.go`.
- Proves it: `TestAssistantMessageAlwaysCarriesReasoningContent` in
  `internal/provider/openai_test.go`; stable-head echo in `stable_head_test.go`.

### 6. Gated optional controls

Optional DeepSeek controls (thinking, reasoning effort, sampling, JSON object mode)
are negotiated via a capability seam and emitted only when the provider declares
support — never unconditionally — so capability negotiation cannot change the head.

- Lives: `Engine.NegotiateGenerationControls` in `internal/engine/engine.go`;
  `thinking`/`reasoning_effort` emission in `internal/provider/openai.go`.
- Proves it: `TestOpenAIEmitsThinkingAndReasoningEffort`,
  `TestOpenAIOmitsThinkingWhenDisabled`, `TestOpenAIDeclaresGenerationControlCapabilities`
  in `internal/provider/openai_test.go`.
- The Copilot provider carries the toggle in its explicit form on every turn:
  `thinking:{type:enabled}` when on, and an explicit `thinking:{type:disabled}`
  suppression (with no `reasoning_effort`) when off, overriding the backend's
  reasoning-on server default. This suppression is copilot-path-only and does
  not change the openai path's omit-when-disabled byte shape (issue #263).
- Proves it: `TestCopilotDropsEffortWhenThinkingDisabled`,
  `TestCopilotSendsThinkingEnabledWhenThinkingOn` in
  `internal/provider/copilot_test.go`.

### 7. Cache-stable compaction

The unification compaction engine evicts from the body, never the immutable base
prompt; the anchored summary is re-injected **below** the stable head, and the tail
floor survives byte-for-byte. Post-compaction, the head remains a valid cache prefix.

- Lives: `internal/engine/compact.go` (stable-head pull-out + re-anchor logic),
  wired into `RunAgent` via `AgentOptions.Compaction`.
- Proves it: `TestRunAgentCompactsAtThreshold` in `internal/engine/compact_test.go`
  (head byte-stable through compaction; summary anchored after the base prompt).

## Why no section numbers

This doc intentionally carries no `§` cross-references and no prose that duplicates
code. Every claim above names the enforcing test file directly. A prior prose doc
(`docs/spec.md`) drifted from the code; LLM readers over-weighted it and its
citations orphaned ~100 comments. That failure mode is why the test suite is the
single source of truth and this page is a thin pointer to it.

## Navigation

- Engine test suite: `internal/engine/cache_test.go`, `stable_head_test.go`,
  `prompt_test.go`, `proxy_cache_test.go`.
- Provider test suite: `internal/provider/openai_test.go`.
- Proxy fixture: `internal/provider/testdata/` (`proxy-turn1.sse`,
  `proxy-turn2.sse`, `usage-*.sse`).
