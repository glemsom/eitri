## Agent skills

### Issue tracker

Issues live as GitHub issues in `glemsom/eitri`, operated via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`.

### Domain docs

Single context — `CONTEXT.md` at root + `docs/adr/` (indexed in `docs/ADRs.md`). See `docs/agents/domain.md`.

### Documentation conventions

- Glossary entries in `CONTEXT.md` are vocabulary only — ≤ 2 sentences per row. No file trees (link to `docs/ARCHITECTURE.md#target-repository-layout` instead); no behavioral specifications. Trim or split long entries, linking to the canonical home.
- Behavioral detail lives in the `docs/ARCHITECTURE.md` module sections; decisions are recorded as ADRs in `docs/adr/` (indexed in `docs/ADRs.md`).

### Development & release flow

See the "Development & release flow" section in `CONTEXT.md` — versioning, daily development, cutting a release, and the CI pipeline.

### Testing

See `docs/TESTING.md`.
