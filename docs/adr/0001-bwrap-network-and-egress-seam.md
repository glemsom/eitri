# ADR 0001 — bwrap network policy & web_fetch egress seam

- Status: accepted
- Date: 2026-08-12
- Related: ticket [bwrap network policy & web_fetch egress seam (#19)](https://github.com/glemsom/eitri/issues/19)

## Context

The bubblewrap sandbox isolates agent shell commands. eitri.md (§2.5) made
bwrap a defense-in-depth boundary for filesystem (read-only root, writable
workspace `+` `/tmp`, separate PID ns) — but left network policy, the `web_fetch`
tool's relationship to the sandbox, and the "fallback to direct execution"
line (§3) unspecified. Also unresolved: how host-side tools like
`open_in_browser` open files that live in the session-scoped `/tmp` namespace.

## Decision

1. **`bash` has host network.** Shell commands run inside bwrap with network
   available (`--share-net`). No network caging of the shell. Rationale: the
   agent needs network from the shell; bwrap is defense-in-depth for
   filesystem/PID isolation, not a network censor.

2. **`web_fetch` is separate from `bash` and network-unrestricted.** The
   fetch-and-convert-to-Markdown tool is its own execution path, not a `bash`
   invocation, and is not network-restricted.

3. **No fallback to unsandboxed execution.** The "falls back to direct
   execution" line (§3) is struck. bwrap is a hard prerequisite: if `bwrap` is
   absent at launch, Eitri errors out with a message to install bubblewrap. It
   never degrades to direct, unsandboxed command execution.

4. **`open_in_browser` is host-side**, outside the bwrap cage. When it opens a
   `file://` target in the session temp, it must translate the sandbox path to
   the host path (`/tmp` → `/tmp/eitri-GUID`) before launching the browser.
   That path-namespace seam is its own open decision: Sandbox path-namespace
   translation for host-side tools (#22).

## Consequences

- The sandbox guarantees read-only root, writable workspace `/tmp`, and PID
  isolation, but not network isolation — network is shared with the host.
- Egress-sensitive operations are governed by tool breadth (narrow `web_fetch`)
  rather than a network-blocked shell.
- A missing `bwrap` installation is a hard startup failure, not a silent
  security downgrade.
- Host-side browser launch needs a sandbox→host temp path mapping, tracked in
  #22.
