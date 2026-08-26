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
A skill invoked by the user from the TUI via `/skillname [<args>]`. The model has no `skill` tool — it loads packs itself via `bash cat` — so every activation is a human slash. Each activation starts an agent turn whose prompt carries the skill's content; a repeated activation re-applies the full skill rather than no-op'ing. The SkillActivation module owns this surface end to end — slash-command parsing, completion candidates and cycling, the rendered completion list and its row count, the follow-up turn prompt, and activation through the SkillsSurface — so the Model delegates and holds no slash state: slash tests hit the module seam directly rather than driving a full Model.
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

**Transcript layout cache**:
The lazily rebuilt row index behind the transcript's mouse hit-test and plain-text rendering. Every transcript mutation — message appends, stream deltas, tool entries, resize, settings flips, rail-width changes — routes through a Transcript-owned method that marks the cache stale itself, so no code outside the transcript implementation ever writes the dirty flag by hand.
_Avoid_: manual invalidation, dirty-flag writes

**Open-ended expand seam**:
The persistent Ctrl+E mode that renders every tool entry full-size. A file-changing tool entry shows an inline before→after diff; a path whose content could not be snapshotted falls back to a `[+N, −M]` count summary. Collapsed, an entry keeps only the one-line delta tag.
_Avoid_: detail view, full view

**Fold**:
The session-owned writer for a running turn's live material: streamed deltas grow the streaming assistant message, and tool observations land in both the tool log and the arrival-ordered event log, with sequence numbers stamped by Fold alone. No code outside the session folds turn events directly. Stream and Tool mark the transcript's layout cache stale inside themselves, so the Model's stream-delta and tool-update handlers never invalidate by hand around a Fold call.
_Avoid_: stream handler, event appender

**TurnSession**:
The owner of a turn's whole life through four verbs: Begin arms the cancelable context and submits the prompt, Stop cancels the in-flight turn, Fold writes live stream/tool events, and Commit reconciles completion (success, error, or stopped) into the transcript. The Model routes messages; all live-turn state — timeline, sequence counter, streaming cursor, busy flag — lives behind these verbs (the transcript reads the live event log through a read-only accessor), so tests can drive a full turn without a UI loop. Each verb keeps the transcript's layout cache correct itself — marking it stale inside the mutation path — so no caller ever invalidates by hand around a verb call.
_Avoid_: dispatch, turn state machine

**Settings overlay**:
The owner of an open Settings surface: the draft form, its on-demand model-discovery lifecycle, and persistence of the draft through the save seams. The Model only tracks whether an overlay is open and routes messages to it through a single Handle entry point; the overlay answers with an outcome (continue, closed, saved), any follow-up command, and the accepted draft on save. The overlay's verbs keep the form state internally consistent.
_Avoid_: settings form handler, settings state machine

**Launch splash**:
The animated full-screen startup sequence (matrix rain resolving into the rainbow wordmark) that owns the screen until it settles or is skipped. The Splash module owns the lifecycle end to end — the animation state, the tick cadence, the title/cursor side-effects, the keypress skip, and the early end when the transcript gains content — so the Model only tracks whether the splash is active (a nil pointer) and routes every message through the module's single Handle entry point, while Init asks it for the startup commands and View asks it for the frame. Splash tests hit the module seam directly rather than driving a full Model.
_Avoid_: boot screen, loading screen

**Busy spinner**:
The animated braille indicator that runs while a turn works — an OpenCode-style frame set advanced every 80 ms tick by the spinner module — degrading to a static "… thinking" line for reduced-motion or non-UTF-8 environments. The motion gate it shares with the Launch splash disables all animation when `EITRI_NO_MOTION` is set or the locale cannot render braille, so working state stays readable as text.
_Avoid_: loader, progress indicator

**Thinking suppression**:
A per-provider capability to actually stop chain-of-thought on the wire when the thinking toggle is off, negotiated with the provider at session start. A provider that cannot suppress reasoning surfaces a warning rather than silently ignoring the toggle.
_Avoid_: reasoning off, cot off

**Message-layer transcript**:
The JSONL record (`messages.jsonl` in a session dir) of every provider request/response cycle at the wire level: full messages (system, user, assistant with reasoning and tool calls, tool results), tool names, finish reason, usage, and errors. Ground truth for debugging Eitri and LLM behavior; navigated via `eitri session list/show/grep`. See docs/sessions.md.
_Avoid_: debug log, http trace

**Kitty graphics capability**:
The terminal's support for the Kitty graphics protocol, resolved once at TUI startup from `TERM_PROGRAM` (kitty, ghostty, wezterm) with a graphics-query + DA1 probe fallback. Every Kitty-gated render feature reads this flag; a non-Kitty terminal receives zero Kitty escape sequences and falls back to text-only rendering.
_Avoid_: image support, graphics mode

