# Batch mode (`-b` flag)

Eitri supports a headless batch mode via the `-b` flag. Instead of starting the HTTP server and browser UI, it runs a single prompt against the agent loop and streams the output to stdout.

## Usage

```bash
eitri -b "implement the feature in issue #42"
eitri --persona reviewer -b "review the code in PR #123"
eitri -b implement the feature in issue #42
```

The `-b` flag takes a prompt; the full prompt is the `-b` value joined with all
remaining command-line arguments after the flag, so quoting is optional —
`eitri -b implement feature X` runs the agent with the prompt "implement
feature X". An empty or whitespace-only prompt (`eitri -b ""`, `eitri -b "   "`)
is rejected with a clear error and a non-zero exit code instead of starting the
UI server. Output is streamed token-by-token to stdout as plain text (no SSE,
no tool cards, no HTML). For reasoning models, thinking/reasoning deltas are
streamed to stdout as they arrive, delimited by `[thinking]` and `[/thinking]`
markers so the reasoning content is distinguishable from the final text —
models without reasoning produce no markers and their output is unchanged.

The optional `--persona` flag overrides the active persona for the batch run only — it does not change `config.json`.

**Precedence (highest to lowest):**
1. `--persona` CLI flag — overrides everything for this run
2. `active_persona` in `config.json` — persistent per-workspace persona
3. `"generic"` fallback — built-in default when neither is set

The persona is resolved from the workspace `.eitri/personas/` directory, falling back to `~/.eitri/personas/`. If the persona file does not exist, the run fails with a clear error.

## How it works

1. Loads config from `EITRI_CONFIG` env var or `~/.eitri/config.json`
2. Builds a minimal `RunService` — no UI session manager, no browser — but
   wires the same skills service as the UI, so Agent Skills work identically
   in batch mode
3. Calls `BatchRun` which runs the agent loop with a request-based history manager
4. Streams text tokens and reasoning/thinking deltas to stdout in real-time. Ordinary text is written as plain tokens; reasoning content is wrapped in `[thinking]`…`[/thinking]` markers (tool calls execute silently — only final text and thinking are streamed)
5. Exits with code 0 on success, non-zero on failure

Sub-agents are supported in batch mode: the `delegate` and `collect` tools are
registered for headless runs, so the agent can spawn sub-agents for
data-intensive work just as it can in the browser UI. Because there is no UI
session, sub-agents spawned from batch mode do not create child sessions.

## Agent Skills

Batch runs assemble their tool registry, LLM request, and system prompt
through the same run-preparation seam as UI runs (ADR-0024), so skills behave
identically:

- The skills catalog is emitted into the system prompt.
- A persona with required skills emits the `<required_skills>` directive and
  the agent loads each skill via the `skill` tool on its first turn — the
  loaded skill content flows into the conversation exactly as in the UI.
- `max_output_tokens`, the session-scoped prompt-cache key, and the thinking
  level are applied to batch LLM requests just as in the UI.
- Browser-tool connections are released when the run ends, and a panic inside
  the batch agent loop writes a crash dump to `~/.eitri/crash-dump/`.

The only tool differences from a UI run are mode-specific: batch has no
`render_quick_replies` (no UI to render chips into), confirmations are
auto-denied, and output is plain text to stdout instead of SSE.

## Auto-compaction

Batch runs compact their conversation history exactly like UI runs. After each
complete agent turn the batch turn completer persists its per-turn snapshot
and then runs the same shared auto-compaction step the UI uses, so long batch
runs stay within the context window instead of overflowing and failing where
the same prompt would succeed in the UI. The compaction settings in
`~/.eitri/config.json` (`compaction_enabled`, `compaction_threshold_percent` /
`compaction_low_water_percent`, `compaction_message_size_threshold`,
`compaction_tool_call_retention_turns`, `compaction_salience_enabled`) are
honored identically in both modes. When a batch run's history exceeds the
high-water mark it is compacted below the low-water mark, and the compacted
history is reflected in the on-disk `session.json` snapshot (a second snapshot
is written after compaction). Sub-agents spawned from batch mode auto-compact
too: they inherit the parent's context window and compaction settings, run the
same shared compaction step after each turn, and their child-session snapshots
under `~/.eitri/sessions/<taskID>/` reflect the compacted history.

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
- The session ID is auto-generated by the shared run-ID helper (same as UI
  and sub-agent runs — ADR-0025). The run reports its `session_id` in its
  output, and `~/.eitri/sessions/<id>/` is findable via the session-report
  path, so there is no need to pre-name the directory.
