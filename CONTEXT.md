# Eitri — Domain Glossary

Eitri is a single-binary AI coding agent for Linux. This context covers the runtime environment a session runs in and the user-facing model of a working session.

## Language

**Workspace**:
The user's working directory in which Eitri reads, writes, and executes, mounted read-write at its host path.
_Avoid_: project, root, cwd

**Path namespace**:
The view of the filesystem a component operates in; host paths are canonical, including session temp.
_Avoid_: filesystem, mount namespace

**Session temp**:
Per-run ephemeral storage shared by `bash` and host-side tools, removed at run end.
_Avoid_: tmp, scratch

**Sandbox / bwrap cage**:
The isolation boundary for shell commands: root read-only (including host `/tmp` unless configured writable), workspace and session temp writable, separate PID namespace, private `/dev` and `/proc`.
_Avoid_: container, jail

**Declared dependency / declared toolset**:
The executable set Eitri verifies at boot and refuses to start without — the hard substrate (`bwrap`, `bash`) plus the declared tools (`rg`, `curl`, `lynx`, `patch`, `python3`) — because the single fixed prompt promises them unconditionally. Soft dependencies (`git`, a browser launcher for `open_in_browser`) are opportunistic: never gated at boot, surfacing only if the agent reaches for them.
_Avoid_: requirement, prerequisite

**Host-side tool**:
A tool running outside the sandbox while resolving the same path namespace as `bash`; write-side tools gate targets on the writable roots.
_Avoid_: local tool, external tool

**Skill activation**:
A skill invoked by the user from the TUI via `/skillname [<args>]`. The model holds no slash state or skill tool; it sees a name/path/description index and loads packs itself; every activation through this surface is a human slash. `model-invocable: false` (`disable-model-invocation: true`) gates model discovery only; the human slash surface is untouched.
_Avoid_: slash command, skill invocation

**Stopped turn**:
A running agent turn aborted by the user: the provider stream dies, any running tool is killed, and fresh provider work is refused. Partial output stays on screen marked as stopped — distinct from an error.
_Avoid_: cancelled turn, aborted turn

**Phase**:
The derived answer to "what is the agent doing right now": `idle`, `reasoning`, `working`, or `answering`, computed from live turn state rather than stored.
_Avoid_: status, state

**Expansion / collapse**:
The per-block open/closed state of chain-of-thought and tool-result entries in a transcript, toggled per block and by global expand-all / collapse-all modes. A live turn's chain-of-thought is one block per streamed delta, each focused and toggled independently and cleared when the turn commits to a single snapshot block.
_Avoid_: toggle

**Transcript event log**:
The arrival-ordered record of what an assistant turn emitted — reasoning deltas, tool starts, tool results, answer deltas. `content` and `reasoning` are derived snapshots of this log; entries appended outside a turn carry a synthesized single-answer-event log so they render through the same flow path.
_Avoid_: timeline, history

**Merged transcript flow**:
A transcript rendered as one continuous block per turn in arrival order, interleaving reasoning, tool entries, and the answer where their events landed — replacing separate thinking / tool-log / answer panes.
_Avoid_: combined view, flow view

**Follow**:
The auto-scroll behavior that pins the history viewport to new content while a turn streams; scrolling up to read breaks follow, reaching the newest content re-engages it.
_Avoid_: autoscroll, pin

**Transcript layout cache**:
The lazily rebuilt row index behind the transcript's mouse hit-test and plain-text rendering. Transcript-affecting mutations mark it stale so the lazy hit-test rebuilds exactly once per change; no code outside the transcript implementation writes the dirty flag by hand.
_Avoid_: manual invalidation, dirty-flag writes

**Open-ended expand seam**:
The persistent Ctrl+E mode that renders every tool entry full-size, framing the delivered result in the tool-category's hue; collapsed entries keep a one-line head and, where the result overran, a summary of the lines/bytes retained.
_Avoid_: detail view, full view

**Fold**:
The session-owned writer for a running turn's live material: streamed deltas grow the streaming assistant message, and tool observations land in both the tool log and the arrival-ordered event log, with sequence numbers stamped by Fold alone.
_Avoid_: stream handler, event appender

**TurnSession**:
The owner of a turn's whole life: Begin arms a new turn, Stop cancels the in-flight one, and Commit reconciles completion (success, error, stopped) into the transcript. It owns the in-turn event log (timeline), its sequence counter, and the streaming cursor, which the transcript reads through a read-only accessor. The busy flag lives on the transcript, not the session.
_Avoid_: dispatch, turn state machine

**TurnFlow**:
The per-turn record of ordered live observations and the answer/reasoning snapshots derived from them. It is the source for what a turn has emitted so far and what is committed when the turn completes; turn lifecycle remains with TurnSession and transcript layout remains with the transcript.
_Avoid_: event buffer, stream accumulator

**Settings overlay**:
The owner of an open Settings surface: the draft form, its on-demand model-discovery lifecycle, and persistence of the draft through the save seams. The Model only tracks whether an overlay is open and routes messages to a single Handle entry point.
_Avoid_: settings form handler, settings state machine

