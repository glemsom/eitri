# Changelog

All notable changes to Eitri are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- HTTP trace recorder gains a dedicated `lastFailingTrace` slot that preserves the most recent non-2xx response (or errored request) — never evicted by the ring buffer. Crash dumps include this as `failing_http_trace` in `crash.json`. `HTTPTrace` gains a `ResponseHeaders` field capturing response headers for provider-side correlation. (#604)
- (new entries here)

### Fixed

- Assistant chat bubbles no longer stretch to the full messages container width. `.message` is now capped at `max-width: 90%` so wide content (long unbreakable lines, full-width tables) cannot push the bubble background and border past the readable area. Regression test `TestBrowser_AssistantBubbleMaxWidth` covers this.

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

[Unreleased]: https://github.com/glemsom/eitri/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/glemsom/eitri/releases/tag/v0.1.1
[0.1.0]: https://github.com/glemsom/eitri/releases/tag/v0.1.0
