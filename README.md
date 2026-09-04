# Eitri

**Eitri** is a self-hosted, single-binary AI coding agent for Linux. It runs entirely on your machine and your own model credentials — no vendor lock-in, no hosted agent service, nothing leaves your box except the requests you choose to send to your provider.

## Philosophy

- **Smith it.** Minimal, precise strikes. Full substance, no filler. Prefer the simplest correct solution, focused edits over full rewrites, and preserving existing code style.
- **Unix primitives first.** Compose command-line tools into simple pipelines. Scripts are for state and control flow; everything else is `bash`.
- **Self-host or don't.** Eitri is a single static Go binary you drop anywhere. Sessions, transcripts, and configuration live under `~/.eitri`. You own them.
- **Your provider, your terms.** Point Eitri at any model or OpenAI-compatible endpoint — local or cloud. No vendor lock-in.
- **Sandboxed by default.** Eitri never runs unsandboxed: every execution is confined by bubblewrap.
- **One prompt, exactly what it promises.** The agent prompt is fixed and written to match a declared dependency set. Eitri verifies every declared dependency at launch and refuses to start if anything is missing, so the agent never hallucinates a tool that isn't there.

> Eitri's internal, agent-facing documentation lives in [`CONTEXT.md`](CONTEXT.md). This README is for humans.

## Quickstart

```sh
# Install the declared toolset (required; Eitri refuses to start without it,
# because its agent prompt promises these tools unconditionally):
#   Debian/Ubuntu: sudo apt install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
#   Fedora:        sudo dnf install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils
#   Arch:          sudo pacman -S bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils

make build          # 1. build ./bin/eitri
./bin/eitri         # 2. launch the interactive TUI
# 3. on first launch you'll be asked to log in to your provider
```

## Usage

### Launch modes

| Command | What it does |
| --- | --- |
| `eitri` | Launch the interactive TUI |
| `eitri -b "<prompt>"` | Run once in batch mode and exit |
| `eitri -b "<prompt>" -v` | Batch mode, plus print the model's thinking/reasoning to stdout |
| `eitri -d` | Debug mode: write full HTTP traces to/from the provider |
| `eitri --version` | Print the version and exit |

### Repository instructions (`AGENTS.md`)

If the workspace root (the directory you launch Eitri from) contains an `AGENTS.md`, Eitri reads it and carries its content to the model as a dedicated system-layer directive headed `## Repository instructions (AGENTS.md)` — both in the TUI and in batch (`-b`) mode. The injected instructions are **additive**: the built-in Eitri persona prompt is preserved unchanged, and the message is excluded from persisted session history so it isn't duplicated on the next turn. Without an `AGENTS.md`, no extra message is sent and the request is byte-identical to the pre-feature case. There is no opt-in or escape-hatch flag; the file is loaded whenever it exists.

### Diagnostics with pprof

Use `pprof` for performance symptoms: slow rendering, stalls while streaming, high CPU, or unexpected allocation pressure. It is disabled by default; enable it only for a diagnostic run and bind it to localhost:

```sh
eitri --pprof 127.0.0.1:6060
```

From another shell, collect profiles while reproducing the problem:

```sh
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/profile
curl --fail --max-time 30 -o heap.pprof http://127.0.0.1:6060/debug/pprof/heap
curl --fail --max-time 30 -o goroutine.txt 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=2'
```

Mutex and block profiling are available when needed, but are off unless requested because they add overhead:

```sh
eitri --pprof 127.0.0.1:6060 --pprof-mutex --pprof-block
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/mutex
go tool pprof -seconds 30 http://127.0.0.1:6060/debug/pprof/block
```

Use pprof to find where time or allocation pressure is spent. To prove a performance fix, measure before and after one focused change with benchmarks; see [`docs/render-diagnostics.md`](docs/render-diagnostics.md) for the full diagnostics workflow.

### Sessions

Eitri records every session so you can review, replay, and search past work:

```sh
eitri session list
                           # list recorded sessions (GUID, time, cycles, model)
eitri session show <guid> [--turn N] [--no-reasoning]
                           # compact per-cycle summary; --turn N dumps that cycle's full JSON records
eitri session talk <guid> [--turn N|N-M] [--from N] [--role user|assistant|tool|system] [--reasoning]
                           # full conversation as plain text; shared request history is deduped
                           # reasoning is stripped unless --reasoning
eitri session grep <pattern> [guid|all] [-full]
                           # find cycles whose messages match pattern, with snippets;
                           # -full prints the complete matching field text
```

Full detail lives in [`docs/sessions.md`](docs/sessions.md).

### In the TUI

- Type a prompt in the **composer** at the bottom and press `enter` to submit.
- Start slash commands with `/` (e.g. `/settings` to open settings).
- Enter `/help` for the complete live reference, which always shows the current bindings.

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
| `pgup` / `pgdn` | Scroll history |

#### Panes

| Key | Action |
| --- | --- |
| `ctrl+e` | Toggle expanded/collapsed view |
| `ctrl+x` | Narrow the right pane |
| `ctrl+z` | Widen the right pane |

#### Actions

| Key | Action |
| --- | --- |
| `ctrl+s` | Open settings |

#### Slash commands

| Command | Action |
| --- | --- |
| `/settings` | Open the settings panel |
| `/new` | Start a fresh session (clears this conversation) |
| `/login` | Interactive provider login |
| `/help` | Show this help message |

#### Concepts

| Term | Meaning |
| --- | --- |
| `expanded mode` | `ctrl+e` toggles all tool and chain-of-thought blocks |
| `block focus` | `tab` to focus, `enter` to expand one block |
| `drag-select` | Click and drag to select text |
| `right rail` | Stats, context, and model info |

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
| `context_overflow_recovery` | bool | `true` | Summarize older history and retry once if the provider rejects an oversized request |
| `extra_writable_paths` | array of strings | *(empty)* | Additional paths the agent may write to |
| `theme` | string | `dark` | UI theme |
| `rail_width` | int | `30` | Width of the right rail/pane |
| `copilot` | object | *(none)* | GitHub Copilot device-flow credential state |
| `custom_openai` | object | *(none)* | Custom OpenAI-compatible base URL + key |

The `copilot` and `custom_openai` objects are managed by Eitri (via device-flow login and the settings panel respectively); you rarely need to edit them by hand.

## Requirements

- **Linux** (Eitri is a Linux agent).
- **Declared toolset** (required; fatal at boot) — Eitri verifies every declared dependency at launch and refuses to start without it, because its agent prompt promises these tools unconditionally:
  - Hard substrate: `bwrap` (bubblewrap — Eitri never runs unsandboxed) and `bash`.
  - Declared tools: `rg` (ripgrep), `curl`, `lynx`, `patch`, `python3`, `git`, `jq`, `xdg-open` (`xdg-utils`, backing `open_in_browser`).
  - Install hints (a missing tool aborts the launch naming every miss with its package):
    - Debian/Ubuntu: `sudo apt install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils`
    - Fedora: `sudo dnf install bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils`
    - Arch: `sudo pacman -S bubblewrap bash ripgrep curl lynx patch python3 git jq xdg-utils`
- **Base toolset** (assumed present) — the coreutils `bash` builds on: `grep`, `sed`, `awk`, `cat`, `nl`, `diff`; no boot check.

## Building

```sh
make build    # build ./bin/eitri
make test     # run the test suite (go test ./...)
make clean    # remove build artifacts
```

## Contributing

See [`CONTEXT.md`](CONTEXT.md) and [`docs/`](docs/) for the internal codebase documentation and agent guidance.
```