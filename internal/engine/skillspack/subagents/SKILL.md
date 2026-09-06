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
  wait "${pids[$task_number]}"
  echo "=== subagent $task_number exit=$? ==="
done
echo "=== answers ==="
for task_number in 1 2; do
  jq -r .answer "$TMPDIR/sa-$task_number.json"
done
```

Each subagent run prints one `--format json` envelope; `jq -r .answer` extracts the answer field from it instead of parsing prose stdout.

Change both ranges to `1` for one subagent. Always launch, wait for every
process, and read the results in the same Bash tool call: in sandboxed mode
the sandbox terminates child processes when the tool call returns; elsewhere
they merely keep running — waiting and reading within the same call is what
guarantees the results are there to collect.

## File handoff

Hand files between the parent and a subagent through the **workspace** — the
only path writable from both sides. Each subagent runs in an isolated sandbox
with its *own* writable `$TMPDIR`; the parent's `$TMPDIR` is read-only from
inside the subagent, and a subagent's scratch files are unreachable by the
parent after the run. So pass workspace paths in task prompts for any file
that crosses the boundary, and let each side keep private scratch in its own
`$TMPDIR`.
