# Eitri domain context

Eitri is a self-hosted, single-binary AI coding agent for GNU/Linux. It reads, writes, and runs code in a declared workspace through natural-language conversation with local or hosted model providers. This document defines stable terms; implementation paths belong in [ARCHITECTURE.md](ARCHITECTURE.md).

## Principles

- **Unix composition:** prefer existing GNU/Linux programs with inspectable inputs and outputs over bespoke capabilities.
- **Throwaway script:** short-lived Bash or Python glue for task-local state, branching, or coordination; not a permanent product surface.
- **Linux-only boundary:** GNU/Linux conventions and programs are supported; portability is not a design goal.

## Runs and sessions

**Turn**: one provider request/response cycle, including any tool calls it streams.
_Avoid_: cycle, iteration.

**Run**: one bounded turn-loop execution, ending in a final answer, the turn cap, or a user stop.
_Avoid_: invocation, request.

**Stop**: the user's cancellation of a live run, distinct from a provider or tool failure.
_Avoid_: cancel, abort, interrupt.

**Live turn**: the run in flight in the TUI transcript view, re-projected on every stream delta.
_Avoid_: streaming turn, current turn.

**Committed turn**: a finished run settled into the transcript, no longer re-projected.
_Avoid_: past turn, history.

**Session**: the append-only, GUID-named on-disk record of one run. It is not an editable conversation.
_Avoid_: conversation, chat history.

**Persisted transcript**: the message-layer JSONL record of provider requests and responses inside a session — the ground truth for debugging and performance work.
_Avoid_: message log, trace.

**TUI transcript view**: the rendered conversation in the terminal. Qualify "transcript" when the UI is meant.
_Avoid_: transcript (bare).

## Providers and context

**Provider**: a model endpoint behind one adapter, owning authentication, streaming, and generation control.
_Avoid_: backend, service.

**Dialect**: the provider-agnostic request/response shape, translated at the provider seam into a wire format.
_Avoid_: provider, adapter.

**Compaction**: model-based summarization of older turns when a request nears the provider's context window.
_Avoid_: compression, trimming.

**Compression**: deterministic, zero-LLM bounding of tool output by stripping ANSI and applying line and byte caps. Never interchangeable with compaction.
_Avoid_: compaction, truncation, clipping.

**Context overflow**: a provider refusal that a request exceeds its context window. It triggers emergency compaction.
_Avoid_: too many tokens, limit reached.

## Tools and workspace

**Toolset**: the fixed set of tools promised to the model. Eitri verifies the backing commands before launch.
_Avoid_: plugin set, tool bundle.

**Workspace**: the session's declared scope, writable by design. The current working directory is only the incidental process location.
_Avoid_: cwd, repo root.

**Sandbox**: the default boundary around `bash` — read-only system, writable workspace and session temp, isolated process namespace.
_Avoid_: cage, jail, container.

**Session temp**: the per-session writable directory for ephemeral artifacts, distinct from the system-wide `/tmp`.
_Avoid_: scratch space, tmpdir.

## Skills

**Skill**: a discovered, validated pack of instructions and resources. A skill may be human-invocable, model-invocable, or both.
_Avoid_: plugin, prompt pack, command.

**Skill activation**: the slash-command path that resolves a skill and injects its body into the next turn.
_Avoid_: invocation, execution.

**Subagent**: a batch-mode Eitri process the agent launches in an isolated execution directory. Its result is the batch envelope's `answer` field, not parsed prose.
_Avoid_: worker, child agent, task.

## Modes and surfaces

**Batch mode**: one `Run` from a prompt on the command line, then exit. Piped non-TTY stdin is appended after the prompt as fenced context — input, never instructions.
_Avoid_: non-interactive, CLI mode.

**Batch envelope**: the single machine-readable object a batch run emits at end: answer, session, turns, stopped.
_Avoid_: result JSON, output object.

**Debug mode**: records raw provider HTTP request and response bodies in the session.
_Avoid_: verbose mode, `-v`.

**Composer**: the TUI input surface for prompts, mentions, and slash commands.
_Avoid_: input box, prompt bar.

**Turn runtime**: the TUI surface that owns a live turn end to end — it starts the run, delivers its events in arrival order, and commits the finished turn. The TUI reaches a run only through the turn runtime.
_Avoid_: run loop, turn manager.

**Turn session**: the cancellable execution half of a live turn: the context the run runs under, and the means to stop it. It holds no view state.
_Avoid_: turn state, session state.
