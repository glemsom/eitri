---
name: subagents
description: Run parallel or background subagent tasks with isolated batch runs, waiting for each and reading settled results in the same Bash call.
model-invocable: true
---

## Subagents

For each batch-mode subagent, use an isolated execution directory and always
wait for it before reading the result. The same pattern works for one or
many subagents:
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

Each subagent run prints one `--format json` envelope; `jq -r .answer` extracts the answer field from it instead of parsing prose stdout.
A run that fails prints **no** envelope and exits non-zero, so gate on both the exit code and
`jq -e` before trusting an answer: bare `jq -r` on the resulting empty file prints nothing and
exits 0, which makes a crashed subagent look like one that answered with nothing.

Change both ranges to `1` for one subagent. Always launch, wait for every
process, and read the results in the same Bash tool call: in sandboxed mode
the sandbox terminates child processes when the tool call returns; elsewhere
they merely keep running — waiting and reading within the same call is what
guarantees the results are there to collect.

## File handoff

Hand files between the parent and a subagent through the **workspace** — the
one path writable from both sides. Each subagent runs in an isolated sandbox
with its own writable `$TMPDIR`, and the parent's `$TMPDIR` is read-only from
inside the subagent, so a subagent cannot reach into the parent's scratch.

The reverse does not hold: `mktemp -d "$TMPDIR/subagent.XXXXXX"` puts the agent
directory under the parent's `$TMPDIR`, so the parent can read a subagent's
transcript at `$agent_dir/sessions/<guid>/`. That is the subagent's private
scratch rather than a handoff channel — the path is minted fresh per run and
its layout is an implementation detail — so pass workspace paths in the task
prompt for anything that has to cross the boundary. Those `subagent.*`
directories also outlive the run, so remove them once the batch is collected.

To replay a subagent, point `eitri session show` at the same data directory the
run used, or it reports no records:

```sh
EITRI_DIR="$agent_dir" eitri session show "$(jq -r .session "$TMPDIR/sa-1.json")"
```
