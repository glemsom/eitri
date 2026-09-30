---
name: subagents
description: "Subagents: hand a task to its own `eitri -b` agent. Use when a prompt asks for a 'background agent', a sub-agent, a second agent, or several independent tasks in parallel."
model-invocable: true
---

## Subagents

A 'background agent' is a subagent: a batch-mode `eitri -b` run with its own
session and sandbox, launched from the parent's Bash tool call.

**Launch** each subagent into its own execution directory, then **collect** it —
wait, and read the answer — inside the same Bash tool call. The one script
covers one or many:
```sh
for task_number in 1 2; do
  task="<task $task_number>"
  agent_dir=$(mktemp -d "$TMPDIR/subagent.XXXXXX")
  EITRI_DIR="$agent_dir" EITRI_CONFIG="${EITRI_CONFIG:-$HOME/.eitri/config.json}" \
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

One subagent is both ranges set to `1`. Collecting inside the same Bash tool call
is what makes the results there. Uncollected subagents are lost: in sandboxed mode
the sandbox terminates child processes when the tool call returns; elsewhere they
merely keep running.

The gate is load-bearing. Each run prints one `--format json` envelope whose
`answer` field is the result; a failed run prints no envelope and exits
non-zero, and bare `jq -r` on that empty file prints nothing while exiting 0 — a
crash would read as an answer with nothing in it.

## Handoff

Piped stdin is the cheap channel — `git diff | eitri -b "Review this diff"` —
and carries up to 1 MiB, refused (never truncated) past that. Reach for the
workspace when the payload is larger, or when the subagent has to write a file
back.

For files, use the **workspace**: the one path writable from both sides. A
subagent takes its launch directory as its workspace, so launch from the
workspace and never from `$TMPDIR` — launching from `$TMPDIR` hands every
subagent the parent's scratch read-write, silently, with no failed write to show
for it. Pass workspace paths in the task prompt for anything crossing the
boundary.

Each run leaves its session trail at `$agent_dir` — the parent's to read, on a
path minted per run, with an implementation-detail layout inside. Hand work
across the boundary as workspace paths, not as locations in it, and replay a run
by pointing `eitri session show` at the data directory that run used:
```sh
EITRI_DIR="$agent_dir" eitri session show "$(jq -r .session "$TMPDIR/sa-1.json")"
```
