# Eitri — Domain Glossary

Eitri is a single-binary AI coding agent for Linux. This context defines the domain vocabulary of a working session — the terms to use when naming concepts in code, tests, and issues, and the synonyms to avoid. Behavioral detail lives in the code and tests (the source of truth); this file carries the term and its stable name.

## Runtime environment

**Workspace**
The user's working directory in which Eitri reads, writes, and executes, mounted read-write at its host path.
_Avoid_: project, root, cwd

**Session temp**
Per-run ephemeral storage shared by `bash` and host-side tools, removed at run end.
_Avoid_: tmp, scratch

**Sandbox / bwrap cage**
The isolation boundary for shell commands: root read-only (including host `/tmp` unless configured writable), workspace and session temp writable, separate PID namespace, private `/dev` and `/proc`.
_Avoid_: container, jail

**Host-side tool**
A tool running outside the sandbox while resolving the same filesystem paths as `bash`; write-side tools gate targets on the writable roots.
_Avoid_: local tool, external tool

**Base tool**
A shell program assumed present on any host that runs Eitri and therefore never checked at startup (`grep`, `sed`, `awk`, `cat`, `nl`, `diff`).
_Avoid_: core utility, builtin

**Declared dependency / declared toolset**
The executables Eitri verifies at boot and refuses to start without — the hard substrate (`bwrap`, `bash`) plus the declared tools (`rg`, `curl`, `lynx`, `patch`, `python3`, `git`, `jq`, `xdg-open`) — because the single fixed prompt promises them unconditionally. The one boot-time check reports every miss (with per-distro install hints) and exits non-zero. An unsandboxed (`--yolo-unsafe`) session exempts the sandbox substrate (`bwrap`), whose backend it bypasses, and never suggests installing bubblewrap.
_Avoid_: requirement, prerequisite, supported tool, external tool, dependency probe, preflight

## Agent session

**Stopped turn**
A running agent turn aborted by the user: the provider stream dies, any running tool is killed, and fresh provider work is refused. Partial output stays on screen marked as stopped — distinct from an error.
_Avoid_: cancelled turn, aborted turn

**Phase**
The derived answer to "what is the agent doing right now": `idle`, `reasoning`, `working`, or `answering`, computed from live turn state rather than stored.
_Avoid_: status, state

**Thinking suppression**
A per-provider capability to actually stop chain-of-thought on the wire when the thinking toggle is off, negotiated at session start. A provider that cannot reason-suppress surfaces a warning rather than silently ignoring the toggle.
_Avoid_: reasoning off, cot off

**Skill activation**
A skill invoked by the user from the TUI via `/skillname [<args>]`. The model holds no slash state or skill tool; it sees a name/path/description index and loads packs itself; every activation through this surface is a human slash. `model-invocable: false` (`disable-model-invocation: true`) gates model discovery only; the human slash surface is untouched.
_Avoid_: slash command, skill invocation

## Transcript & TUI

**Expansion / collapse**
The per-block open/closed state of chain-of-thought and tool-result entries in a transcript, toggled per block and by the global expand-all / collapse-all modes (Ctrl+E). A live turn's chain-of-thought is one block per streamed delta, each focused and toggled independently and cleared when the turn commits to a single snapshot block.
_Avoid_: toggle, detail view, full view

**Merged transcript flow**
A transcript rendered as one continuous block per turn in arrival order, interleaving reasoning, tool entries, and the answer where their events landed — replacing separate thinking / tool-log / answer panes.
_Avoid_: combined view, flow view

**Follow**
The auto-scroll that pins the history viewport to new content while a turn streams; scrolling up to read breaks it, reaching the newest content re-engages it.
_Avoid_: autoscroll, pin

**Busy spinner**
The animated braille indicator that runs while a turn works — an OpenCode-style frame set advanced every 80 ms — degrading to a static "… thinking" line for reduced-motion or non-UTF-8 environments.
_Avoid_: loader, progress indicator

**Kitty graphics capability**
The terminal's support for the Kitty graphics protocol, resolved once at TUI startup. Non-Kitty terminals receive zero Kitty escape sequences and fall back to text-only rendering.
_Avoid_: image support, graphics mode

**Settings overlay**
The owner of an open Settings surface: the draft form, its on-demand model-discovery lifecycle, and persistence of the draft through the save seams.
_Avoid_: settings form handler, settings state machine

## Message & prompt plumbing

**Message-layer transcript**
The JSONL record (`messages.jsonl` in a session dir) of every provider request/response cycle at the wire level — full messages, tool names, finish reason, usage, errors. Ground truth for debugging; navigated via `eitri session list/show/grep`.
_Avoid_: debug log, http trace

**Transcript event log**
The arrival-ordered record of what an assistant turn emitted — reasoning deltas, tool starts, tool results, answer deltas. `content` and `reasoning` are derived snapshots of this log.
_Avoid_: timeline, history

**Prompt history ring**
The Model-owned ring of submitted user prompts, capped at 100 and deduplicating consecutive repeats. It records real user prompts and `/skill ...` activations but never control slash commands or empty drafts; it survives `/new` and, when persisted, restart. It is the data source the arrow-key recall reads from.
_Avoid_: history list, submitted-log

**Arrow-key recall**
The readline-style navigation of the prompt history ring from the composer: `up`/`down` pull a prior/following prompt into the draft at the caret edge, and `down` past the newest restores the draft the recall displaced. It never fires for `shift+up`/`shift+down`, while a turn streams, or while a completion menu is open.
_Avoid_: history browsing, prompt cycling

**`/new` (fresh session)**
A control slash command that starts a fresh session: it re-mints the live session key to a fresh GUID, blocks while a turn streams, a skill is pending, or the Settings overlay is open, and never records into the prompt-history ring. It preserves config, Settings, provider, and the ring; the old GUID's on-disk session dir and engine history are orphaned (auditable, no pruning).
_Avoid_: reset, wipe, clear history

**Single authoritative prompt**
Eitri's one fixed system prompt (`internal/engine/prompt.md`), written to match exactly the declared dependency set and never adapted to what is installed on a given machine. This is what makes the startup dependency check mandatory rather than advisory.
_Avoid_: system prompt, adaptive prompt, tool guidance

**Repository instructions (AGENTS.md)**
The content of the workspace-root `AGENTS.md`, carried to the provider as its own system-layer message headed `## Repository instructions (AGENTS.md)` and appended after the persona, workspace directive, and skill index. Additive — it never replaces the byte-stable persona prompt. It is stripped from persisted session history and preserved in the compaction stable head.
_Avoid_: project instructions, repo guidance, agent handbook
