# Eitri

**Eitri** is a self-hosted, single-binary AI coding agent for Linux. It runs entirely on your machine and your own model credentials — no vendor lock-in, no hosted agent service, nothing leaves your box except the requests you choose to send to your provider.

## Why self-host?

- **Your data stays yours.** Sessions, transcripts, and configuration live under `~/.eitri`. You control where they are and who can read them.
- **One binary.** No runtime, no daemon, no container image — a single static Go binary you can drop anywhere.
- **Your provider, your terms.** Point Eitri at any model or OpenAI-compatible endpoint, from a local model to a cloud provider.
- **Sandboxed by default.** Eitri never runs unsandboxed: every execution is confined by bubblewrap.

> Eitri's internal, agent-facing documentation lives in [`CONTEXT.md`](CONTEXT.md). This README is for humans.

## Quickstart

```sh
# Install the bubblewrap sandbox (required; Eitri never runs unsandboxed):
#   Debian/Ubuntu: sudo apt install bubblewrap
#   Fedora:        sudo dnf install bubblewrap
#   Arch:          sudo pacman -S bubblewrap

make build          # build ./bin/eitri
./bin/eitri         # launch the interactive TUI
```

On first launch you'll be asked to log in to your provider. A single command handles the whole flow:

```sh
eitri              # interactive TUI (recommended for first-time setup)
```

## Usage

### Launch modes

| Command | What it does |
| --- | --- |
| `eitri` | Launch the interactive TUI |
| `eitri -b "<prompt>"` | Run once in batch mode and exit |
| `eitri -v` | In batch mode, print the model's thinking/reasoning to stdout |
| `eitri -d` | Debug mode: write full HTTP traces to/from the provider |
| `eitri --version` | Print the version and exit |

### Sessions

Eitri records every session so you can review, replay, and search past work:

```sh
eitri session list                            # list recorded sessions (GUID, time, cycles, model)
eitri session show <guid> [--turn N]          # compact per-cycle summary
eitri session talk <guid> [--turn N|N-M]      # full conversation as plain text
eitri session grep <pattern> [guid|all]       # find cycles whose messages match
```

Full detail lives in [`docs/sessions.md`](docs/sessions.md).

### In the TUI

- Type a prompt in the **composer** at the bottom and press `enter` to submit.
- Start slash commands with `/` (e.g. `/settings` to open settings).
- Press `?` for the live `/help` reference, which always shows the current bindings.

#### Composer

| Key | Action |
| --- | --- |
| `up` / `down` | Navigate completion candidates; recall a prior/next prompt when the completion list is closed |
| `tab` / `enter` | Accept the highlighted completion |
| `esc` | Close the completion list |
| `tab` | Cycle block focus when the composer is empty |
| `enter` | Submit the draft, or toggle the focused block when empty |
| `shift+enter` | Insert a newline |

#### Navigation

| Key | Action |
| --- | --- |
| `?` | Show help |
| `pgup` / `pgdn` | Scroll history |

#### Panes

| Key | Action |
| --- | --- |
| `e` | Expand all blocks |
| `E` | Collapse all blocks |
| `ctrl+e` | Toggle expanded view |
| `ctrl+x` | Narrow the right pane |
| `ctrl+z` | Widen the right pane |

#### Actions

| Key | Action |
| --- | --- |
| `ctrl+s` | Open settings |
| `ctrl+o` | Copy transcript to clipboard |

#### Slash commands

| Command | Action |
| --- | --- |
| `/settings` | Open the settings panel |
| `/copy` | Copy the transcript to the clipboard |
| `/new` | Start a fresh session (clears this conversation) |
| `/login` | Interactive provider login |
| `/help` | Show this help message |

> The in-TUI `/help` is always available as the live reference and is the authoritative source for keybindings.

## Configuration

### Data directory and paths

| Variable | Purpose | Default |
| --- | --- | --- |
| `EITRI_DIR` | Data directory (sessions, config, transcripts) | `~/.eitri` |
| `EITRI_CONFIG` | Config file path override | `<dataDir>/config.json` |

### `config.json` keys

| Key | Type | Default | Meaning |
| --- | --- | --- | --- |
| `provider` | string | `opencode-go` | Provider backend |
| `model` | string | `deepseek-v4-flash` | Model to use |
| `reasoning_effort` | string | `low` | Reasoning effort level |
| `thinking_enabled` | bool | `true` | Whether the model reasons/uses thinking |
| `cot_collapsed_by_default` | bool | `true` | Render chain-of-thought collapsed until expanded |
| `tool_results_collapsed_by_default` | bool | `true` | Render tool results collapsed until expanded |
| `max_turns` | int | `250` | Maximum turns per run |
| `compaction_fraction` | float | `0.8` | Fraction of the context at which compaction triggers |
| `extra_writable_paths` | array of strings | *(empty)* | Additional paths the agent may write to |
| `theme` | string | `dark` | UI theme |
| `rail_width` | int | `30` | Width of the right rail/pane |
| `copilot` | object | *(none)* | GitHub Copilot device-flow credential state |
| `custom_openai` | object | *(none)* | Custom OpenAI-compatible base URL + key |

The `copilot` and `custom_openai` objects are managed by Eitri (via device-flow login and the settings panel respectively); you rarely need to edit them by hand.

## Requirements

- **Linux** (Eitri is a Linux agent).
- **bubblewrap (`bwrap`)** must be installed — Eitri never runs unsandboxed and refuses to start without it.

## Building

```sh
make build    # build ./bin/eitri
make test     # run the test suite (go test ./...)
make clean    # remove build artifacts
```

## Contributing

See [`CONTEXT.md`](CONTEXT.md) and [`docs/`](docs/) for the internal codebase documentation and agent guidance.
```