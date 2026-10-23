---
name: subagents
description: "Offload bulky or independent work to a fresh Eitri agent so its legwork stays out of your context. Use when the answer matters more than the work's detail, or independent tasks can run in parallel."
model-invocable: true
---

## Subagents

Use a subagent to offload work whose output matters but whose process or volume would pollute this context. Give it a bounded task and request the complete useful result in its final answer. Keep work inline when one quick command answers it.

### Launch and collect

Run each `eitri -b` in the current workspace (the subagent's working directory) and give it a unique `EITRI_DIR` for its session data. Preserve the parent's config: capture it **before** setting `EITRI_DIR`. Launch independent tasks in the background, then wait for and read every result in the same Bash call; child processes may be killed when that call ends.

```sh
parent_config="${EITRI_CONFIG:-${EITRI_DIR:-$HOME/.eitri}/config.json}"
sa_dir="$TMPDIR/sa"
mkdir -p "$sa_dir"
declare -a pids
for n in 1 2; do
  agent_dir="$sa_dir/$n"
  mkdir -p "$agent_dir"
  EITRI_DIR="$agent_dir" EITRI_CONFIG="$parent_config" \
    eitri -b "<bounded task $n; include the complete result in your final answer>" --format json \
    > "$sa_dir/$n.json" 2> "$sa_dir/$n.err" &
  pids[$n]=$!
done
for n in 1 2; do
  status=0
  wait "${pids[$n]}" || status=$?
  if [ "$status" -eq 0 ] && jq -e .answer "$sa_dir/$n.json" >/dev/null; then
    jq -r .answer "$sa_dir/$n.json" | tee "$sa_dir/$n.md"
  else
    echo "subagent $n failed (exit $status):" >&2
    cat "$sa_dir/$n.err" >&2
  fi
done
```

For one task, change both loop bounds to `1`; add a loop iteration for each task. `--format json` emits one envelope; `.answer` is the deliverable. The success and JSON checks prevent a failed run from looking like an empty answer. Keep requested answers concise: stdout is compressed, so a report printed there can lose its tail.

For data rather than a task prompt, pipe it into `eitri -b "<task>" --format json`; stdin is appended as input context, up to 1 MiB. Use the shared workspace for files the subagent must read or write, and name those paths in its prompt.
