## Agent skills

### Issue tracker

Issues live as GitHub issues in `glemsom/eitri`. Use the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` at root + `docs/adr/`. See `docs/agents/domain.md`.

### Documentation conventions

Documentation is layered: glossary vocabulary lives in `CONTEXT.md`, behavioral detail in `docs/ARCHITECTURE.md` module sections, and decisions as ADRs in `docs/adr/` (indexed in `docs/ADRs.md`).

- Glossary entries are vocabulary only — ≤ 2 sentences per row.
- No file trees inside glossary entries; link to `docs/ARCHITECTURE.md#target-repository-layout` instead.
- No behavioral specifications inside glossary entries — behavior belongs in the `docs/ARCHITECTURE.md` module sections.
- ADRs live in `docs/adr/*.md` and are indexed in `docs/ADRs.md`.
- Long glossary entries should be trimmed or split, with a link to the canonical home for the details.

### Development & release flow

See the "Development & release flow" section in `CONTEXT.md`. It covers versioning, daily development, cutting a release, and the CI pipeline.

### Testing

See `docs/TESTING.md`.
