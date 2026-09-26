# Domain Docs

How the engineering skills consume this repo's domain documentation.

## Before exploring, read `CONTEXT.md`

`CONTEXT.md` at the repo root is the single source of domain terminology. `ARCHITECTURE.md` holds the structural map and points back at it for vocabulary rather than redefining terms. If `CONTEXT.md` is missing, **proceed silently** — don't flag its absence or suggest creating it upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates it lazily, when terms or decisions actually get resolved.

This repo is single-context. A multi-context layout (`CONTEXT-MAP.md` plus one `CONTEXT.md` per context) would be a deliberate change, not an accident.

## Use the glossary's vocabulary

When your output names a domain concept — an issue title, a refactor proposal, a hypothesis, a test name — use the term as defined in `CONTEXT.md`, and don't drift to the synonyms its `_Avoid_` lines name.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).
