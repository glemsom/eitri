# 0024 — Unified parent-run preparation across UI and batch modes

**Status**: Accepted
**Date**: 2026-08-05

## Context

UI runs (`startRunWithConfig`), batch runs (`BatchRun`), and sub-agent runs
(`SpawnSubAgent`) each assembled their LLM request, tool registry, and system
prompt in separate, partially-duplicated code. The duplication had already
drifted:

- Batch mode never set `max_output_tokens` on the LLM request — it relied on
  the provider's fallback instead of the configured cap.
- Batch mode set no session-scoped prompt-cache key, so batch parent runs got
  no prompt-cache reuse on providers that support it.
- Batch mode had no Agent Skills support: no skills service was wired, so
  personas with required skills silently lost both the skills catalog and the
  `<required_skills>` directive, and the agent had no `skill` tool.
- Batch mode never released browser-tool connections (`toolReg.EndSession`)
  when a run ended, leaking CDP connections across batch runs that used the
  browser tool.
- Batch mode passed a nil crash-dump function to the agent loop, so a panic
  inside the batch agent loop wrote no crash dump.
- The `skill` tool, `delegate`, and `collect` were registered per-path with
  slightly different conditions, and `render_quick_replies` was unconditional
  in the UI path.

The mode-specific differences are small and genuinely mode-specific: batch
has no UI session (no `render_quick_replies`), no confirmation prompt
(auto-denies), and streams text to stdout instead of SSE. Everything else
should be identical.

## Decision

Introduce one shared run-preparation seam, `RunService.prepareRun` (in
`internal/runner/prepare.go`), used by both the UI parent run and the batch
parent run. It is parameterized only by:

- `sessionID` — the run-scoped session identifier (UI session ID or batch
  session ID), which keys prompt-cache, HTTP trace recording, and `EndSession`
  cleanup;
- `skillCtx` — the session's active skill activations (empty for batch);
- `uiSessionMgr` — nil for batch; when non-nil the UI-only
  `render_quick_replies` tool is registered.

For the same config and prompt the seam produces, through a single code path:

1. **The same tool registry** — `bash`, `grep`, `read`, `write`, `edit`,
   `render_mermaid_diagram`, `web_fetch`, `browser`, plus parent-only
   `delegate`, `collect`, and `skill` (when a skills service is wired).
   `render_quick_replies` is registered only when a UI session exists.
2. **The same system prompt contract** — persona/repo-instructions/skills
   catalog assembly, with the `<required_skills>` directive emitted whenever
   the persona requires skills. Batch mode no longer skips skill resolution:
   it wires the same `skills.Service` and passes an empty activation context.
3. **The same LLM request behavior** — `max_output_tokens` from config
   (`req.MaxTokens`), a session-scoped `prompt_cache_key` on providers that
   support it (still skipped for Anthropic-routed models), and the configured
   thinking level. The request builder (`buildRunRequest`) is shared by
   sub-agent runs too, so children inherit the same request contract.

Cleanup is owned by the seam's callers and is identical in both modes:
`defer toolReg.EndSession(sessionID)` releases browser-tool connections, and
the service's crash-dump function is passed to `RunAgent` so a panic inside
either loop writes a crash dump.

Batch mode in `cmd/eitri/main.go` now wires the skills service
(`SetSkillsService`) and the crash-dump callback, so personas with required
skills work exactly as in the UI: the directive is emitted, the agent calls
`skill()` on its first turn, and the loaded skill content flows into the
conversation.

## Consequences

Positive:

- UI and batch runs cannot drift on registry, system prompt, or LLM request
  behavior — one seam, one contract.
- Batch mode gains `skill` support, `max_output_tokens`, prompt-cache keys,
  browser-tool connection release, and loop-panic crash dumps for free.
- Sub-agents inherit the shared request contract (including
  `max_output_tokens`).

Negative:

- The seam adds a small abstraction layer over the two call sites; new
  mode-specific knobs must be added to `runPrepOptions` rather than patched
  into one path.
- Sub-agent runs keep their own (restricted) registry assembly by design —
  they are a genuinely different mode, not a parent run.
