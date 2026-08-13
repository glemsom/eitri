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

With no flags, `eitri` launches the **interactive fullscreen TUI**: a composer
you type prompts into and a conversation view that renders each assistant
answer as Markdown→ANSI. It renders into the **primary (normal) buffer** — not
the alt screen — so the terminal's native selection, scrollback, and search keep
working through a session (`docs/spec.md` §9). Both the TUI and batch sit on the
same run engine and session transcript: a conversation round-trips through the
engine exactly like `-b` does. On each turn the model's reasoning is surfaced as
its own **collapsible thinking stream** — auto-collapsed after the turn, expanded
with `tab` — kept separate from the answer (`docs/spec.md` §9). Assistant
**answers stream in place** as Markdown→ANSI, growing token by token through the
engine's event stream rather than one full-reply dump on completion, so a reply
renders styled markdown live as it arrives. When skills are
detected, a **skills panel** lists them with install scope and per-session
activation state, and typing `/skillname` (or `/` + `tab` to complete) activates
the skill directly through the same seam the engine uses (`docs/spec.md` §9,
`eitri.md` §2.3). A bottom **status strip** above the composer shows live
run telemetry throughout — model, reasoning effort, thinking on/off,
`turns / max_turns`, running cost, and a **prompt-cache hit-ratio gauge** fed
from the engine's per-turn usage events, plus a non-blocking `[compacted]`
marker after a session compaction (`docs/spec.md` §4/§7). On narrow windows it
collapses to the live counters so it never crowds the composer.
The workspace directory the run acts on is shown as a **read-only workspace header**
above the transcript, so you always see which directory the agent operates in
(`docs/spec.md` §9).

### Keyboard shortcuts

Keybindings for the interactive TUI. Typing, cursor movement, and backspace
are handled by the composer; the shortcuts below drive the agent.

| Shortcut                   | Context       | Action                          |
| -------------------------- | ------------- | ------------------------------- |
| `Enter`                    | Composer      | Send the current prompt (no-op while busy)
| `Shift+Enter`              | Composer      | Break the prompt onto a new line (multi-line input; plain `Enter` still sends)
| `ctrl+c`                   | Anywhere      | Quit                            |
| `ctrl+s`                   | Conversation  | Open the Settings panel         |
| `ctrl+d`                   | Conversation  | Open / close the changed-file Review panel
| `esc`                      | Settings      | Cancel / close without saving   |
| `tab`                      | Composer      | Toggle the collapsible thinking stream |
| `tab`                      | Slash command | Cycle skill-name completion     |
| `tab` / `enter`            | Settings      | Move focus to the next field    |
| `up` / `down` / `left` / `right` | Settings | Adjust the focused value   |
| `y` / `Y` / `Enter`        | Max-turns     | Continue = yes                  |
| `n` / `N` / `esc` / `ctrl+c` | Max-turns   | Continue = no                   |

When a run pauses at the `max_turns` cap, only `y`/`n` (or `esc`/`ctrl+c` to
stop) are accepted until you answer. In the Settings panel, focus moves with
`tab`/`enter` and values are adjusted with the arrow keys; the free-form paths
field accepts typing and `backspace`.

| Flag          | Meaning                                                                  |
| ------------ | ------------------------------------------------------------------------ |
| `-b <prompt>` | Run once in batch mode with the given prompt, emit the answer, and exit. |
| `-v`          | In batch mode, also print the model's thinking/reasoning to stdout.      |
| `-d`          | Debug mode (writes full HTTP traces to/from the provider).               |
| `--version`   | Print the version and exit.                                              |

`-b <prompt>` runs one agent turn through the shared run engine and prints the
final answer to stdout. Inside the turn, the engine dispatches any tool calls
(`bash`, `read`, `write`, `edit`, `web_fetch`, `open_in_browser`, and — when
skills are discovered — `skill`) through the
shared tool registry, which runs `bash` inside the bwrap sandbox, resolves every
path through the shared path-namespace seam (ADR-0002), fetches web content on
`web_fetch`'s own network-unrestricted path (ADR-0001), and launches the host
browser for `open_in_browser` (outside the cage). The batch engine opts the run
into deepseek's
session-scoped prompt cache: every request carries `prompt_cache_key=<GUID>`
and the request head (`model` + stable prior-turn history) stays byte-identical
across turns, so only the tail grows (`docs/spec.md` §4). Per-turn `usage`
(including `prompt_cache_hit/miss_tokens`) is parsed at the provider seam and
returned on the run result — the telemetry behind later cache gauges and
compaction re-warm detection. Tool defs are strict-shaped
(`additionalProperties:false`, all-required; optionals as nullable unions),
re-expressed from one canonical schema per dialect, and the dispatch loop
iterates *all* parallel calls in a turn — validates each against its strict
schema, and routes malformed/truncated arguments back to the model as a
structured `{"INVALID_JSON": ...}` tool result so the model self-corrects
(`docs/spec.md` §2) instead of crashing or silently skipping. Long sessions
auto-compact through the unified compaction engine (`docs/spec.md` §7,
ADR-0003): once prompt usage crosses the configured `compaction_fraction` of
the context window (or a provider context-overflow fires below it), the oldest
body is evicted and folded into an anchored `## Objective` / `## Next Move`
summary re-injected at the request head, while the last `tail_turns=2`
assistant+user pairs (reasoning included) stay verbatim; a `[compacted]`
marker is surfaced on each event. Noisy `bash`
reads (`ls`/`find`/`grep`/`rg`) return deterministically-compressed output at
the tool-result boundary — ANSI/progress screens, consecutive-line dedupe, and
an explicit `+N more` tail marker on heavy listings, gated never to inflate and
recoverable by re-running the command (`docs/spec.md` §5; `internal/compress`).
Thinking/reasoning is suppressed from batch output by default (`docs/spec.md` §6); use `-v` to surface it. `-d` enables
debug mode, which attaches the HTTP trace sink to the run session.

