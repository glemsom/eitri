# AGENTS.md

Guidance for AI coding agents working in this repository.

## Domain docs

`CONTEXT.md` is the domain glossary; `ARCHITECTURE.md` maps how modules are wired and borrows that terminology rather than redefining it; decisions live in `docs/adr/`. Read [docs/agents/domain.md](docs/agents/domain.md) before naming a domain concept, and write with the glossary's vocabulary rather than the synonyms it avoids.

## Issue tracker

Issues live as GitHub issues, managed with the `gh` CLI. See [docs/agents/issue-tracker.md](docs/agents/issue-tracker.md).

Triage has five canonical roles whose label strings are fixed; use them verbatim per [docs/agents/triage-labels.md](docs/agents/triage-labels.md).

## Code changes

Only add comments in Go source if the comment will add information the code does not already tell; references to spec sections or issues alone are never sufficient.

`internal/tui` is being de-monolithed one package at a time; read [docs/agents/de-monolith-tui.md](docs/agents/de-monolith-tui.md) before moving files in there.

## Operational docs

Contracts a change can break, each beside the code that owns it: [docs/batch-mode.md](docs/batch-mode.md) (piped stdin, the `--format json` envelope, exit codes), [docs/sessions.md](docs/sessions.md) (transcript layout, `session` subcommands), [docs/render-diagnostics.md](docs/render-diagnostics.md) (render contracts and their guards), [docs/tui-iconography.md](docs/tui-iconography.md) (glyph and icon tiers).
