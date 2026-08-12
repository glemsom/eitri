# Eitri

A self-hosted, single-binary AI coding agent for Linux — the Norse blacksmith
who forged Mjölnir. Runs entirely on your own machine: reads, writes, and
executes commands inside your workspace, guided by natural-language
conversations.

See `eitri.md` for the product vision, `docs/spec.md` for the build reference,
`CONTEXT.md` for the domain glossary, and `docs/adr/` for architectural
decisions.

## Build

Requires Go 1.26+. Bubblewrap (`bwrap`) is a hard prerequisite at runtime — see
ADR-0001; Eitri never falls back to unsandboxed execution.

```sh
go build .        # produces ./eitri in the workspace
```

## Usage

```sh
eitri [flags]
```

| Flag          | Meaning                                                                  |
| ------------ | ------------------------------------------------------------------------ |
| `-b <prompt>` | Run once in batch mode with the given prompt, emit the answer, and exit. |
| `-v`          | In batch mode, also print the model's thinking/reasoning to stdout.      |
| `-d`          | Debug mode (writes full HTTP traces to/from the provider).               |
| `--version`   | Print the version and exit.                                              |

`-b <prompt>` runs one agent turn through the shared run engine and prints the
final answer to stdout. Inside the turn, the engine dispatches any tool calls
(`bash`, `read`, `write`, `edit`) through the shared tool registry, which runs
`bash` inside the bwrap sandbox and resolves every path through the shared
path-namespace seam (ADR-0002). Tool defs are strict-shaped
(`additionalProperties:false`, all-required; optionals as nullable unions),
re-expressed from one canonical schema per dialect, and the dispatch loop
iterates *all* parallel calls in a turn — validates each against its strict
schema, and routes malformed/truncated arguments back to the model as a
structured `{"INVALID_JSON": ...}` tool result so the model self-corrects
(`docs/spec.md` §2) instead of crashing or silently skipping. Thinking/reasoning is suppressed from batch
output by default (`docs/spec.md` §6); use `-v` to surface it. `-d` enables
debug mode, which attaches the HTTP trace sink to the run session.

## Configuration

On launch Eitri creates its config file with defaults if it is absent and loads
it on startup (`config.json`, `eitri.md` §2.7):

- `~/.eitri/config.json` by default; `EITRI_CONFIG` overrides the path
- `EITRI_DIR` overrides the data directory

Persisted settings: `provider`, `model`, `reasoning_effort` (default `high`),
`max_turns` (default `250`), `compaction_fraction` (default `0.8`), and
`extra_writable_paths`. Provider credentials are delivered via environment:
the OpenCode Go key is read from `OPENCODE_API_KEY`, and `EITRI_PROVIDER_URL`
optionally overrides the Chat-Completions endpoint (defaults to OpenCode Go;
custom OpenAI-compatible endpoints are formalized in T11). The batch engine
injects a fake provider in tests via the `app.Options.Provider` seam.

## Data directory

On launch Eitri creates its data directory and establishes a GUID session for
the run under it (`eitri.md` §2.7):

- `~/.eitri/` by default; `EITRI_DIR` overrides it
- `~/.eitri/config.json` — persisted configuration
- `~/.eitri/sessions/<GUID>/` — per-run session transcript
- `~/.eitri/sessions/<GUID>/trace-request.http` & `trace-response.http` —
  full HTTP traces, written only when `-d` debug mode is enabled

## Sandbox prerequisite

The `bash` tool runs commands inside a bubblewrap (`bwrap`) sandbox. If `bwrap`
is absent at launch, Eitri exits non-zero with an install-bubblewrap message —
it never degrades to unsandboxed execution (ADR-0001 decision 3).
