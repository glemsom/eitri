# Eitri

Eitri is a self-hosted, single-binary AI coding agent that reads, writes, and runs code in a user's workspace through natural-language conversation with any OpenAI-compatible model provider.

## Language

### Runs and sessions

**Turn**:
One provider request/response cycle: the agent sends the whole message history and consumes the streamed reply (which may include tool calls).
_Avoid_: Cycle, iteration

**Run**:
One bounded execution of the turn loop against a prompt — a batch invocation, or one submission inside the interactive TUI. A run ends on a final answer, the max-turn cap, or a user stop.
_Avoid_: Invocation, request

**Session**:
The on-disk trail of one run, GUID-named under the data directory. The GUID is the session's identity; sessions are append-only records, never edited.
_Avoid_: Conversation, chat history

**Transcript**:
The message-layer record inside a session directory: one JSON line per request and response, exactly what went over the wire. The ground truth for debugging and performance work.
_Avoid_: Log, history dump

**Live session key**:
The session GUID currently bound to the interactive TUI, held behind a mutable holder so `/new` re-mints it and every surface observes the new key at the next turn boundary.
_Avoid_: Active session, current chat

**Data directory**:
The local state root (`~/.eitri` by default, overridable with `EITRI_DIR`) holding config, sessions, and skills. The user owns it; the binary creates it on first launch.
_Avoid_: Config dir, home

### Provider and context

**Provider**:
An OpenAI-compatible model endpoint behind one adapter: model discovery, streaming, generation control, and auth. Local or cloud; no vendor is built-in-only.
_Avoid_: Backend, vendor

**Dialect**:
The canonical, provider-agnostic shape of a tool definition or message, translated at the provider seam into whatever wire format the endpoint speaks.
_Avoid_: Format, encoding

**Compaction**:
Proactive summarization of older turns when prompt usage crosses a fraction of the context window, keeping the session alive across long runs; also forced reactively on a context overflow from the provider.
_Avoid_: Summarization, trimming

**Context overflow**:
A provider refusal signaling the request exceeded the context window; detected from both the sentinel and provider error bodies, and triggers emergency compaction.
_Avoid_: Token limit error

**Compression**:
The deterministic, zero-LLM shrinking of high-volume tool output at the tool-result boundary — line caps, byte caps, ANSI stripping — with an explicit "+N more" marker, never silent truncation.
_Avoid_: Compaction (that is the LLM-driven summarization of turns)

### Tools and sandbox

**Toolset**:
The declared, fixed set of tools the agent prompt promises unconditionally (`bash`, `open_in_browser`, and their backing binaries). Eitri verifies every one at launch and refuses to start if anything is missing — the prompt never hallucinates a tool that isn't there.
_Avoid_: Plugin set, extensions

**Workspace**:
The host directory the session operates in; writable by design.
_Avoid_: Project root, cwd (cwd is the incidental current directory; the workspace is the session's declared scope)

**Sandbox**:
The bubblewrap cage confining every `bash` execution by default: read-only root, writable workspace and session temp, isolated PID/`/proc`/`/dev` namespaces. Dropped only by the explicit `--yolo-unsafe` opt-out. The prompt claims no containment either way: the sandbox sentence moved with the batch-subagent guidance into the `subagents` skill.
_Avoid_: Cage, jail (colloquially fine, but "sandbox" is canonical)

**Session temp**:
The per-session writable scratch directory (`$TMPDIR`-style host path) the agent is told to write ephemeral artifacts to.
_Avoid_: Scratchpad, tmp (the ambiguous system-wide /tmp)

### Skills

**Skill**:
A discovered, validated pack of agent instructions (body plus resources) found under the builtin, user, or project scope, activated by the human via `/skillname` or read by the model itself through `bash`.
_Avoid_: Prompt template, plugin

**Builtin skills root**:
The binary-owned ROM at `$EITRI_DIR/skills-builtin` where embedded builtin skill packs are materialized at boot. Refreshed only on content mismatch (upgrades win, edits reverted by design); a builtin is overridden by a same-named skill in the user or project scope.
_Avoid_: Engine skills, embedded skills

**Skillspack source**:
The repo directory `internal/engine/skillspack/` — the single place builtin skill packs are authored and edited. The materialized skills-builtin root is its output, and from inside an agent sandbox `~/.eitri` is read-only (only the workspace, the session's `$TMPDIR`, and `~/.cache` are writable), so agent edits to builtin skills land in the skillspack source, never in the materialized ROM.
_Avoid_: Skills-builtin editing, builtin skill patching

**Skill catalog**:
The filtered, trust-gated set of skills for a run, backing both the human slash surface and the model-facing skill index. On exact-name collision the strongest claim wins: project > user > builtin.
_Avoid_: Skill list, registry

**Model-invocable skill**:
A skill the model may discover and load on its own via the rendered index; non-model-invocable skills stay reachable only through the human slash surface.
_Avoid_: Auto skill, hidden skill

**Skill activation**:
The slash-command path that resolves `/skillname`, appends the invocation to the transcript, and injects the skill body into the follow-up turn's context.
_Avoid_: Skill run, skill load

### TUI surface

**Transcript (TUI)**:
The scrolling rendered record of the conversation in the terminal — distinct from the on-disk transcript, which is the same trail's message-layer record.
_Avoid_: Log view

**Composer**:
The input surface where the user writes prompts, mentions, and slash commands.
_Avoid_: Input box, prompt field

**Turn session**:
The TUI-side owner of one run's lifecycle — context, cancellation, thinking toggle, and timeline — committed to the transcript when the run settles.
_Avoid_: Run state, turn manager

**Stop**:
The user's cancellation of a live run, surfaced as the dedicated `ErrStopped` sentinel so it is always distinguishable from a failure.
_Avoid_: Interrupt, cancel (cancel implies the ambient context cancellation; Stop is the user-facing act)

### Modes

**Batch mode**:
One-shot execution: `eitri -b "<prompt>"` runs a single run and exits. Piped (non-TTY) stdin rides as fenced **stdin context** after the prompt, and `--format json` prints one machine-parseable envelope `{answer, session, turns, stopped}` instead of the plain answer; batch is also the substrate for subagent dispatch.
_Avoid_: Headless mode, non-interactive mode

**Stdin context**:
The fenced `Stdin input:` block batch mode appends after the `-b` prompt when stdin is piped (non-TTY), so upstream data is declared input — never instructions. Empty stdin appends nothing; input over the 1 MiB cap is refused rather than truncated; stdin piped without `-b` refuses the launch because the TUI would silently drain it.
_Avoid_: Piped context, fenced input block

**Debug mode**:
`-d`: additionally records raw HTTP request/response bodies into the session directory.
_Avoid_: Verbose, trace mode (trace is the artifact, not the mode)

**Subagent**:
A batch-mode Eitri process launched by the agent itself in an isolated execution directory and awaited in the same shell invocation.
_Avoid_: Child agent, worker
