# Eitri

Eitri is a self-hosted AI coding agent for GNU/Linux. It runs as a single Go binary, uses natural-language prompts to read, edit, and run code in a workspace, and connects to local or hosted model providers.

## Install and run

Requirements are GNU/Linux, Go to build Eitri, and the [runtime tools](#runtime-requirements).

```sh
make build
./bin/eitri
```

The first launch opens settings for provider and credential configuration. Install the binary with:

```sh
make install                 # installs to ~/.local/bin/eitri
eitri
```

## Usage

```sh
eitri                         # interactive TUI
eitri -b "Review this diff"   # one batch run
git diff | eitri -b "Review this diff"
eitri session list
eitri session show <guid>
eitri session talk <guid>
eitri session grep <pattern> [guid|all]
eitri --version
```

Batch mode accepts only piped, non-TTY stdin and appends it as fenced context. Input is limited to 1 MiB. `--format json` emits one object containing `answer`, `session`, `turns`, and `stopped`; `-v` sends reasoning to stderr. See [docs/batch-mode.md](docs/batch-mode.md) for the input, output, and exit-code contract.

Useful flags:

| Flag | Purpose |
| --- | --- |
| `-d` | Record full provider HTTP traces in the session. |
| `--yolo-unsafe` | Disable bubblewrap; `bash` runs with the user's full permissions. |
| `--pprof <addr>` | Enable localhost pprof diagnostics. `--pprof-mutex` and `--pprof-block` add profiles. |
| `--format text\|json` | Select batch output format. |

The TUI's `/help` is the authoritative reference for keybindings and slash commands. `/settings`, `/login`, and `/new` are available there.

## Safety

By default, every `bash` command runs in a bubblewrap sandbox with a read-only root, writable workspace and session temporary directory, and isolated PID, `/proc`, and `/dev` namespaces. `--yolo-unsafe` removes the sandbox and must only be used with trusted prompts and workloads.

The whole host root is bound read-only, so `$HOME` is readable but not writable. Tools that keep state there — `helm`, `terraform`, `az`, `aws`, `docker` — need their paths added to `extra_writable_paths` in the config, or the **Writable paths** field in `/settings`. Eitri tells the model the resolved writable set, so it stops guessing where the boundary is. See [docs/sandbox.md](docs/sandbox.md) for the full boundary, the escape hatch, and its sharp edges.

Commands are limited to 120 seconds by default; each call can request up to 3600 seconds.

## Providers and local data

Supported providers are `opencode-go`, `github-copilot`, and `custom-openai`. Configure the provider, credentials, and model in the TUI's `/settings`; provider-specific login is available through `/login` where applicable.

Eitri stores configuration, credentials, sessions, transcripts, and materialized builtin skills under `~/.eitri`. Set `EITRI_DIR` to change the data directory, or `EITRI_CONFIG` to change the config path (default: `<data directory>/config.json`). Do not commit the config file.

Sessions are append-only and can be inspected with the `session` commands. See [docs/sessions.md](docs/sessions.md).

## Skills and workspace instructions

Skills are discovered in project `.agents/skills`, user `~/.agents/skills`, then builtin skills. A same-named skill in a higher-priority scope shadows lower-priority scopes. Builtins currently include `subagents` and `web-access`.

An `AGENTS.md` in the workspace root is loaded as repository instructions. [CONTEXT.md](CONTEXT.md) defines internal terms; [ARCHITECTURE.md](ARCHITECTURE.md) maps implementation boundaries.

## Build and test

```sh
make build
make test
git diff | eitri -b "Review this diff"
```

## Runtime requirements

Eitri verifies these commands before starting, unless `--yolo-unsafe` removes the `bwrap` requirement:

`bwrap`, `bash`, `rg`, `curl`, `lynx`, `patch`, `python3`, `git`, `jq`, and `xdg-open`.

Debian/Ubuntu:

```sh
sudo apt install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
```

Fedora and Arch provide the same package names. Core utilities such as `sed`, `awk`, and `diff` are assumed.
