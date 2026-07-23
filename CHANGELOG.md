# Changelog

All notable changes to Eitri are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Response card duplication when run completes: EventSource no longer reconnects
  after receiving the "done" event (RENDERING state now treated as terminal in

### Added

- Debug API: expose SSE event history in session debug endpoint (#565)
  onerror handler). Also sets a no-active-run timestamp after cleanup to prevent
  autoConnectOnPageLoad from reconnecting stale sessions. (#N/A)

### Added

- Perf: tool definitions computed once per run instead of every turn (hoist tool defs out of agent loop) (#551)
- Debug API: session, runtime, and config endpoints (#556)
- RunService.ActiveRunCount() method for debug introspection

- Crash dumps: batch mode failure writes structured crash dump (#559)
- Crash dumps: WriteCrashDump() + RunService CrashDumpFunc wiring (#559)

- Crash dumps: UI mode triggers crash dump on fatal agent errors (#560)
- Crash dumps: agent loop panic recovery writes crash dump then re-panics (#560)

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

[Unreleased]: https://github.com/glemsom/eitri/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/glemsom/eitri/releases/tag/v0.1.0
