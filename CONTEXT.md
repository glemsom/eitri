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

**Fold**:
The session-owned writer for a running turn's live material: streamed deltas grow the streaming assistant message, and tool observations land in both the tool log and the arrival-ordered event log, with sequence numbers stamped by Fold alone. No code outside the session folds turn events directly.
_Avoid_: stream handler, event appender

**TurnSession**:
The owner of a turn's whole life through four verbs: Begin arms the cancelable context and submits the prompt, Stop cancels the in-flight turn, Fold writes live stream/tool events, and Commit reconciles completion (success, error, or stopped) into the transcript. The Model routes messages; all live-turn state — timeline, sequence counter, streaming cursor, busy flag — lives behind these verbs (the transcript reads the live event log through a read-only accessor), so tests can drive a full turn without a UI loop.
_Avoid_: dispatch, turn state machine

**Thinking suppression**:
A per-provider capability to actually stop chain-of-thought on the wire when the thinking toggle is off, negotiated with the provider at session start. A provider that cannot suppress reasoning surfaces a warning rather than silently ignoring the toggle.
_Avoid_: reasoning off, cot off

**Message-layer transcript**:
The JSONL record (`messages.jsonl` in a session dir) of every provider request/response cycle at the wire level: full messages (system, user, assistant with reasoning and tool calls, tool results), tool names, finish reason, usage, and errors. Ground truth for debugging Eitri and LLM behavior; navigated via `eitri session list/show/grep`. See docs/sessions.md.
_Avoid_: debug log, http trace

**Kitty graphics capability**:
The terminal's support for the Kitty graphics protocol, resolved once at TUI startup from `TERM_PROGRAM` (kitty, ghostty, wezterm) with a graphics-query + DA1 probe fallback. Every Kitty-gated render feature reads this flag; a non-Kitty terminal receives zero Kitty escape sequences and falls back to text-only rendering.
_Avoid_: image support, graphics mode

**Splash convergence flash**:
The single-frame (frame 22) ignition flash at the moment the wordmark starts resolving out of the rain: a full-width solid bar of true-color `#00FFC8` background replacing the wordmark's vertical middle row for exactly one frame, while rain keeps collapsing around it. Rendered as background-colored spaces, which no other splash element uses — the hue matches the wordmark gradient's hottest stop so it reads as the gradient flaring.
_Avoid_: flash overlay, screen blink

**Staggered letter reveal**:
The splash wordmark's assembly motion: each letter of EITRI enters one frame after its left neighbor (frames 22–26), dropping two rows below its final position on the entry frame and settling upward the next frame, so the mark reads as assembling itself from the converging rain. Total splash duration is unchanged; letters render through the same storm cell path as the settled wordmark.
_Avoid_: per-letter animation, bounce-in

**Splash face reveal**:
The embedded dwarf face (`face.webp`, compiled into the binary) shown on Kitty-capable terminals during the splash's emergence phase: fading in from frame 10 to full visibility at frame 18, holding through 19, and dissolving across the shatter (frames 20–22). Transmitted as a downscaled PNG via the Kitty graphics protocol (chunked base64 APC commands) and placed centered in the cell grid; opacity is baked into each frame's pixel alpha since the protocol has no per-image opacity key. Non-Kitty terminals see no graphics escapes and only the rain.
_Avoid_: logo display, image splash

**Splash eye flash**:
The one-frame (frame 18) bright-green (`#00FF88`) highlight over the dwarf's two eye cells at the peak of the emergence ramp — the moment the face is fully revealed. The eye positions are fixed fractions of the face's cell footprint, so the overlay tracks the face's centered placement at any terminal size. Kitty terminals only; non-Kitty terminals intensify the rain through emergence instead.
_Avoid_: laser eyes, glow effect

**Splash title branding**:
The OSC 0 window title (`⚒ Eitri — forging agents`) installed when the launch splash starts and restored (to the title captured at splash start) when it ends. Terminals without OSC 0 support ignore the escape, so emission is always harmless.
_Avoid_: tab name, header text

**Splash cursor choreography**:
The hardware-cursor hide (`CSI ? 25 l`) emitted when the launch splash starts and the show-plus-blink restore (`CSI ? 25 h` then `CSI ? 12 h`) emitted when it settles back into idleWelcome, on every exit path (full playback, skip keypress, mid-splash transcript content). The animation owns the full screen, so no cursor is ever visible during a splash frame.
_Avoid_: caret hiding, cursor blink mode