**Busy spinner**:
The animated braille indicator that runs while a turn works — an OpenCode-style frame set advanced every 80 ms — degrading to a static "… thinking" line for reduced-motion or non-UTF-8 environments.
_Avoid_: loader, progress indicator

**Thinking suppression**:
A per-provider capability to actually stop chain-of-thought on the wire when the thinking toggle is off, negotiated with the provider at session start. A provider that cannot suppress reasoning surfaces a warning rather than silently ignoring the toggle.
_Avoid_: reasoning off, cot off

**Message-layer transcript**:
The JSONL record (`messages.jsonl` in a session dir) of every provider request/response cycle at the wire level — full messages, tool names, finish reason, usage, errors. Ground truth for debugging; navigated via `eitri session list/show/grep`.
_Avoid_: debug log, http trace

**Prompt history ring**:
A Model-owned ring of submitted user prompts, capped at 100 entries and deduplicating consecutive repeats. It records real user prompts and `/skill ...` activations but never control slash commands or empty drafts; it is the data source the arrow-key recall reads from and survives a `/new` because it lives on the Model, not the transcript or session. When a persisted path is wired (a `prompt_history.json` sibling of `config.json` in the data directory), the ring loads at boot and rewrites on every change, so recall survives a full program restart; a missing or corrupt file falls back to an empty ring.
_Avoid_: history list, submitted-log.

**Arrow-key recall**:
The readline-style navigation of the prompt history ring from the composer: `up`/`down` pull a prior/following prompt into the draft while the caret rests on the top/bottom line, and `down` past the newest restores the draft that recall displaced. It only fires at the caret edge, never for `shift+up`/`shift+down`, while a turn streams, or while a completion menu is open; a recalled `/skill ...` line stays inert until Enter submits it through the slash path.
_Avoid_: history browsing, prompt cycling.

**`/new` (fresh session)**
A control slash command that starts a fresh session: it never records into the prompt-history ring and immediately re-mints with no confirmation, blocked while a turn streams, a skill is pending, or the Settings overlay is open. It re-mints the live session key to a fresh GUID — the engine's session history for the new key begins empty — resets the transcript to the empty welcome state, and zeros the live STATS counters, while the old GUID's on-disk session dir and engine history are orphaned (auditable, no pruning). It preserves config, Settings, provider, and the prompt-history ring, all of which live outside the transcript.
_Avoid_: reset, wipe, clear history.

**Kitty graphics capability**:
The terminal's support for the Kitty graphics protocol, resolved once at TUI startup from `TERM_PROGRAM` with a graphics-query + DA1 probe fallback. Non-Kitty terminals receive zero Kitty escape sequences and fall back to text-only rendering.
_Avoid_: image support, graphics mode

**Declared dependency**:
A tool Eitri's single system prompt relies on unconditionally and therefore checks for at startup, refusing to run when missing. One of a fixed set (`rg`, `curl`, `lynx`, `patch`, `python3`, plus the `bwrap`/`bash` substrate); contrasted with a base tool that is assumed present and a soft dependency that is documented but never gates startup.
_Avoid_: supported tool, external tool, tool requirement

**Base tool**:
A shell program assumed present on any host that runs Eitri and therefore never checked at startup (`grep`, `sed`, `awk`, `cat`, `nl`, `diff`). Distinct from a declared dependency, which is startup-checked and fatal if missing.
_Avoid_: core utility, builtin

**Soft dependency**:
A tool Eitri may use but never gates startup on: absent at runtime the affected path degrades or fails in a contained, user-visible way. `git` (edit/review loop) and `xdg-open` (backend of `open_in_browser`); a missing `git` raises a single non-fatal boot notice, a missing `xdg-open` fails only when the browser tool actually runs.
_Avoid_: optional tool, nice-to-have

**Startup dependency check**:
The single boot-time pass in `app.Run` (reusing the injectable `LookPath` seam) that verifies every declared dependency is present, reporting all missing tools with per-distro install hints through one exit path and exit code 1. Exists so the fixed system prompt never names a tool that is not installed.
_Avoid_: dependency probe, verify tooling, preflight

**Single authoritative prompt**:
Eitri's one fixed system prompt (`internal/engine/prompt.md`), written to match exactly the declared dependency set and never adapted to what is installed on a given machine. It owns operating strategy; the `bash`/`open_in_browser` tool Descriptions keep their mechanical contract on the normal function-calling surface. This is what makes the startup dependency check mandatory rather than advisory.
_Avoid_: system prompt, adaptive prompt, tool guidance

**Repository instructions (AGENTS.md)**:
The content of the workspace-root `AGENTS.md`, carried to the provider as its own system-layer message headed `## Repository instructions (AGENTS.md)` and appended after the persona, workspace directive, and skill index. Additive — it never replaces the byte-stable persona prompt, and a workspace without the file sends the pre-feature wire bytes unchanged. The message is stripped from persisted session history and preserved in the compaction stable head, mirroring the workspace directive and skill index.
_Avoid_: project instructions, repo guidance, agent handbook
