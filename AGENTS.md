# AGENTS.md

Guidance for AI coding agents working in this repository.

## Domain docs

`GLOSSARY.md` is the domain glossary; `ARCHITECTURE.md` maps how modules are wired and borrows that terminology rather than redefining it; decisions live in `docs/adr/`. Read [docs/agents/domain.md](docs/agents/domain.md) before naming a domain concept, and write with the glossary's vocabulary rather than the synonyms it avoids.

## Issue tracker

Issues live as GitHub issues, managed with the `gh` CLI. See [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md).

Triage has five canonical roles whose label strings are fixed; use them verbatim per [docs/agents/triage-labels.md](docs/agents/triage-labels.md).

## Code changes

Only add comments in Go source if the comment will add information the code does not already tell; references to spec sections or issues alone are never sufficient.

## Operational docs

Load the relevant docs when working in these areas:

- [docs/batch-mode.md](docs/batch-mode.md) — batch mode, piped stdin, the `--format json` envelope, and exit codes.
- [docs/sandbox.md](docs/sandbox.md) — the `bash` sandbox, writable paths, and `extra_writable_paths`.
- [docs/sessions.md](docs/sessions.md) — session transcripts and the `session` subcommands.
- [docs/render-diagnostics.md](docs/render-diagnostics.md) — TUI rendering behavior, render contracts, and their guards.
- [docs/adr/0002-emoji-identity-tier.md](docs/adr/0002-emoji-identity-tier.md) — glyphs, icons, and where emoji may be used in the UI.
