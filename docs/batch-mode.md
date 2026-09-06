# Batch mode as a Unix filter

`eitri -b "<prompt>"` turns Eitri into a Unix filter: it reads piped (non-TTY)
stdin as context, writes the answer to stdout, and reports the outcome through
its exit code. This page documents the whole surface a script can rely on: the
piped-stdin rules, the `--format json` envelope schema, and the exit-code
promise.

## Piped stdin as context

Run batch mode with piped stdin and the data rides to the model as a fenced
block appended **after** the prompt, introduced by a `Stdin input:` header:

```sh
git diff | eitri -b "Review this diff"
```

What the model effectively receives is the prompt, then a fenced block declared
as input:

````text
<prompt>

Stdin input:
```
<data>
```
````

The block is declared as input, never as instructions, so upstream data can't
be mistaken for a command to follow. The data is appended verbatim (a missing
trailing newline is added so the closing fence starts on its own line).

The rules a pipeline can rely on:

- **Only non-TTY stdin is read.** Pipes and file redirects carry context;
  terminal input is never read or drained. A character device such as
  `/dev/null` is not a pipe, so `eitri -b "<prompt>" < /dev/null` is
  byte-identical to running without stdin.
- **Empty stdin appends nothing.** The prompt is sent unchanged; no empty
  `Stdin input:` block is created.
- **Input over 1 MiB is refused, never truncated.** Piping more than 1 MiB of
  context fails the run before the model is reached (`piped stdin exceeds the
  1 MiB batch-context cap; refusing rather than truncating`, exit 1). A silent
  truncation could hand the model a partial diff and get a confident answer on
  data it never saw.
- **Piped stdin without `-b` refuses to launch.** Stdin piped into an
  interactive launch would be silently drained by a TUI that never reads it, so
  Eitri refuses instead (exit 1) with a pointer at the fix:
  `stdin is piped but no batch prompt was given; run eitri -b "<prompt>" to pipe data in as context — piped stdin is never silently drained`.
  This is the standing promise: piped data is never silently consumed.

## `--format json` envelope

`--format json` replaces the plain-text answer on stdout with **exactly one
JSON object** at run end:

```json
{"answer":"Hello world","session":"16e7bf03b41ff71636c0e08956c59673","turns":1,"stopped":false}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `answer` | string | The final answer text. |
| `session` | string | The run's session GUID — names the session directory under `sessions/` in the data directory, usable with `eitri session show <guid>`. |
| `turns` | number | Provider request/response cycles the run performed (tool-calling turns included). |
| `stopped` | bool | Whether the run ended in a user stop rather than a normal completion. Batch binds no stop, so an envelope printed by a batch run always has `stopped: false`; a stopped or failed run exits non-zero and prints no envelope. |

Stdout carries nothing but this envelope — no banner, no progress, no thinking.
Everything else routes to stderr:

- `-v` thinking is streamed to stderr under both formats, so `--format json -v`
  keeps stdout machine-parseable.
- Refusals and errors are written to stderr as `eitri: <error>`, and the
  process exits 1.

An unknown `--format` value is rejected before anything boots:
`eitri: unknown --format "xml" (want: text, json)`, exit 1.

## Exit-code promise

Exit codes stay minimal and stable:

- **`0`** — the run answered: it reached a final answer and printed it (text or
  JSON envelope).
- **`1`** — everything else: refusals (oversized stdin, piped stdin without
  `-b`, unknown `--format`, missing declared toolset) and failures (provider
  errors, max-turn cap, run errors).

For callers that need more than 0/1, the JSON envelope carries `turns` and
`stopped`.

## Pipe examples

Review a diff, answer as text:

```sh
git diff | eitri -b "Review this diff for bugs and style"
```

Review a diff and extract just the answer:

```sh
git diff | eitri -b "Review this diff" --format json | jq -r .answer
```

Summarize a log excerpt, keeping the reasoning out of the answer stream:

```sh
tail -n 500 app.log | eitri -b "Summarize what went wrong" --format json -v 2>thinking.log | jq -r .answer
```

Branch on the outcome — exit 0 means answered, 1 means refused or failed:

```sh
if git diff | eitri -b "Review this diff" --format json > review.json; then
  jq -r .answer review.json
else
  echo "review failed" >&2
fi
```

Capture the session GUID for later replay:

```sh
git diff | eitri -b "Review this diff" --format json > review.json
eitri session show "$(jq -r .session review.json)"
```
