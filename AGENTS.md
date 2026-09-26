# AGENTS.md

Guidance for AI coding agents working in this repository.

## Domain docs

`CONTEXT.md` is the domain glossary; `ARCHITECTURE.md` maps how modules are wired and borrows that terminology rather than redefining it; decisions live in `docs/adr/`. Read `docs/agents/domain.md` before naming a domain concept.

## Issue tracker

Issues live as GitHub issues, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

Five canonical triage roles use label strings equal to their names: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

## Code changes

Only add comments in Go source if the comment will add information the code does not already tell; references to spec sections or issues alone are never sufficient.

`internal/tui` is being de-monolithed one package at a time; read `docs/agents/de-monolith-tui.md` before moving files in there.
