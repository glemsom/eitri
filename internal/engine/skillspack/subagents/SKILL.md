---
name: subagents
description: "Offload, delegate, or parallelize: run bulky or independent work in fresh eitri subagents."
model-invocable: true
---

## Subagents

A subagent starts with an empty context: it cannot see this conversation, ask you anything, or recover from a vague brief.

- **Write a self-contained prompt**: absolute paths, the goal, and the exact shape of the answer you want (table, JSON, one line per match). Ask for the deliverable, never the process.
- **Bound the answer**: "answer in under 400 words". When the full result is bulky, ask for it in a scratch file the repo ignores (`.scratch/`), plus a short summary and that path — then grep the file instead of reading it whole.
- **Stay inline** when one command answers the question: a subagent costs seconds and tokens to save neither.

### Launch and collect

Each `eitri -b` needs its own `EITRI_DIR`, and the parent config must be captured **before** you override it. Launch every task in the background, then wait and read all of them in the same bash call — that call's end kills its children in the sandbox (`--die-with-parent`), though not when running unsandboxed — so raise the call's `timeout` past the slowest expected subagent, and redirect stdin from `/dev/null` so no child drains a pipe meant for someone else.

```sh
parent_config="${EITRI_CONFIG:-${EITRI_DIR:-$HOME/.eitri}/config.json}"
sa="$TMPDIR/sa"; mkdir -p "$sa"
tasks=(
  "<task 1: absolute paths, goal, answer shape, word cap>"
  "<task 2>"
)
for i in "${!tasks[@]}"; do
  mkdir -p "$sa/$i"
  EITRI_DIR="$sa/$i" EITRI_CONFIG="$parent_config" \
    eitri -b "${tasks[$i]}" --format json >"$sa/$i.json" 2>"$sa/$i.err" </dev/null &
  pids[$i]=$!
done
for i in "${!tasks[@]}"; do
  status=0
  wait "${pids[$i]}" || status=$?
  if [ "$status" -eq 0 ] && jq -e .answer "$sa/$i.json" >/dev/null; then
    echo "=== $i ==="; jq -r .answer "$sa/$i.json" | tee "$sa/$i.md"
  else
    echo "=== $i failed (exit $status) ===" >&2; cat "$sa/$i.err" >&2
  fi
done
```

One task, one array element. `--format json` prints a single envelope whose `.answer` is the deliverable; a failed run exits non-zero and prints none, which the status and `jq -e` checks catch. A failure means the run never finished: read `.err`, retry once, then do that slice inline. Your own stdout is compressed downstream, so a long answer loses its tail.

For data rather than a prompt, pipe it: `git diff | eitri -b "<task>" --format json` appends the stream as input context, capped at 1 MiB. The child's `$TMPDIR` is not yours — files you share go in the workspace.
