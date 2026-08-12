# ADR 0002 — Sandbox path-namespace translation for host-side tools

- Status: accepted
- Date: 2026-08-12
- Related: ticket [Sandbox path-namespace translation for host-side tools (#22)](https://github.com/glemsom/eitri/issues/22); superseeds the open question left by [ADR 0001](0001-bwrap-network-and-egress-seam.md).

## Context

`bash` runs inside a bubblewrap sandbox: root mounted read-only (so common
system tools in `/usr/bin` etc. are available but immutable), the workspace
folder mounted read-write **at the same path as on the host**, and a
session-specific temp dir mounted as sandbox `/tmp`. Host-side tools —
`open_in_browser`, `write`, `edit`, and path validation — run outside the cage
and must resolve the **same path namespace** as `bash` (eitri.md §2.5).
The only genuine namespace mismatch is the session temp: bwrap provisions host
`/tmp/eitri-<GUID>` and exposes it as sandbox `/tmp`.

## Decision

1. **Host paths are the canonical external namespace.** The model addresses
   workspace files by their host absolute paths. Because the workspace is
   mounted read-write at the identical host path, workspace paths need **no
   translation**. The sole remapped root is the session temp: sandbox `/tmp`
   ↔ host `/tmp/eitri-<GUID>`.

2. **One shared translation seam, not per-tool copies.** A single
   `PathTranslator` in the tool registry routes every path-taking tool
   (`bash`, `write`, `edit`, `open_in_browser`) and path validation through the
   same unit. This preserves the "resolve the same `/tmp` namespace" invariant
   without duplicating logic across N tools.

3. **Bidirectional, reversible, prefix-map.** The seam translates both
   directions (sandbox `/tmp` → host `/tmp/eitri-<GUID>` for host-side work,
   and reverse for a path re-entering from the model). It maps the temp root by
   prefix only and is idempotent — nested calls never compound or double-apply.

4. **Path validation happens on the translated host form.** Writable-roots are
   configured host-absolute, so targets are validated after translation: temp
   targets against the session temp root; everything else against the
   workspace root and configured `extra_writable_paths`.

5. **The model always sees sandbox `/tmp/...`.** The `eitri-<GUID>` segment is
   an internal host detail and is never surfaced to the model. Only a genuinely
   host-side launch (`open_in_browser`) translates temp paths to their host
   form at the moment of opening the browser.

## Consequences

- Model-facing temp identity is stable sandbox `/tmp`; the GUID stays internal.
- Single seam keeps the "same namespace" promise across all path-taking tools.
- Root is read-only in-sandbox; only workspace and session temp are writable,
  with `write`/`edit`/validation and host-side open sharing one translator.
