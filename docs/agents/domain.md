# Domain docs

How the engineering skills consume this repo's domain documentation.

## Before exploring, read these

- **`GLOSSARY.md`** at the repo root — the single source of domain terminology.
- **`ARCHITECTURE.md`** — the structural map; it borrows `GLOSSARY.md`'s vocabulary rather than redefining terms.
- **`docs/adr/`** — the ADRs that touch the area you're about to work in.

This repo is single-context, so there is no `GLOSSARY-MAP.md`. If any of these files is missing, **proceed silently** — don't flag its absence or suggest creating it upfront. The `/domain-modeling` skill (reached via `/grill-with-docs` and `/improve-codebase-architecture`) creates them lazily, when terms or decisions actually get resolved. A multi-context layout (`GLOSSARY-MAP.md` plus one `GLOSSARY.md` per context) would be a deliberate change, not an accident.

## Use the glossary's vocabulary

When your output names a domain concept — an issue title, a refactor proposal, a hypothesis, a test name — use the term as defined in `GLOSSARY.md`, and don't drift to the synonyms its `_Avoid_` lines name.

If the concept you need isn't in the glossary yet, that's a signal: either you're inventing language the project doesn't use (reconsider) or there's a real gap (note it for `/domain-modeling`).

## Flag ADR conflicts

If your output contradicts an existing ADR, surface it explicitly rather than silently overriding:

> _Contradicts ADR-0007 (…), but worth reopening because…_
