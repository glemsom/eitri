## Agent skills

### Issue tracker

Issues live as GitHub issues in `glemsom/eitri`. Use the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default canonical labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — `CONTEXT.md` at root + `docs/adr/`. See `docs/agents/domain.md`.

## Large output policy

Do not paste large command output into chat.

For noisy commands (`go test`, builds, linters):
- save full output to log file
- report only summary, failures, and last 100-200 lines
- preserve real exit code
- use `set -o pipefail` when piping
- avoid verbose flags unless diagnosing
- rerun narrower scope before rerunning broad scope

For Go:
- avoid `go test -v ./...`
- prefer package/test-scoped runs first
- use full `./...` near end

Example:
```bash
go test ./... > /tmp/go-test.log 2>&1
status=$?
grep -nE '^(FAIL|--- FAIL:)|panic:|^FAIL\t|^# ' /tmp/go-test.log | tail -n 120 || true
tail -n 80 /tmp/go-test.log
exit $status