- Traces are drained to disk before the process exits on both success and
  failure paths.
- Retention follows the existing policy: `session.json` is never pruned;
  traces and timelines count toward the global 1 GiB cap. There is no opt-out
  — `EITRI_DIR` already redirects storage. See ADR-0023.

## Caveats

- **No confirmations:** Confirmation requests are automatically denied. Ops that require confirmation will return errors to the LLM.
- **No browser session:** The agent cannot open browser tabs or interact with a UI.
- **No SSE/streaming UI:** Output is raw text only. Tool cards, chat bubbles, and the HTMX frontend are not available. Reasoning/thinking content from reasoning models is delimited with `[thinking]`…`[/thinking]` markers on stdout.
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
4. **Review trail:** Each worker's batch session is persisted under an auto-generated `~/.eitri/sessions/<id>/` (session snapshot, HTTP traces, and timeline), same layout as a UI session. The worker's log carries its `session_id`; after a batch, every processed issue has a reviewable session directory for post-run inspection with the same tools that work on UI sessions (`jq`, `cat`, session report).
5. **Merge queue:** After all workers finish, the dispatcher merges PRs one at a time (`gh pr merge --squash --delete-branch`), rebasing each PR branch onto the latest `origin/main` first. If a rebase conflicts, the dispatcher spawns a focused `eitri -b` resolution run inside that worktree, capped at 3 attempts per PR; past the cap the PR is left open with a comment and the dispatcher moves on. Merging is serialized because two concurrent merges would race (the second PR goes stale and GitHub refuses to merge).
6. **Cleanup:** Worktrees are removed on success and on crash (via `trap`). On startup the dispatcher removes stale `in-progress` labels whose worktree no longer exists.
7. **Stopping:** Ctrl+C (or SIGTERM) stops claiming new issues after the current batch finishes — in-flight workers and the merge queue run to completion, then the script exits. A second signal forces an immediate exit. Workers are spawned via `setsid --wait` so the terminal signal only reaches the dispatcher; without `setsid` a Ctrl+C also kills the workers.

The dispatcher reports per-issue worker exit status and continues past failures; it exits 0 only if nothing was left unmerged or orphaned.

### Verdict plumbing (`run_persona_batch`, `extract_verdict`)

`agent-loop.sh` ships two reusable helpers for the review-gated pipeline (T3,
#1188) so the build→test→review gates (T4/T5) and any future gate share one
invocation path:

- `run_persona_batch <wt> <stage> <persona> <prompt>` runs `eitri --persona
  <persona> -b "<prompt>"` inside the worktree as a **fresh batch** (fresh
  context = objective evaluation), streaming output to `$wt/log.$stage` with the
  same `setsid --wait` shield and 130/143 Ctrl+C loop the worker phase uses. It
  prints three lines: the verdict (`APPROVED | CHANGES_REQUIRED | BLOCKED |
  hard-fail`), the batch's exit status, and the stage log path.
- `extract_verdict <log>` parses the **latest** `VERDICT: ...` line out of a log,
  returning just the verdict name, or empty when there is none.

A non-zero exit **or** a missing `VERDICT:` line both surface as `hard-fail` —
a missing verdict or an auth/config/lock error never becomes a blind retry. The
helper only runs a batch and reports a verdict; it does not decide loop policy
(the shared 3-round cap / re-entry / merge precondition live in T6). Both
functions live in `agent-loop.sh`, which is guard-claused so sourcing it (e.g.
from a Go test, or from T4/T5) defines the helpers without starting the
dispatcher.

## Exit codes

| Code | Meaning                                      |
|------|----------------------------------------------|
| 0    | Success — agent loop completed, or the run was cancelled via context |
| 1    | Any batch failure — config load failure, auth failure, or run error |
