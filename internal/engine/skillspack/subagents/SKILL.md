---
name: subagents
description: "Subagents: hand a task to its own `eitri -b` agent. Use when a prompt asks for a 'background agent', a sub-agent, a second agent, or several independent tasks in parallel."
model-invocable: true
---

## Subagents

A 'background agent' is a subagent: a batch-mode `eitri -b` run with its own
session and sandbox, launched from the parent's Bash tool call.

**Launch** each subagent into its own execution directory, then **collect** it —
wait, and read the answer — inside the same Bash tool call. The subagent takes
its own data directory and the parent's config:
```sh
# Resolve the parent's config before repointing EITRI_DIR: assigned in the same
# command, EITRI_DIR would make this fallback resolve to the fresh agent dir.
parent_config="${EITRI_CONFIG:-${EITRI_DIR:-$HOME/.eitri}/config.json}"
for task_number in 1 2; do
  task="<task $task_number>"
  agent_dir=$(mktemp -d "$TMPDIR/subagent.XXXXXX")
  EITRI_DIR="$agent_dir" EITRI_CONFIG="$parent_config" \
    eitri -b "$task" --format json > "$TMPDIR/sa-$task_number.json" 2> "$TMPDIR/sa-$task_number.err" &
  pids[$task_number]=$!
done
for task_number in 1 2; do
  status=0
  wait "${pids[$task_number]}" || status=$?
  if [ "$status" -eq 0 ] && jq -e .answer "$TMPDIR/sa-$task_number.json" >/dev/null; then
    echo "=== subagent $task_number ==="
    jq -r .answer "$TMPDIR/sa-$task_number.json"
  else
    echo "=== subagent $task_number failed (exit $status) ===" >&2
    head -n 5 "$TMPDIR/sa-$task_number.err" >&2
  fi
done
```

For a single subagent, set both ranges to `1`. In sandboxed mode the sandbox
terminates child processes when the tool call returns; elsewhere uncollected
runs merely keep running, their answers unread. Collect inside that call.

Each run prints one `--format json` envelope, whose `answer` field is the
result; a failed run prints no envelope and exits non-zero. Bare `jq -r` on
that empty file prints nothing while exiting 0, so a crash would read as an
empty answer — the script's `jq -e .answer` gate is what tells them apart.

## Handoff

Piped stdin is the cheap channel — `git diff | eitri -b "Review this diff"` —
and carries up to 1 MiB, refused (never truncated) past that. Reach for the
workspace when the payload is larger, or when the subagent has to write a file
back.

The **workspace** is the one path writable from both sides: a subagent takes
its launch directory as its workspace. Launch from the workspace, never from
`$TMPDIR` — launching there hands every subagent the parent's scratch
read-write, silently, with no failed write to show for it. Pass workspace paths
in the task prompt for anything crossing the boundary.

Each run leaves its session trail at `$agent_dir`, a directory minted per run,
its layout an implementation detail. Replay a run with `eitri session show`
pointed at the data directory that run used:
```sh
EITRI_DIR="$agent_dir" eitri session show "$(jq -r .session "$TMPDIR/sa-1.json")"
```
