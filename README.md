# Eitri

Eitri is a self-hosted AI coding agent for Linux. It runs as a single Go binary, uses natural-language prompts to read, edit, and run code in a workspace, and connects to local or hosted model providers.

Eitri keeps its system prompt small, uses Unix tools through `bash`, and stores configuration and sessions locally under `~/.eitri`.

## Install and run

Requirements: Linux, Go for building, and the runtime tools listed below.

```sh
make build
./bin/eitri
```

The first launch opens settings for provider and credential configuration. Install the binary with `make install` (to `~/.local/bin/eitri`).

## Usage

```sh
eitri                         # interactive TUI
eitri -b "Review this diff"   # one batch run
git diff | eitri -b "Review this diff"
eitri session list             # list saved sessions
eitri session show <guid>
eitri session talk <guid>
eitri session grep <pattern> [guid|all]
eitri --version
```

Batch mode reads only piped, non-TTY stdin and appends it as fenced context. Input is limited to 1 MiB. `--format json` emits one JSON object containing `answer`, `session`, `turns`, and `stopped`; `-v` sends reasoning to stderr. See [docs/batch-mode.md](docs/batch-mode.md) for the input, output, and exit-code contract.

Useful flags:

| Flag | Purpose |
| --- | --- |
| `-d` | Write full provider HTTP traces to the session. |
| `--yolo-unsafe` | Disable bubblewrap; `bash` runs with the user's full permissions. |
| `--pprof <addr>` | Enable localhost pprof diagnostics. `--pprof-mutex` and `--pprof-block` add profiles. |
| `--format text\|json` | Select batch output format. |

The TUI's `/help` is the authoritative reference for keybindings and slash commands. `/settings`, `/login`, and `/new` are available there.

## Safety

By default, every `bash` command runs in a bubblewrap cage with a read-only root, writable workspace and session temporary directory, and isolated PID, `/proc`, and `/dev` namespaces. `--yolo-unsafe` removes this cage and must only be used with trusted prompts and workloads.

Commands are time-limited: 120 seconds by default, configurable per call up to 3600 seconds.

## Configuration and data

`EITRI_DIR` changes the data directory (default `~/.eitri`). `EITRI_CONFIG` changes the config path (default `<data directory>/config.json`). The data directory contains configuration, sessions, transcripts, and materialized builtin skills.

Supported providers are `opencode-go`, `github-copilot`, and `custom-openai`. Settings can be changed in the TUI; credentials are stored in the local config.

Important config keys include `provider`, `model`, `reasoning_effort`, `thinking_enabled`, `max_turns` (default `250`), `context_overflow_recovery`, `extra_writable_paths`, `theme`, and `rail_width`. Eitri manages provider credential objects in the config. Do not commit this file.

Sessions are append-only and can be inspected with the `session` commands. See [docs/sessions.md](docs/sessions.md).

## Skills and workspace instructions

Eitri discovers skills in this order: project `.agents/skills`, user `~/.agents/skills`, then builtin skills. A higher-priority skill with the same name shadows a lower-priority one. Builtins currently include `subagents` and `web-access`.

An `AGENTS.md` in the workspace root is loaded as repository instructions. `CONTEXT.md` documents Eitri's internal terminology; `ARCHITECTURE.md` maps the implementation for maintainers and agents.

## Build and test

```sh
make build
make test
git diff | eitri -b "Review this diff"
```

## Runtime requirements

Eitri verifies these commands before starting (unless `--yolo-unsafe` removes the `bwrap` requirement): `bwrap`, `bash`, `rg`, `curl`, `lynx`, `patch`, `python3`, `git`, `jq`, and `xdg-open`.

Debian/Ubuntu:

```sh
sudo apt install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
```

Fedora and Arch package managers provide the same package names. Core utilities such as `sed`, `awk`, and `diff` are assumed.
