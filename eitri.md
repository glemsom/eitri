# Eitri — Product idea

**License:** GNU General Public License v3.0
**Category:** Self-hosted, single-binary AI coding agent / assistant for Linux

Eitri (named for the Norse blacksmith who forged Mjölnir) is an AI agent that runs entirely on the user's own machine. It reads, writes, and executes commands inside the user's workspace, guided by natural-language conversations.

---

## 1. Positioning & value proposition

- **Single binary.** A compiled Go binary with no runtime dependencies, Docker, or Kubernetes. Install to `$PATH`, launch from the target workspace — done.
- **Self-hosted & private.** All conversation data, session snapshots, HTTP traces, and configuration live locally under `~/.eitri/`. Agent execution happens on host hardware. No third-party cloud processes the user's code.
- **Provider-agnostic.** Works with OpenCode Go, GitHub Copilot (device-flow OAuth), and any Custom OpenAI-compatible provider.
- **TUI Interfaces.** A Rich fullscreen TUI interface. Rendering Markdown and Mermaid. 
- **Headless.** (Also support running in `batch` mode for unattented automations/schedules)
- **Token efficient.** Tools are optimized for LLM token usage. As example, `read` requires line range for reading, to avoid reading entire files by default. `bash` tools has filters for common tools to reduce token usage.

## 2. Capabilities
### 2.1 tools

- **Built-in tool registry** shared across all run kinds (TUI, batch):
  - `bash` — shell command execution (sandboxed via `bubblewrap`). Token efficient filters for commands like `ls`, `find` `grep` and `rg`)
  - `read` - Read file. Supports file-range reads to avoid reading entire files into context.
  - `write` - For writing an entire file.
  - `edit` - For editing existing files.
  - `web_fetch` — fetch a URL and convert to Markdown
  - `open_in_browser` — open a single URL or file in the host browser (Similar to `xdg_open`)
  - `skill` — activate an Agent Skill pack (Research the recommended way to do this, expecially for OpenAI compatible endpoints)

- **Review surface.** In the TUI, `ctrl+d` opens a read-only Review panel over the transcript: a changed-file list with `[+N, −M]` deltas and status (modified/added/deleted), an in-terminal inline diff of the focused file (pure-Go diff engine, no external renderer), and `open_in_browser` as the escape hatch to the full file in the host browser/editor for diffs too rich for the terminal. It never mutates the repo or the live agent loop.

