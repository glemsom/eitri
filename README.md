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

The `-b` and `-d` behaviors are wired up in later tickets (T1b, T1c); today the
binary parses them and performs the boot sequence.

## Data directory

On launch Eitri creates its data directory and stores sessions, config, and
debug traces under it (`eitri.md` §2.7):

- `~/.eitri/` by default
- `EITRI_DIR` overrides the data directory
- `EITRI_CONFIG` overrides the config file path

## Sandbox prerequisite

The `bash` tool runs commands inside a bubblewrap (`bwrap`) sandbox. If `bwrap`
is absent at launch, Eitri exits non-zero with an install-bubblewrap message —
it never degrades to unsandboxed execution (ADR-0001 decision 3).
