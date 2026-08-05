# Changelog

All notable changes to Eitri are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Agent turns are now correlated to HTTP traces by ID at write time: each trace records its `run_id` and 1-based `turn`, an `llm_call` SSE/timeline event carries the successful attempt's `trace_id` plus retry count and timing, and the session report joins turns to traces by ID (the ±30s timestamp heuristic is demoted to a fallback for legacy data). Per-turn retries are surfaced in the report (attempt count + failed attempts before success, plus a `total_retries` run summary), and time-to-first-token (request start → first content token) is recorded per trace and shown in the report. (#988)
- The debug API now exposes the full persisted HTTP trace archive via `GET /api/debug/traces` (query by `session_id` / `provider_id` / `model` / RFC3339 time range, with `limit`/`offset` pagination) and `GET /api/debug/traces/aggregate` (window aggregate over the same filters: count, error rate, p50/p95 latency, and token totals). Traces survive restarts on disk and are now queryable historically, not just through the in-memory ring buffer; traces of permanently deleted sessions are excluded. (#989)
- `GET /api/debug/metrics` exposes aggregate per-provider/per-model LLM health counters accumulated by the HTTP trace recorder: total calls, retry count, error counts by structured class (`rate_limit`, `timeout`, `auth`, `context_length`, `network`, `other`), a latency histogram, token totals (prompt, completion, cache read/write), and cache hit/miss counts. Error classes are classified once at capture time from the HTTP status code and the error message (never by scanning error text at display time). HTTP traces gain the supporting fields (`model`, `attempt`, `finish_reason`, `usage`, `error_class`); for streaming responses longer than the 256KB body cap the usage/finish_reason are recovered from the stream tail instead of being truncated away. Batch (headless) runs and sub-agents feed the same recorder and counters as browser runs. (#987)
- HTTP traces now record one accurate per-LLM-call measurement record: provider-reported token usage (prompt, completion, cache-read/cache-creation, reasoning), `finish_reason`, model name, retry attempt number, and time-to-first-byte. The run-done SSE event now carries the provider-reported usage when available (the text-length estimate is used only when the provider returned none), and the session report shows the enriched fields per turn plus aggregate cache token counts. (#986)
- The `CalibrationStore` now persists per-model chars-per-token observations to disk (`~/.eitri/calibration.json`, or `$EITRI_DIR` when set) and restores them on startup, so calibration data survives restarts instead of resetting to the default 4.0 chars/token. An absent or empty file falls back to current defaults; the store is saved on shutdown (server mode) and at the end of batch runs. (#985)
- The Session Manager now exposes shared read-only accessors (`GetMetaShared`, `GetConversationShared`, `GetConfigShared`) that return the session's internal state directly as references instead of deep-copying the entire conversation on every call. They are documented as manager-owned ("do not mutate") and are the preferred read API going forward. Purely additive — no behaviour change. (#979)
- The LLM retry policy is now configurable per run: `RunConfig.RetryPolicy` (retry attempts + backoff delay) threads through to the agent run loop, and tests inject zero-attempt or zero-backoff policies so dead-endpoint runs fail in milliseconds instead of sleeping 5×1s. Production defaults are unchanged (5 retries, 1s backoff). (#1024)
- Inter and JetBrains Mono are now self-hosted: the UI font stack is bundled as embedded `woff2` assets under `/static/fonts/*` instead of being loaded from `fonts.googleapis.com`. The Google Fonts `<link>`/preconnect tags are gone, the critical latin subsets are `<link rel=preload>`ed in the page head, every `@font-face` uses `font-display: swap` (text renders immediately on slow/offline loads), and the service worker precaches all 13 font files so the app shell is fully offline-capable with zero third-party CDN requests. (#970)
- Static assets under `/static/*` are now served with `Cache-Control: public, max-age=31536000, immutable`, so the ~4.7MB JS/CSS bundle is fetched once and served from cache on every later navigation/reload instead of being re-downloaded. Every asset reference (page shell, JS islands, service-worker precache, PWA manifest icons) carries a `?v=<content-hash>` cache-bust query string derived from the embedded asset contents, so a release changes the URLs and stale JS/CSS can never survive an upgrade. The service worker script is served with `Cache-Control: no-cache` and embeds the same version, so a release updates the worker and drops its old precache automatically. HTML pages and SSE streams are unaffected and remain uncached. (#969)
- The page shell now loads all core scripts with `defer` (non-render-blocking), and the heavy rendering libraries — mermaid (~2.7MB), KaTeX, and Prism — are no longer fetched on every page load. `eitri-lazy-load.js` fetches them on demand only when a diagram, equation, or code block is actually present, and re-scans after every HTMX swap. The service-worker precache list matches the new strategy (heavy libraries are cached on first use). (#968)
- The `browser` tool now exposes a per-action typed JSON schema to the model instead of a free-form args blob: `browser.action` is an `enum` of valid actions and `args` is a discriminated union (`oneOf`) with one typed branch per action carrying its exact required parameters. The hand-rolled `query`→`selector` fallbacks for common LLM mistakes are removed — the schema itself tells the model the selector field is called `selector`. On-wire action names and behaviour are unchanged. (#955)
- `browser` tool gained four new actions: `new_tab` opens a fresh tab and returns its `target_id`, `close_tab` closes a tab, `select` sets an HTML `<select>` dropdown to a given option value, and `get_value` reads back the current value of a form element. All go through the deadline-bounded `prepareTarget` path so a hung CDP connection can't block the agent loop. (#954)
- Tool schema generator (`SchemaOf`) now supports `jsonschema_enum`, `jsonschema_minimum`/`jsonschema_maximum`, `jsonschema_min_items`/`jsonschema_max_items`, and `jsonschema_item_description` struct tags, so tool schemas can carry `enum`, numeric bounds, and array-length constraints for the LLM. `browser.action` now exposes its valid actions as an `enum`. (#950)

### Changed

- `scripts/agent-loop.sh` now processes `ready-for-agent` issues in parallel instead of strictly sequentially. The script is a dispatcher: it claims up to `-j N` issues (default 2), adds an `in-progress` label to each, runs one `eitri -b` worker per issue in a detached git worktree (`.worktrees/issue-N`, worker output to `.worktrees/issue-N/log`), then serially rebases and squash-merges the resulting PRs. Rebase conflicts spawn a focused `eitri -b` resolution run capped at 3 attempts; leftover PRs stay open with a comment and are reported. Stale `in-progress` labels are cleaned up on the next startup. Ctrl+C/SIGTERM stops claiming after the current batch finishes (workers run via `setsid --wait`); a second signal forces an immediate exit. (#1035)

- Live context and compaction estimates now use the calibrated chars-per-token ratio for the current model: the context panel's `context_update` events (`ComputeContext`) and the auto-compaction high-water gate (`compactSessionHistory`) pass the active `CalibrationStore` and model name instead of a nil store and empty model, so both call sites stop falling back to the default 4.0 chars/token once calibration data exists. Default behaviour is unchanged when no calibration data is present. (#985)
- HTTP trace persistence is now non-blocking: completed traces are handed to a bounded async worker (`SaveTraceAsync`) so disk I/O never blocks the LLM request path, and the recorder's `OnComplete` callback now fires after the recorder mutex is released so parallel sessions never serialize on a trace write. The shutdown flush drains the async queue before persisting remaining recorder traces, so every trace still reaches disk with no loss or duplication. In-flight trace tracking is now capped (64 by default); when the cap is reached the oldest in-flight trace is evicted with an `evicted` marker (fail-safe) instead of growing without bound — a response body that is never read or closed can no longer leak the in-flight map. (#984)
- The Session Manager read path no longer deep-copies the conversation. `Get`/`GetValidated` and the shared accessors now return cheap O(1) shared references (no per-read message allocation; the facade is a single fixed-size allocation regardless of conversation length), and the copying getters (`GetMeta`, `GetConversation`, `GetConfig`) are gone. The few callers that genuinely need a detached snapshot — JSON snapshot serialization, the ChatPage/ReportPage template rendering path, and the debug endpoints (polled concurrently with active runs) — now call explicitly-named copy helpers (`CopySession`, `CopyMeta`, `CopyConversation`, `CopyConfig`). Benchmark: `Get` on a 1000-message conversation is ~79 ns/op / 1 alloc vs ~35 µs/op / 180 KB for `CopySession`. (#981)
- Session callers have been migrated to the shared read accessors from #979: every read-only call site of the copying getters (`Get`/`GetValidated` ownership checks, `GetMeta`, `GetConversation`, `GetConfig`) now reads via `GetMetaShared`/`GetConversationShared`/`GetConfigShared`, eliminating the per-request full-conversation deep copy on the chat, render, compact, skills, workspace, and run-status paths. The copying getters stay for callers that genuinely need a detached facade — JSON snapshot serialization, the ChatPage/ReportPage template rendering path, and the debug endpoints (which are polled concurrently with active runs) — each documented at its call site. (#980)

- Per-request HTTP logging is now suppressed in test binaries, so failed-test output dumps stay lean — production servers still log every request at Info level. Test coverage for the request-log fields moved to a direct unit test. (#1031)
- `make test` and `make test-race` now print a single compact verdict line (packages passed/failed, failing test names, and DATA RACE warnings with `-race`) plus only the failing tests' error excerpts, instead of one boilerplate line per package. Full raw output is teed to `dist/test-output.log` / `dist/test-race-output.log` for on-demand grepping, and the exit code mirrors `go test`. (#1032, #1033)

### Fixed

- The chat-orphan regression test for a failed run start (`TestChatOrphanedMessageOnStartRunFailure`, issue #972) is restored: it now points at a reachable mock provider that passes live config validation (model discovery succeeds), then fails `buildLLMService` on an empty API key so the run start fails synchronously after a successful config save — replacing the unreachable-URL trick that config validation now rejects. (#1025)

- Persona home-directory resolution now runs through an explicit per-server seam (`ServerConfig.HomeDir` / `RunConfig.HomeDir`) instead of reading the process `HOME` env var at every lookup, and api test helpers inject a per-server temp home dir instead of mutating the global `HOME` — parallel api tests no longer race on persona resolution and the suite is stable under `make test-race`. Production behavior is unchanged. (#1023)
- The HTTP server now sets `ReadHeaderTimeout` (5s), `IdleTimeout` (60s) and a 1 MiB `MaxHeaderBytes` cap, so a client that connects and stalls mid-header (slow-loris) or leaves dead keep-alive connections behind is closed promptly instead of pinning a goroutine and socket indefinitely. Streaming responses are deliberately exempt from read/write deadlines, so long-lived SSE streams (chat stream + browser events stream) keep working exactly as before. (#966)- Token and thinking deltas are now batched server-side before being sent over SSE (flush every ~50ms or every 4096 chars, plus on event-type/turn changes and stream close), so a streaming run sends far fewer network frames while the client still receives the complete text. Run-state SSE history is now bounded by event count and a byte budget for high-volume token content, so a long reasoning stream no longer grows the in-memory history without bound and a subscriber reconnecting mid-run replays only the recent tail. (#967)
- `web_fetch` now reads the response body through a bounded limit (content cap plus a slack margin) instead of pulling the whole page into memory, so a huge or hostile page stops being read cleanly at the cap and the existing 32 KiB truncation still applies. Redirects are now capped (10) via `CheckRedirect`, so a redirect loop fails with a clear error instead of being followed indefinitely. (#953)
- `grep` with `context` now enforces the 2 KiB output cap during the workspace walk — the same per-line byte accounting used for `context: 0` — instead of accumulating every match and truncating only at render time, so context output can no longer exceed the cap or behave inconsistently. Files without matches are no longer retained in memory during the walk, and context windows are merged around matches as files are scanned instead of caching whole files for later rendering. (#952)
- `browser.click` now initializes through the deadline-bounded `prepareTarget` path like the other browser actions, so a hung CDP connection during `click` returns within the browser action timeout instead of blocking the agent loop indefinitely. The per-element wait/click timeout (10s) is applied as a child of the prepared operation context. (#951)
- LLM tool-call replay now replaces provider-generated opaque tool IDs with stable safe IDs, preventing GitHub Copilot Responses errors like `tool use id ... is invalid` on the next turn.
- LLM tool-call replay now repairs repeated streamed JSON argument chunks and collapses duplicate tool IDs before dispatch, preventing GitHub Copilot tool calls from failing with `invalid character '{' after top-level value`.
- Fix browser page becoming unresponsive during long streams by rendering streaming markdown incrementally instead of re-rendering the whole message on every flush.
- Reduce main-thread jank during streaming: very large single growing blocks (e.g. big code blocks) are now streamed append-only instead of re-rendered from scratch on every 80ms flush; the full-width header no longer uses expensive `backdrop-filter` blur that repainted on every scroll; auto-scroll is debounced, instant (not smooth-animation-stacked) while streaming, and holds position when the user has scrolled up; and the streaming-bubble relocation walk now runs only after an actual HTMX swap rather than on every token.

- Fix the live Thinking panel freezing the UI on long reasoning streams: the panel kept the whole (unbounded) reasoning transcript as one growing text node, forcing the browser to re-wrap/re-layout it on every frame during streaming — O(n²) main-thread layout that made the page unresponsive (gear/nav unclickable, Chrome "kill page"). The live transcript is now bounded to a trailing window while auto-scrolling, keeping the UI responsive.

- Fix the gear/header (global nav) becoming unclickable while a blocked-read confirmation is pending mid-run: the full-screen confirmation overlay (z-index:1000) covered the header (z-index:100), freezing the UI during a running/streaming session. The header now stacks above the overlay so nav stays usable while a confirmation waits.

### Documentation

- **CONTEXT.md**: fix domain glossary and project structure drift — drop OpenRouter from supported providers, add `delegate`/`collect` to the Tool entry, correct the compactor config key names and note salience-aware compaction, drop removed DiffCard from Render component, and add `internal/message/` + `browser` tool to the project structure. (#960)

## [0.1.6] — 2026-07-29

### Added

- Settings now uses save-only config drafts with dirty-state Save/Revert controls and navigation/unload warnings for unsaved changes.
- Settings now always shows editable Provider endpoint controls with provider-default indicators and reset-to-default support.

### Fixed

- Settings Test Connection now validates the unsaved draft, reports discovered model count and selected-model availability, refreshes the model picker, and never writes the saved config.
- Provider switch in Settings now gracefully returns empty model list instead of
  error toast when saved credentials don't match the new provider. Model refresh
  saves the credentials first, then discovers models. (#948)


- Config + Settings UI for `browser_ws_url`: new config field with default `ws://127.0.0.1:9222`, merge handler, and text input in the Settings page with help text. (#918)
- Browser tool: implement `type` action — types text into an element identified by CSS selector. Clears existing value first, handles empty text as no-op, and returns clear error messages for invalid/missing selectors. (#919)
- PWA enablement: Eitri is now installable as a standalone desktop app. Adds `manifest.json`, service worker (`sw.js`), PWA icons derived from `face.webp`, and meta/apple-touch-icon tags in `<head>`. (#945)
- App shell caching: service worker precaches all static assets (`/static/*`) on install, uses cache-first strategy for static assets, network-first for Google Fonts, network-only for `/api/*` and SSE `/stream` endpoints, and falls back to cached app shell for navigation requests when offline. Provides instant loads on repeat visits and offline resilience. (#946)

### Fixed

- `grep` with context now applies the 2 KiB cap to final rendered output instead of double-counting match bytes and truncating early.

- Settings model refresh and Test Connection now use current unsaved form values while preserving masked saved API keys.
- Settings Save now validates the whole draft atomically, rejects unavailable selected models without changing saved config, supports first-time no-model saves, and notes that active runs keep previous settings.
- Settings Save now leaves saved provider config untouched when draft provider validation fails.

- `write` tool now accepts empty `content` values, enabling empty file creation and truncation while still rejecting omitted content.

- Session screenshot file serving now requires the owning `browser_id` cookie and rejects non-screenshot filenames.

- Workspace directory browser and workspace update endpoints now require the owning `browser_id` cookie, returning 401 when missing and 404 when mismatched.

- Fatal agent run errors now move sessions to `error`, persist that status in snapshots, and notify the browser sidebar.

- LLM stream close helper now exits after normal stream completion instead of leaking a goroutine until process exit.

- GitHub Copilot `gpt-5*` models now expose Thinking Level choices in Settings and preserve selected reasoning effort.

- Provider-switch model refresh now passes the selected provider to the server so models are discovered for the correct provider, not the previously saved one.

- Duplicate assistant bubbles when a run produces no text output (e.g., tool-only run) — the render handler now checks that the last assistant message was created after the triggering user message before rendering it.

- GitHub Copilot provider now calls root `/chat/completions` and sends Copilot-required headers during LLM requests. Previously Eitri hit `/v1/chat/completions` and omitted Copilot auth context headers, causing `404 page not found` errors on simple prompts.

- GitHub Copilot provider now discovers and runs `/responses`-only models like `gpt-5.5`. Eitri stores the selected model API mode at config save time and routes Copilot GPT models through root `/responses` when required, while keeping `/chat/completions` for chat-compatible models.

- OpenCode Go provider: ensure `/v1` suffix in base URL for OpenAI-compatible models. Previously, a bare `/zen/go` base URL produced `.../zen/go/chat/completions` (404), missing the required `/v1/` segment.

## [0.1.5] — 2026-07-26

### Added

- **Feed provider Usage data into CalibrationStore**: After each streaming LLM response, the agent loop now extracts `Usage.PromptTokens` from the stream's Done event, computes the chars-per-token ratio from the actual input text, and feeds it into the `CalibrationStore` for per-model exponential moving average calibration. Calibration changes are logged at Debug level. Supports both streaming (`ChatStream`) and non-streaming (`Chat`) paths. (PR #832)

- **Compactor: compact oversized user & assistant messages**: The compactor now scans all message roles (user, assistant, tool) instead of only tool results. A new `MessageSizeThreshold` control (configurable, default 2000 estimated tokens) gates which individual messages are eligible for compaction. Role-appropriate summarization prompts are used for each role. Assistant messages retain their `ToolCalls` after compaction. Compacted non-tool messages are tagged with `[MESSAGE COMPACTED]` prefix to prevent re-compaction. New config field `compaction_message_size_threshold` and `EITRI_COMPACTION_MESSAGE_SIZE_THRESHOLD` env var.

- **Load historical session from disk**: New `SessionManager.LoadFromDisk()` method restores a previously-persisted session snapshot into the in-memory session manager with status forced to idle. New `POST /api/sessions/{id}/load` endpoint triggers loading from disk; responds with an HTMX sidebar swap and redirect to the loaded session's chat view. Returns 404 if the session doesn't exist on disk. No-op redirect if the session is already active. Underlying `RunService.LoadSessionFromDisk()` coordinates disk read, UI session restoration, and conversation history rehydration.

### Fixed

- `make test` no longer deletes `~/.eitri/personas`. Persona test `TestMain` now overrides `HOME` to a temp dir instead of calling `os.RemoveAll` on the real user home directory.
- **`generic` persona location**: the built-in `generic` persona is now
  created in `~/.eitri/personas/` (user-level home) instead of
  `<workspace>/.eitri/personas/`, so it is shared across workspaces and
  not duplicated in each project.

### Changed

- **TurnCompleter interface**: extracted the ~95-line `OnTurnComplete` closure
  passed to `loop.RunAgent` into a proper `TurnCompleter` interface. `RunService`
  implements the interface. Compaction logic shared between auto-compaction
  (turn callback) and manual compaction (`CompactSession`) via the new
  `compactSessionHistory` helper. (#781)

### Removed

- **glob tool**: removed (covered by `bash` + `find`). (#733, #737)

### Added

- **Persona-driven system prompt**: active persona's system prompt is now
  used as the base for runs. A persona selector in the chat top bar lets
  users switch persona mid-session. The existing `system_prompt` config
  field acts as a session override on top of the persona's prompt. (#755)
- **Per-persona skill injection**: personas can declare injected skills
  that are automatically loaded when the persona is active. Deduplication
  prevents conflicts with manually activated skills. (#756)
- **Subagent persona support**: the `delegate()` tool accepts an optional
  `persona` field. Subagents can be spawned with any persona from the
  user's catalog, enabling role-specific subagents while the parent stays
  on another persona. (#757)
- **Persona CRUD**: users can now define named personas (system prompt +
  optional injected skills) in Settings → Personas. Personas are stored
  as YAML files under `.eitri/personas/<name>.yaml`. The `generic` persona
  is auto-created on first run. Up to 10 custom personas enforced.
  See ADR-0018. (#754)
- **Backward compatibility & cleanup**: `active_persona` defaults to
  `"generic"` when unset. Generic persona auto-created on server startup.
  `system_prompt` config field preserved as session override. Settings UI
  updated to clarify its override role. (#758)

- **bwrap sandboxing for bash commands**: shell commands now run inside a
  bubblewrap sandbox by default (requires `bwrap` on PATH; falls back
  gracefully). Read-only root filesystem, writable workspace and `/tmp`,
  separate PID namespace. Network enabled by default. Disable via Settings
  or `"sandbox": {"profile": "none"}` in config. See ADR-0017.

### Documentation

- **README.md**: add Prerequisites section with bwrap install instructions
  and sandbox configuration reference.
- **ARCHITECTURE.md**: add `internal/sandbox/` module to the module map
  and update BashTool description to reflect sandboxing.

## [0.1.4] — 2026-07-24

### Added

- Session persistence: snapshots written after every agent turn to `~/.eitri/sessions/<id>/session.json` and `~/.eitri/history/<id>/history.json`; restored on server startup; deleted on session delete. Persister with graceful Flush shutdown, atomic writes, and 1 GiB retention cap. (#702, #705, #708, #711)
- Gravatar support: `UserEmail` config field with Settings UI profile section; user chat bubbles render Gravatar avatar (MD5 hash, `d=mp` fallback, 32×32). (#671, #672, #683, #684)
- Debug config fields `DebugPrompt`, `DebugRequest`, `DebugLLMDir` in config, migrated from ad-hoc `os.Getenv` calls. (#696)
- Runner sub-package extraction: `runner/runconfig`, `runner/broadcast`, `runner/adapters`, `runner/loop` — clearer module boundaries. (#695, #699)
- `RunOpts` struct replacing `AgentConfig`, `RunSpec` struct, and `RunPlanner` seam for cleaner agent loop setup. (#642, #647, #648, #654)
- CI stability: increase Chrome websocket timeout to 60s, add retry logic to browser startup in tests, and remove duplicate browser test run from `release-check` Makefile target. (#655)
- Comprehensive test coverage: LLM error handling/stream parsing, provider profiles/auth, handler suites (confirm, sessions, skills, config, workspace, debug), runner service methods, tool infrastructure, copilot device flow, runstate SSE writers, and concurrency-safe run tracker/broadcast/subagent stores. (#657, #660, #663, #665, #670, #675, #676, #678, #680, #681, #687)
- HTTP trace recorder gains a dedicated `lastFailingTrace` slot that preserves the most recent non-2xx response (or errored request) — never evicted by the ring buffer. Crash dumps include this as `failing_http_trace` in `crash.json`. `HTTPTrace` gains a `ResponseHeaders` field capturing response headers for provider-side correlation. (#604)

### Fixed

- Assistant chat bubbles no longer stretch to the full messages container width. `.message` is now capped at `max-width: 90%` so wide content (long unbreakable lines, full-width tables) cannot push the bubble background and border past the readable area. Regression test `TestBrowser_AssistantBubbleMaxWidth` covers this.
- SSE stream no longer crashes when LLM returns tool call with empty arguments (e.g. hallucinated tool name). Empty `json.RawMessage` is now sanitized to `{}` before marshaling. (#605)
- Error toast modal X button now correctly closes the overlay. (#599aa38)
- Data race in `browserBroadcaster` between `Unsubscribe` and `Broadcast`. (#687)
- Workspace indicator now shows the session workspace instead of the server launch workspace. (#e86f8b3)
- Workspace update handler now accepts URL-encoded form data. (#5e0f6c7)

### Removed

- `DiffCard` and `FileEditCard` render components (and their LCS diff engine) — edit tool results now display inline in the tool card text output. (#700)

### Changed

- Runner package refactored from a monolith into four sub-packages: `adapters`, `loop`, `runconfig`, `broadcast`. (#695, #699)
- Debug configuration migrated from ad-hoc `os.Getenv` calls to structured `config.Config` fields. (#696)

## [0.1.3] — 2026-07-23

### Fixed

- Debug API `run` object now includes `busy`, `turns`, `pending_approval` fields; SSE diagnostic counters preserved as sibling fields (#585)
- Lock ordering in debug session handlers: snapshot SSE counters atomically under RunService.mu to avoid potential data race with concurrent run cancellation (#589)
- Response card duplication when run completes: EventSource no longer reconnects after receiving the "done" event (RENDERING state now treated as terminal in onerror handler). Also sets a no-active-run timestamp after cleanup to prevent autoConnectOnPageLoad from reconnecting stale sessions. (#N/A)

### Added

- Debug API: expose SSE event history in session debug endpoint (#565)
- Debug API: add `GET /api/debug/sessions/{id}/http` route as path-based alias for session-scoped HTTP trace lookup (#586)
- Debug API: session, runtime, and config endpoints (#556)
- Debug API: SSE subscriber/replay counters (#566)
- Perf: tool definitions computed once per run instead of every turn (hoist tool defs out of agent loop) (#551)
- RunService.ActiveRunCount() method for debug introspection
- Crash dumps: batch mode failure writes structured crash dump (#559)
- Crash dumps: WriteCrashDump() + RunService CrashDumpFunc wiring (#559)
- Crash dumps: UI mode triggers crash dump on fatal agent errors (#560)
- Crash dumps: agent loop panic recovery writes crash dump then re-panics (#560)
- doc.go files for the provider, litellm, skills, and tool packages

## [0.1.2] — 2026-07-23

### Added

- `prompt_cache_key` support for OpenAI-compatible providers (opencode_go, custom_openai) — sends session-scoped cache key to reduce repeated prefix processing on multi-turn conversations. (#553)
- Tool definitions are now hoisted out of the agent turn loop and computed once per run instead of every turn. (#551)

### Fixed

- SessionID prompt cache key no longer incorrectly gated by `ThinkingLevel`.

### Changed

- Pre-release version — superseded by v0.1.3 the following day with additional debug API, crash dump, and doc.go changes.

## [0.1.1] — 2026-07-22

### Fixed

- Session stream reconnect on page navigation: `autoConnectOnPageLoad` now
  always attempts connection instead of skipping when the rendered status is
  "idle". A time-based guard prevents reconnect storms after `no_active_run`.
  Also changes `handleChat` active-run 409 Conflict to 200 OK with
  `HX-Retarget` so the error toast is visible (HTMX drops non-2xx bodies).
  (#N/A)
- edit tool no longer dumps full file content as text blocks; returns concise summary with line change count. FileEditCard component uses snippet from args. (#538)

### Added

- Initial release infrastructure: VERSION file, `--version` flag, GitHub Actions CI + release workflows, versioned builds, multi-platform release targets, changelog, and release orchestration scripts. (#N/A)
- README.md with human-facing overview, installation instructions, configuration docs, and security notes.
- Changelog discipline policy documented in development flow — every behavioural change must have an Unreleased entry.

## [0.1.0] — 2025-07-18

### Added

- Initial public release of Eitri — a self-hosted, single-binary AI coding agent for Linux.
- HTMX + Templ chat UI with SSE streaming, tool cards, Mermaid diagrams, file diffs, and context panel.
- Agent loop with built-in tools: bash, glob, grep, read, write, edit, skill, delegate/collect, web_fetch, render_mermaid_diagram, render_quick_replies.
- Support for OpenCode Go, GitHub Copilot, and OpenRouter LLM providers via litellm transport.
- Agent Skills framework for modular system prompt extensions.
- Sub-agent support (nested agent loops via delegate/collect).
- Session management (in-memory, up to 10 concurrent sessions).
- Batch mode (`-b` flag) for headless issue processing.
- Browser E2E testing via chromedp.
- Provider discovery, authentication, and profile management.
- Architecture Decision Records (docs/adr/).
- Install script for Linux (scripts/install.sh).

[Unreleased]: https://github.com/glemsom/eitri/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/glemsom/eitri/releases/tag/v0.1.6
[0.1.5]: https://github.com/glemsom/eitri/releases/tag/v0.1.5
[0.1.4]: https://github.com/glemsom/eitri/releases/tag/v0.1.4
[0.1.3]: https://github.com/glemsom/eitri/releases/tag/v0.1.3
[0.1.2]: https://github.com/glemsom/eitri/releases/tag/v0.1.2
[0.1.1]: https://github.com/glemsom/eitri/releases/tag/v0.1.1
[0.1.0]: https://github.com/glemsom/eitri/releases/tag/v0.1.0
