# Eitri — Domain Glossary

Eitri is a single-binary AI coding agent for Linux. This context covers the runtime environment a session runs in and the user-facing model of a working session.

## Language

**Workspace**:
The user's working directory in which Eitri reads, writes, and executes. Mounted read-write at its host path inside the sandbox.
_Avoid_: project, root, cwd

**Path namespace**:
The view of the filesystem a component operates in. Host paths are canonical; the only remapped region is the session temp.
_Avoid_: filesystem, mount namespace

**Session temp**:
Per-run ephemeral storage shared by `bash` and host-side tools; removed at run end.
_Avoid_: tmp, scratch

**Sandbox / bwrap cage**:
The isolation boundary for shell commands: root read-only, workspace and session temp writable, separate PID namespace, private `/dev` and `/proc`.
_Avoid_: container, jail

**Host-side tool**:
A tool running outside the sandbox but resolving the same path namespace as `bash`. Write-side tools gate targets on the writable roots; read-only tools resolve through the shared translator alone.
_Avoid_: local tool, external tool

**Skill activation**:
A skill invoked by the user from the TUI via `/skillname [<args>]`, distinct from the model's automatic `skill` tool call. Every activation starts an agent turn whose prompt carries the skill's content; a repeated activation re-applies the full skill rather than no-op'ing.
_Avoid_: slash command, skill invocation

**Stopped turn**:
A running agent turn aborted by the user. The provider stream dies, any running tool is killed, and the engine refuses fresh provider work. Partial output stays on screen marked as stopped — distinct from an error — and the next prompt runs normally.
_Avoid_: cancelled turn, aborted turn

**Phase**:
The derived answer to "what is the agent doing right now": `idle`, `reasoning`, `working`, or `answering`. Computed from the live turn state, not stored.
_Avoid_: status, state

**Expansion / collapse**:
The per-block open/closed state of chain-of-thought and tool-result entries in a transcript, controlled by `Tab`/`Enter` per block and by global expand-all / collapse-all modes. Expands and collapses are ordered and composable rather than mutually exclusive.
_Avoid_: toggle

**Transcript event log**:
The arrival-ordered record of what an assistant turn emitted: reasoning deltas, tool starts, tool results, and answer deltas, in the exact order produced. `content` and `reasoning` are derived snapshots of this log. Every assistant transcript entry owns one — entries appended outside a turn (help cards, login messages, error and skill notes) carry a synthesized single-answer-event log so they render through the same flow path as real turns. A user prompt whose running turn has not emitted anything yet synthesizes a minimal empty log for the same reason: the FlowRenderer is the transcript's only render path.
_Avoid_: timeline, history

**Merged transcript flow**:
A transcript rendered as one continuous block per turn in arrival order, interleaving reasoning, tool entries, and the answer where their events landed — replacing separate thinking / tool-log / answer panes.
_Avoid_: combined view, flow view

**Follow**:
The auto-scroll behavior that pins the history viewport to the newest content while a turn streams. Scrolling up to read earlier output breaks follow; reaching the newest content re-engages it.
_Avoid_: autoscroll, pin

**Open-ended expand seam**:
The persistent Ctrl+E mode that renders every tool entry full-size. A file-changing tool entry shows an inline before→after diff; a path whose content could not be snapshotted falls back to a `[+N, −M]` count summary. Collapsed, an entry keeps only the one-line delta tag.
_Avoid_: detail view, full view

**Thinking suppression**:
A per-provider capability to actually stop chain-of-thought on the wire when the thinking toggle is off, negotiated with the provider at session start. A provider that cannot suppress reasoning surfaces a warning rather than silently ignoring the toggle.
_Avoid_: reasoning off, cot off

**Message-layer transcript**:
The JSONL record (`messages.jsonl` in a session dir) of every provider request/response cycle at the wire level: full messages (system, user, assistant with reasoning and tool calls, tool results), tool names, finish reason, usage, and errors. Ground truth for debugging Eitri and LLM behavior; navigated via `eitri session list/show/grep`. See docs/sessions.md.
_Avoid_: debug log, http trace

**Kitty graphics capability**:
The terminal's support for the Kitty graphics protocol, resolved once at TUI startup from `TERM_PROGRAM` (kitty, ghostty, wezterm) with a graphics-query + DA1 probe fallback. Every Kitty-gated render feature reads this flag; a non-Kitty terminal receives zero Kitty escape sequences and falls back to text-only rendering.
_Avoid_: image support, graphics mode

**Splash title branding**:
The OSC 0 window title (`⚒ Eitri — forging agents`) installed when the launch splash starts and restored (to the title captured at splash start) when it ends. Terminals without OSC 0 support ignore the escape, so emission is always harmless.
_Avoid_: tab name, header text