- **Transcript scroll navigation.** In the TUI the transcript is navigable (issue #120): the mouse wheel scrolls up/down, `PgUp`/`PgDn` page through it, and `Home`/`End` jump to the oldest/newest output. Scrolling up breaks the follow position so reading stays put instead of being yanked to the newest; submitting a new prompt re-follows the newest output. Navigation never steals composer input focus (arrow keys keep editing the prompt).

- **Composer.** In the TUI, plain `Enter` submits the prompt; `Shift+Enter` inserts a newline for multi-line drafts. The composer grows with the draft within the fixed bottom band (one row per line, up to a bound), then scrolls internally so a long draft never spills into the transcript; the status strip and slash-completion list stay pinned above it (issue #121).

- **Transcript copy.** In the TUI, `ctrl+o` copies the full conversation transcript (prompts, answers, reasoning, tool entries) to the system clipboard; the `/copy` slash command does the same from the command surface. A click-drag over the transcript highlights a cell range (reverse video) and releasing the drag copies just that snippet — wrapped lines and ANSI-styled rows included — so a single code block or error can be extracted without the whole session (issue #124). Copy reports success/failure as a status note and never mutates the transcript, the composer, or the agent loop (issues #123, #124).

- **Non-interactive fallback.** The full-screen TUI refuses to start when it cannot render cleanly: stdout is not a TTY (output piped), `TERM` is unset or `dumb` (any case, incl. `dumb-*` variants), or the window is narrower than 80 columns. It prints a message pointing at batch mode (`eitri -b "<prompt>"`) to stderr and exits, so no TUI reflow is ever written into a pipe or a dumb terminal (issue #125).

- **Styling identity.** The TUI surface uses a restrained dark palette with a single agent accent: user prompts render as right-aligned "you" chips, assistant answers as left-bordered panes, and errors get an error-colored border. Thinking (🤔), tool (⊕), tool outcome (✓/✗), and error (⚠) markers stay consistent across the transcript, and the fixed bottom band (status strip + slash completion + composer) is framed by an accent separator row. All styles are centralized in `lipgloss`
with hex colors; on the Charm v2 stack (issue #145–#149) lipgloss/bubbletea
render full-fidelity ANSI and Bubble Tea downsamples to the terminal's color
profile at the output layer, so the surface still degrades safely to ANSI-256
or fewer colors on a non-truecolor terminal (issue #122).

- **Max-turns limit.** Configurable cap on loop iterations per run. Default is 250 turns, then interactive prompt to allow Eitri to continue with more turns.

### 2.2 LLM providers & models

- **OpenCode Go** — primary provider; routes models by dialect family (Qwen / MiniMax to Anthropic, the rest to OpenAI completions).
- **GitHub Copilot** — device-flow OAuth integration with an **in-UI approval flow** (TUI-only); batch consumes stored/refreshed credentials and never runs the interactive handshake.
- **Custom OpenAI** — any OpenAI-compatible endpoint with a user-supplied base URL and API key; routes via Chat Completions (no device flow).
- **Model discovery.** Available models are discovered from the configured provider and surfaced in Settings.
- **Provider routing.** A provider factory builds the running provider from the saved `provider` config value, honored identically across TUI and batch.
- **Reasoning support.** First-class handling of model chain-of-thought (including Copilot, which streams its reasoning through the same channel as the primary provider).
- **Prompt caching.** Session-scoped prompt-cache keys to reduce cost/latency on long sessions.

### 2.3 Agent Skills

Eitri supports **Agent Skills** — modular skill packs, as described in https://agentskills.io/specification
- Auto-discovery from fixed project, user, and workspace roots with last-wins precedence.
- Per-session activation; skills can be injected per workspace.
- Slash-command surface: `/skillname` activates a skill, `/settings` opens the settings modal, and `/` shows a completion list mixing the built-in commands with matching skills. (Issue #87)
- TUI shows detected and currently activated skills

### 2.4 Context management

- **Pattern compression** — deterministic, zero-LLM compression of high-volume bash output (`ls`, `find`, `grep`, `rg`) that regroups it by directory/file and never inflates token count.

### 2.5 Security & sandboxing

- **bwrap sandbox.** Shell commands run inside a bubblewrap sandbox by default — read-only root, writable workspace and `/tmp`, separate PID namespace — for defense-in-depth against arbitrary code execution. The workspace is mounted read-write at its host path (no path rewrite); the session temp is mounted as sandbox `/tmp` (host `/tmp/eitri-GUID`).
- **Path-namespace seam.** Host paths are canonical; the only remapped root is session temp (sandbox `/tmp` ↔ host `/tmp/eitri-GUID`). One shared `PathTranslator` in the tool registry routes `bash`, `write`, `edit`, `open_in_browser`, and path validation through the same bidirectional, reversible prefix-map, so all resolve the same `/tmp` namespace as `bash`. The model always sees sandbox `/tmp/...`; the GUID is internal host detail (see ADR-0002).
- **Path validation.** File tools validate against the workspace root and configured writable roots; targets outside everything are hard errors.
- **Writable-path controls** (`write`/`edit`) — targets may include configured `extra_writable_paths`, never arbitrary host paths.
- **Batch-mode guard.** Headless runs auto-deny confirmation requests, preventing risky operations from proceeding unsupervised.
- **Session troubleshooting.** Full session transcript are stored in ~/.eitri/sessions/GUID for troubleshooting. Eitri support `-d` flag for debug, which also enables full HTTP traces to/from LLM Provider for deep dive debug) 

### 2.6 Batch / headless mode

- `eitri -b "prompt"` runs the agent once from the terminal and exits. (Emits answer to `stdout`)
  - By default, does not emit `thinking` to `stdout` (Can be enabled `-v`)
- Uses the same run engine, sandbox, skills, and on-disk review trail as TUI runs — batch sessions persist, report, and auto-compact identically.
- Supports scripting, automation, and CI integration.

### 2.7 Configuration & environment

- Config stored in `~/.eitri/config.json` (path overridable via `EITRI_CONFIG`).
- Key settings: `model` (default `deepseek-v4-flash`), `reasoning_effort` (default `low`; tiers `low`/`medium`/`high`/`max`; raised to trade cost for deeper chain-of-thought, lowered to save cost), `thinking_enabled` (default `true`; when off, runs emit non-thinking requests with no thinking toggle and no `reasoning_effort`, exactly like the compaction summarizer), `max_turns` (cap on tool-loop iterations), `compaction_fraction` (context-utilization trigger for auto-compaction), `theme` (Markdown render theme; default `dark`; supported `dark`/`light`/`dracula`/`tokyo-night`/`pink`/`notty`/`auto`; unknown values fall back to `dark` and print a one-time `unknown theme …` warning in the TUI status band on startup), `extra_writable_paths` (additional writable roots for write/edit).
- **Settings surface.** In the TUI, `ctrl+s` opens a Settings panel to pick the provider & model (models discovered from the configured provider, surfaced with loading/error states) and set `reasoning_effort`, `max_turns`, `compaction_fraction`, `theme` (arrow-cycle through the 7 supported values; an unknown hand-edited value shows raw and the first arrow press selects a valid theme), and `extra_writable_paths`. It shows the live cache hit-ratio + cost readout for the running session. Saving persists to `config.json` and takes effect on the next run — no hand-editing.
- **Max-turns continuation.** On reaching the `max_turns` cap the engine pauses and prompts to continue (`y`/`n`) in the TUI; batch auto-denies and stops with an error.
- Environment variables: `EITRI_CONFIG` (config path), `EITRI_DIR` (data dir).

## 3. Platform & requirements

- **OS:** Linux (amd64 releases).
- **dependency:** bubblewrap (`bwrap`) for sandboxed shell execution; without it Eitri falls back to direct execution.
- **No** tmux, Node.js, npm, Docker, or Kubernetes requirements.

---

## 4. Research
 - How to make Eitri tools token efficient for LLMs to utilize
 - TUI Frameworks for Golang.
 - OpenCode Go endpoints and integration (Chat Complete vs Response API)
 - How to interact with `thinking` supported models
 - Best Practice for caching (Focus on token efficient)
 - Best Practice to expose tool calls to LLMs (Focus on OpenAI compatible, like OpenCode Go provider)
 - 
