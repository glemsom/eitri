---
name: subagents
description: "Outsource part of your own workload to a subagent — a batch `eitri -b` run with its own session and sandbox that hands back an answer. Use when the work splits into independent pieces, when bulky or noisy legwork would flood your context, when something slow (a build, a test run, a sweep) can overlap other work, or when an independent second opinion is wanted — even if the prompt never says 'subagent' or 'background agent'."
model-invocable: true
---

## Subagents

Reach for a subagent when delegating pays for the launch: several pieces that
don't depend on each other; legwork whose *output* matters but whose volume
would flood this context (a wide search, a large diff, a long log); a slow
command to overlap with other work; or a judgement you want a second agent to
reach independently. Keep it inline when one shell call would answer it — the
launch costs a session, and the subagent sees only what the prompt says.

A 'background agent' is a subagent: a batch-mode `eitri -b` run with its own
session and sandbox, launched from the parent's Bash tool call.

**Launch** each subagent into its own execution directory, then **collect** it —
wait, and read the answer — inside the same Bash tool call. The subagent takes
its own data directory and the parent's config, and both the directory and the
envelope are named for the task, so run N and report N are the same task:
```sh
# Resolve the parent's config before repointing EITRI_DIR: assigned in the same
# command, EITRI_DIR would make this fallback resolve to the fresh agent dir.
parent_config="${EITRI_CONFIG:-${EITRI_DIR:-$HOME/.eitri}/config.json}"
sa_dir="$TMPDIR/sa"
mkdir -p "$sa_dir"
for task_number in 1 2; do
  task="<task $task_number>"
  agent_dir="$sa_dir/$task_number"
  mkdir -p "$agent_dir"
  EITRI_DIR="$agent_dir" EITRI_CONFIG="$parent_config" \
    eitri -b "$task" --format json > "$sa_dir/$task_number.json" 2> "$sa_dir/$task_number.err" &
  pids[$task_number]=$!
done
for task_number in 1 2; do
  status=0
  wait "${pids[$task_number]}" || status=$?
  if [ "$status" -eq 0 ] && jq -e .answer "$sa_dir/$task_number.json" >/dev/null; then
    echo "=== subagent $task_number ==="
    jq -r .answer "$sa_dir/$task_number.json" | tee "$sa_dir/$task_number.md"
  else
    echo "=== subagent $task_number failed (exit $status) ===" >&2
    head -n 5 "$sa_dir/$task_number.err" >&2
  fi
done
```

For a single subagent, use `1` in both ranges. In sandboxed mode the sandbox
terminates child processes when the tool call returns; elsewhere uncollected
runs merely keep running, their answers unread. Collect inside that call.

Each run prints one `--format json` envelope, whose `answer` field is the
result; a failed run prints no envelope and exits non-zero. Bare `jq -r` on
that empty file prints nothing while exiting 0, so a crash would read as an
empty answer — the script's `jq -e .answer` gate is what tells them apart.

## The answer is the deliverable

The `answer` field is the only part of a run that comes back intact, so put the
word budget in the task prompt: **the whole deliverable in the final answer,
under ~800 words, never a report rendered through stdout.** Stdout is
compressed on the way back — long content loses a truncation-marked tail — so a
subagent that `cat`s its report answers with a pointer to text nobody can read.
Dense bullets, not prose.

## Time and failure

Give a research or sweep launch `timeout: 3600`. The default of 120 seconds
bounds the shell call, and when it fires the sandbox kills the subagent
mid-run, leaving an empty envelope.

Read `.err` before re-running: empty or `timed out` means the shell call was cut
short and took the subagent with it, so raise the timeout rather than shrinking
the task; `stream truncated` means the answer overran, so retry with a tighter
word cap; a provider or credential error means the parent's config didn't reach
it. Retry once; then do the slice inline.

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

Reports land at `$TMPDIR/sa/N.md`, outside the subagent's workspace: the answer
is already in hand, and the file serves the later turn that needs one verbatim
passage — `rg` it rather than re-reading a whole report.

Each run also leaves its session trail at `$TMPDIR/sa/N`, whose layout is an
implementation detail except for replay:
```sh
EITRI_DIR="$TMPDIR/sa/1" eitri session show "$(jq -r .session "$TMPDIR/sa/1.json")"
```
Replay is a last resort: a lost deliverable is cheaper to re-earn with a
tighter word cap than to excavate from a raw transcript.
