## Agent skills

### Batch mode

Eitri supports headless batch mode via the `-b` flag. See `docs/agents/batch.md`.

The `scripts/agent-loop.sh` script loops over `ready-for-agent` issues and runs them in sequence using batch mode.

### Issue tracker

Issues live as GitHub issues in `glemsom/eitri`. Use the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` at root + `docs/adr/`. See `docs/agents/domain.md`.

### Development & release flow

See the "Development & release flow" section in `CONTEXT.md`. It covers versioning, daily development, cutting a release, and the CI pipeline.

### Testing

See `docs/TESTING.md`.