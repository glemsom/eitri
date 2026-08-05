# Batch mode (`-b` flag)

Eitri supports a headless batch mode via the `-b` flag. Instead of starting the HTTP server and browser UI, it runs a single prompt against the agent loop and streams the output to stdout.

## Usage

```bash
eitri -b "implement the feature in issue #42"
eitri -b --persona reviewer "review the code in PR #123"
```

The `-b` flag expects a prompt string (the remaining arguments after the flag). Output is streamed token-by-token to stdout as plain text (no SSE, no tool cards, no HTML).

The optional `--persona` flag overrides the active persona for the batch run only — it does not change `config.json`.

**Precedence (highest to lowest):**
1. `--persona` CLI flag — overrides everything for this run
2. `active_persona` in `config.json` — persistent per-workspace persona
3. `"generic"` fallback — built-in default when neither is set

The persona is resolved from the workspace `.eitri/personas/` directory, falling back to `~/.eitri/personas/`. If the persona file does not exist, the run fails with a clear error.

## How it works

1. Loads config from `EITRI_CONFIG` env var or `~/.eitri/config.json`
2. Builds a minimal `RunService` — no UI session manager, no browser
3. Calls `BatchRun` which runs the agent loop with a request-based history manager
4. Streams text tokens to stdout in real-time (tool calls execute silently — only final text is streamed)
5. Exits with code 0 on success, non-zero on failure

Sub-agents are supported in batch mode: the `delegate` and `collect` tools are
registered for headless runs, so the agent can spawn sub-agents for
data-intensive work just as it can in the browser UI. Because there is no UI
session, sub-agents spawned from batch mode do not create child sessions.

## Session persistence

Every batch run leaves the same reviewable trail on disk as a UI session,
under `~/.eitri/sessions/<id>/` (or `$EITRI_DIR/sessions/<id>/`):

```
~/.eitri/sessions/<id>/
├── session.json              ← snapshot (id, title, status, messages, system prompt, workspace)
├── timeline/<ts>.json        ← per-run timeline with termination reason
└── traces/<trace_id>.json    ← one HTTP trace per LLM call
```

- `session.json` is written after each complete agent turn and again on exit
  (`idle` on success, `error` on failure), in the exact snapshot shape UI
  sessions use — so the usual `jq`/`cat` inspection, on-demand session load,
  and session report generation work on batch sessions unchanged.
- The session ID defaults to `batch-<unixnano>`. Set `EITRI_BATCH_SESSION_ID`
  to name the session directory yourself (validated: non-empty, no path
  separators, no `..`).
- Traces are drained to disk before the process exits on both success and
  failure paths.
- Retention follows the existing policy: `session.json` is never pruned;
  traces and timelines count toward the global 1 GiB cap. There is no opt-out
  — `EITRI_DIR` already redirects storage. See ADR-0023.

## Caveats

- **No confirmations:** Confirmation requests are automatically denied. Ops that require confirmation will return errors to the LLM.
- **No browser session:** The agent cannot open browser tabs or interact with a UI.
- **No SSE/streaming UI:** Output is raw text only. Tool cards, chat bubbles, and the HTMX frontend are not available.
- **Config-driven:** The model, provider, workspace, and system prompt come from the config file. Set `EITRI_CONFIG` to use a non-default config.
- **Single-shot:** Each `eitri -b` invocation runs one prompt and exits. For processing multiple issues in parallel, use the agent loop script (see below).

## Agent loop pattern

For AFK (away-from-keyboard) processing of `ready-for-agent` issues, use `scripts/agent-loop.sh`. It is a **dispatcher** that works on up to `N` issues in parallel:

```bash
./scripts/agent-loop.sh /path/to/repo       # 2 workers (default)
./scripts/agent-loop.sh /path/to/repo -j 4  # 4 workers
```

How it works:

1. **Claim:** Lists the oldest open `ready-for-agent` issues (excluding `in-progress` and `issue-type:parent`), claims up to `-j N` of them, and adds an `in-progress` label to each. The dispatcher is the only process that touches issue state — workers never do, so there is no claim race.
2. **Worktrees:** Fetches `origin/main` and creates one detached git worktree per issue (`.worktrees/issue-N`). Detached HEAD is required because `main` is checked out in the primary worktree.
3. **Workers:** Runs one `eitri -b` worker per worktree, in parallel. Each worker creates a branch, implements the issue, pushes, and opens a PR whose description contains `Closes #N` (so the issue auto-closes on merge). Worker output goes to `.worktrees/issue-N/log` — never interleaved on the terminal. Workers do **not** merge.
4. **Review trail:** Each worker exports `EITRI_BATCH_SESSION_ID=issue-N`, so its batch session is persisted under `~/.eitri/sessions/issue-N/` (session snapshot, HTTP traces, and timeline) instead of an opaque `batch-*` directory. After a batch, every processed issue has a reviewable per-issue session directory for post-run inspection with the same tools that work on UI sessions (`jq`, `cat`).
5. **Merge queue:** After all workers finish, the dispatcher merges PRs one at a time (`gh pr merge --squash --delete-branch`), rebasing each PR branch onto the latest `origin/main` first. If a rebase conflicts, the dispatcher spawns a focused `eitri -b` resolution run inside that worktree, capped at 3 attempts per PR; past the cap the PR is left open with a comment and the dispatcher moves on. Merging is serialized because two concurrent merges would race (the second PR goes stale and GitHub refuses to merge).
6. **Cleanup:** Worktrees are removed on success and on crash (via `trap`). On startup the dispatcher removes stale `in-progress` labels whose worktree no longer exists.
7. **Stopping:** Ctrl+C (or SIGTERM) stops claiming new issues after the current batch finishes — in-flight workers and the merge queue run to completion, then the script exits. A second signal forces an immediate exit. Workers are spawned via `setsid --wait` so the terminal signal only reaches the dispatcher; without `setsid` a Ctrl+C also kills the workers.

The dispatcher reports per-issue worker exit status and continues past failures; it exits 0 only if nothing was left unmerged or orphaned.

## Exit codes

| Code | Meaning                                      |
|------|----------------------------------------------|
| 0    | Success — agent loop completed, or the run was cancelled via context |
| 1    | Any batch failure — config load failure, auth failure, or run error |
