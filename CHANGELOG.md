# Changelog

All notable changes to Eitri are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Config + Settings UI for `browser_ws_url`: new config field with default `ws://127.0.0.1:9222`, merge handler, and text input in the Settings page with help text. (#918)
- Browser tool: implement `type` action — types text into an element identified by CSS selector. Clears existing value first, handles empty text as no-op, and returns clear error messages for invalid/missing selectors. (#919)

### Fixed

- Fatal agent run errors now move sessions to `error`, persist that status in snapshots, and notify the browser sidebar.

- LLM stream close helper now exits after normal stream completion instead of leaking a goroutine until process exit.

- GitHub Copilot `gpt-5*` models now expose Thinking Level choices in Settings and preserve selected reasoning effort.

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

[Unreleased]: https://github.com/glemsom/eitri/compare/v0.1.5...HEAD
[0.1.5]: https://github.com/glemsom/eitri/releases/tag/v0.1.5
[0.1.4]: https://github.com/glemsom/eitri/releases/tag/v0.1.4
[0.1.3]: https://github.com/glemsom/eitri/releases/tag/v0.1.3
[0.1.1]: https://github.com/glemsom/eitri/releases/tag/v0.1.1
[0.1.0]: https://github.com/glemsom/eitri/releases/tag/v0.1.0