## Agent Skills

Eitri auto-discovers **Agent Skills** (modular instruction packs; see
https://agentskills.io/specification) from two scopes and makes them callable
via a dedicated `skill` tool mid-session (`docs/spec.md` §3; ticket #33):

- **User scope** — `~/.agents/skills/<name>/SKILL.md`, available across every
  project on the machine.
- **Project scope** — `<workspace>/.agents/skills/<name>/SKILL.md`, committed
  with the project. On an exact-name collision the **project pack shadows the
  user pack**.

Discovery parses each pack's frontmatter leniently: a pack with an unparseable
`SKILL.md` (missing/absent `name`+`description` ahead of the body) is omitted
with a warning to stderr rather than surfaced to the model (fail-closed). The
`skill` tool is **only registered when at least one valid skill exists**; when
zero skills are present the tool is omitted entirely. The tool's `name`
parameter is constrained to a strict-schema `enum` of the discovered, filtered
names — never blocked-at-call-time. Activating a skill returns the pack's body
(frontmatter stripped) wrapped in `<skill_content name="…">` plus a
`<skill_resources>` listing of the bundle's files — injected as a **tool result**,
never elevated into a `system` message — so the model resolves referenced files
through its own read/list tools. Re-activating an already-in-context skill
returns a short dedupe notice instead of re-injecting the body. The wrapping
tags double as the compaction ring-fence marker (`["skill"]`). In the TUI a
**skills panel** lists detected skills with their install scope and whether they
are currently activated this session, and a **slash command** (`/skillname`, or
`/` + `tab` to complete) activates a skill directly through the same `skill`
tool the engine uses — no need to ask the model to do it (`eitri.md` §2.3).


## Configuration

On launch Eitri creates its config file with defaults if it is absent and loads
it on startup (`config.json`, `eitri.md` §2.7):

- `~/.eitri/config.json` by default; `EITRI_CONFIG` overrides the path
- `EITRI_DIR` overrides the data directory

Persisted settings: `provider`, `model`, `reasoning_effort` (default `low`),
`thinking_enabled` (default `true`; off produces non-thinking runs with no
`reasoning_effort`), `max_turns` (default `250`), `compaction_fraction`
(default `0.8`), and `extra_writable_paths`. Provider credentials are delivered via environment:
the OpenCode Go key is read from `OPENCODE_API_KEY`, and `EITRI_PROVIDER_URL`
optionally overrides the Chat-Completions endpoint (defaults to OpenCode Go;
custom OpenAI-compatible endpoints are formalized in T11). The batch engine
injects a fake provider in tests via the `app.Options.Provider` seam.

In the TUI, `ctrl+s` opens a **Settings** surface to pick the provider and
model (models are discovered from the configured provider) and tune reasoning
(the `thinking_enabled` on/off mode plus the `reasoning_effort` intensity
when on), `max_turns`, `compaction_fraction`, and `extra_writable_paths`;
saving persists to `~/.eitri/config.json` and takes effect on the next run.
`ctrl+d` opens a **Review** panel on top of the transcript that answers
"what did the agent change?": it lists every file the agent touched with
`[+N, −M]` add/delete counts and status (modified/added/deleted), lets you
inspect a focused file's inline diff in the terminal (a pure-Go diff engine,
no external renderer), and hands the file to the host browser/editor via the
`open_in_browser` escape hatch for diffs too rich for the terminal. It is
read-only against the repo and the live agent loop. When an agent run reaches the `max_turns` cap in the
TUI it pauses and prompts to continue (`y`/`n`); batch mode instead
auto-denies continuation and stops with an error.

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
