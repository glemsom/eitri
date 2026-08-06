# 0026 — Session-scoped sandbox tmpdir + open_in_browser tool

**Status**: Accepted (amends ADR-0017)

## Context

The `bash` tool runs inside a bwrap sandbox that bind-mounts a **fresh ephemeral tmpdir** at `/tmp` per invocation (`--bind <ephemeral-tmpdir> /tmp`, ADR-0017). Two consequences make it impossible for the agent to open files or URLs in the user's browser:

1. `xdg-open` inside the sandbox cannot reach the host X11/Wayland socket (`/tmp/.X11-unix` is shadowed by the sandbox tmpdir), so there is no way to open a browser from `bash`.
2. A report written to `/tmp/foo.html` inside the sandbox lives in a per-command directory (`/tmp/eitri-sandbox-*`) that is deleted when the command returns, and its host path (`/tmp/eitri-sandbox-<random>`) is unknown outside the sandbox — so even a file URL pointing at it is meaningless on the host.

The harness process itself runs **unsandboxed** (bwrap wraps only bash subprocesses), and `cmd/eitri/main.go` already launches the host browser in-process via `openBrowserURL` (`xdg-open` with `Setpgid` detach) for the `EITRI_OPEN_BROWSER` startup auto-open.

## Decision

Add a dedicated **`open_in_browser` tool** that opens a URL in the user's host browser, executed in-process by the unsandboxed harness — not inside the bwrap sandbox. bwrap stays untouched for `bash`.

- Accepts **one URL per call**, restricted to `http`, `https`, and `file` schemes; any other scheme is hard-rejected. A bare path (no scheme) is normalized to `file://`.
- **`/tmp` rewriting**: a URL whose path starts with `/tmp/` is rewritten to the session's sandbox tmpdir on the host, so files written to `/tmp` inside `bash` open correctly in the host browser. The rewrite applies **only when the mapped host path exists**; otherwise the URL passes through unchanged.
- **Execution**: `xdg-open` via the existing `Setpgid` detach pattern (so Ctrl+C never kills the user's browser), fire-and-forget, exit code + stderr returned; missing `DISPLAY`/`WAYLAND_DISPLAY` is a tool error. No confirmation prompt — the call is already visible in the transcript.
- **Platform**: Linux-only (matching bwrap's platform), behind a small per-platform seam so `open`/`start` mappings are trivial to add later.
- **Availability**: registered in the **base toolset**, so sub-agents can open URLs too (they run in-process with identical privileges; no new boundary).

To make the `/tmp` rewriting deterministic, the sandbox's per-command ephemeral tmpdir is replaced by a **session-scoped tmpdir**: one host directory `/tmp/eitri-sandbox-<session-id>` per run, bind-mounted at `/tmp` in every `bash` invocation of that session, created lazily on first use and cleaned up at session end via the existing `toolReg.EndSession(sessionID)` lifecycle (already wired for UI runs, batch runs, and sub-agents). `bash`'s `TMPDIR=/tmp` environment remains unchanged.

## Considered options (rejected)

- **Relax the bwrap sandbox for `xdg-open`** — widens the attack surface of *every* bash call to reach the user's desktop session; rejected.
- **Generic "run on host" tool** — flexible, but a much wider hole than one URL-opening tool; rejected.
- **Keep per-command ephemeral tmpdir + magic path for reports** — preserves per-command isolation but forces the model to learn a special path and makes rewriting fragile; rejected in favour of the session tmpdir.

## Consequences

- `/tmp` writes in `bash` are now **visible to later commands in the same session** (all belonging to the same agent run); isolation across runs is unchanged.
- Parent and sub-agents have different session IDs (`subagent.go` uses the task ID), so their `/tmp` namespaces are **not shared**; the workspace (bind-mounted identically everywhere) remains the cross-agent handoff channel.
- The startup `EITRI_OPEN_BROWSER` auto-open and the new tool share one launcher implementation.

## References

- [0017 — bwrap sandbox for bash tool](0017-bwrap-sandbox.md) (amended)
- [0020 — browser tool via chromedp NewRemoteAllocator](0020-browser-tool-newremoteallocator.md) — separate concern; `open_in_browser` launches the user's browser, it does not drive it via CDP
