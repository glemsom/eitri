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
| ------------- | ------------------------------------------------------------------------ |
| `-b <prompt>` | Run once in batch mode with the given prompt, emit the answer, and exit. |
| `-d`          | Debug mode (writes full HTTP traces to/from the provider).               |
| `--version`   | Print the version and exit.                                              |

The `-b` behavior is wired up in a later ticket (T1c); today the binary parses
the batch prompt and performs the boot sequence. `-d` enables debug mode, which
attaches the HTTP trace sink to the run session.

## Configuration

On launch Eitri creates its config file with defaults if it is absent and loads
it on startup (`config.json`, `eitri.md` §2.7):

- `~/.eitri/config.json` by default; `EITRI_CONFIG` overrides the path
- `EITRI_DIR` overrides the data directory

Persisted settings: `provider`, `model`, `reasoning_effort` (default `high`),
`max_turns` (default `250`), `compaction_fraction` (default `0.8`), and
`extra_writable_paths`. Provider credentials are delivered via environment
(wired in later provider tickets).

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
