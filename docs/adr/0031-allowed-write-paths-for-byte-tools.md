# 0031 — Allowed write paths for byte-tools (write/edit)

**Status**: Accepted
**Date**: 2026-08-07

## Context

The `write` and `edit` tools are hard-restricted to the session workspace
root: both validate their target through `fileutil.ValidateWorkspacePath` and
reject any absolute path outside the workspace with a hard `ToolError`. `read`
already escapes that restriction — the `allowed_read_paths` config key is
threaded into `ReadTool.allowedPaths` and validated through
`fileutil.ValidatePathWithAllowed`. `grep` remains workspace-only: it ignores
`allowed_read_paths`, a pre-existing read/grep asymmetry that is out of scope
here.

The `bash` tool already writes outside the workspace:
`sandbox.Config.ExtraWritablePaths` (config key `sandbox.extra_writable_paths`)
are host paths bind-mounted read-write into the bwrap sandbox (ADR-0017), so a
command can create and modify files there. The byte-level `write`/`edit` tools
cannot follow — a file the agent creates in an extra writable path via `bash`
is unaddressable by `write`/`edit`.

There is a critical wrinkle: `bash`'s `/tmp` is a **session-scoped host shadow
directory** (`/tmp/eitri-sandbox-<session>-*`, created by `sandbox.Manager`
and mounted at `/tmp` inside every sandboxed command, ADR-0026) — not host
`/tmp`. A byte-level `write`/`edit` to host `/tmp/foo` would land in a
*different* place than `bash`'s `/tmp/foo`. `open_in_browser` already solves
this for file URLs by rewriting `/tmp/...` targets through the `TmpdirFor`
callback (`sandbox.Manager.TmpdirFor`, ADR-0026).

## Decision

Widen **both** `write` and `edit` to targets beyond the workspace root, behind
one shared allowed-write-roots mechanism, with the following binding rules:

1. **One mechanism, one source of truth** — the writable roots are
   `sandbox.Config.ExtraWritablePaths`, threaded into `WriteTool`/`EditTool`
   exactly as `allowedPaths` is threaded into `ReadTool`. No new config key:
   the same `sandbox.extra_writable_paths` list governs both the bwrap-
   sandboxed `bash` tool and the harness-running byte-tools.
2. **`/tmp` rewrite** — a `write`/`edit` target starting with `/tmp/` is
   rewritten to the session's sandbox shadow dir via the same `TmpdirFor`
   callback `open_in_browser` uses, so `/tmp/x` names the same host file to
   bash, byte-tools, and the browser. Reusing the callback inherits its
   fallback semantics: rewritten when a session tmpdir is tracked; passed
   through when the sandbox profile is `none` or bwrap is unavailable
   (`TmpdirFor` then returns `("", false)`).
3. **Hard error out of policy** — a target outside the workspace and the
   writable roots is a hard `ToolError`, with **no confirmation prompt**: an
   out-of-policy write is a violation, unlike `read`'s out-of-cover prompt.
4. **No symlink resolution** — validation stays string-based
   (`filepath.Rel`), keeping parity with the workspace check. Symlink
   hardening is a separate cross-cutting change for all path tools.
5. **`grep` untouched** — it remains workspace-only; the pre-existing
   read/grep asymmetry is out of scope.
6. **Absolute host paths assumed** — writable-root entries are assumed to be
   absolute host paths, the same assumption bwrap already makes; no new
   normalization or validation.
7. **Tool schema wording** — the `write`/`edit` `path` jsonschema wording is
   reworded (today: "relative to workspace root, or an absolute path within
   the workspace") to mention the configured writable roots and the `/tmp`
   mapping.

This ADR records the decision only. The code changes (constructor threading,
the `/tmp` rewrite, tests, ARCHITECTURE.md updates) land in a separate
follow-up ticket that depends on this one.

## Considered options (rejected)

- **New config key for writable roots** — duplicates
  `sandbox.extra_writable_paths` and lets the two policy surfaces drift;
  rejected in favour of one shared list.
- **Confirmation prompt for out-of-policy writes** — mirrors `read`'s
  out-of-cover flow, but writes are mutations: a policy violation is
  hard-rejected, not prompted.
- **Symlink-aware validation** — safer against link escapes, but a
  cross-cutting change for every path tool (`read`, `write`, `edit`,
  `grep`, directory browser); deferred to a separate hardening pass.
- **Write to host `/tmp` directly** — a byte-level `/tmp` write would
  silently diverge from bash's session shadow `/tmp`; rejected in favour of
  the rewrite.

## Consequences

- The writable-root policy is now shared between the harness-running
  byte-tools (`write`/`edit`) and the bwrap-sandboxed `bash` tool: one config
  knob controls both.
- `/tmp/x` now means the same host file to bash, `write`/`edit`, and
  `open_in_browser` (ADR-0026).
- Parent and sub-agent runs have different session IDs (a sub-agent uses its
  task ID), hence different `/tmp` shadows — each run's `write`/`edit` to
  `/tmp` maps to its own shadow, consistent with bash (ADR-0026).
- Sandbox-disabled configurations (profile `none`, or bwrap missing or
  unusable) track no session tmpdir, so `/tmp` targets pass through to host
  `/tmp` — the same fallback `open_in_browser` already exhibits.

## References

- [0026 — Session-scoped sandbox tmpdir + open_in_browser tool](0026-session-scoped-tmpdir-and-open-in-browser-tool.md) — the `/tmp` shadow handling this ADR extends
- [0017 — bwrap sandbox for bash tool](0017-bwrap-sandbox.md) — origin of `sandbox.extra_writable_paths`
