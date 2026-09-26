# AGENTS.md

Guidance for AI coding agents working in this repository.

## Domain docs

Single-context: one `CONTEXT.md` at the repo root holds the domain glossary. Structural maps (how code modules are wired) belong in `ARCHITECTURE.md`, which references `CONTEXT.md` for terminology rather than redefining it. Decisions live in `docs/adr/`. See `docs/agents/domain.md`.

## Issue tracker

Issues live as GitHub issues, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

Five canonical triage roles use label strings equal to their names: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

## Code changes

Only add comments in Go source if the comment will add information the code does not already tell; references to spec sections or issues alone are never sufficient.

`internal/tui` is being de-monolithed one package at a time; read `docs/agents/de-monolith-tui.md` before moving files in there.
