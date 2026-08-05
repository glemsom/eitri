# Domain docs

How to consume this repo's domain documentation when exploring the codebase.

## Before exploring

- Read `CONTEXT.md` at the repo root — the domain glossary.
- Read the ADRs in `docs/adr/` that touch the area you're about to work in. Canonical index: `docs/ADRs.md`.

## Use the glossary's vocabulary

When your output names a domain concept (issue title, refactor proposal, hypothesis, test name), use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids. A concept missing from the glossary is a signal: either you're inventing language the project doesn't use, or the glossary has a real gap.

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (event-sourced orders) — but worth reopening because…_
