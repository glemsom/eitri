# Eitri domain context

Eitri is a self-hosted, single-binary AI coding agent for GNU/Linux. It reads, writes, and runs code in a declared workspace through natural-language conversation with local or hosted model providers. This document defines stable terms; implementation paths belong in [ARCHITECTURE.md](ARCHITECTURE.md).

## Principles

- **Unix composition:** Prefer existing GNU/Linux programs with inspectable inputs and outputs over bespoke capabilities.
- **Throwaway script:** Short-lived Bash or Python glue for task-local state, branching, or coordination; not a permanent product surface.
- **Linux-only boundary:** GNU/Linux conventions and programs are supported; portability is not a design goal.

## Terminology at a glance

| Term | Meaning |
| --- | --- |
| Turn | One provider request/response cycle. |
| Run | One bounded execution of the turn loop. |
| Session | The append-only, GUID-named on-disk record of a run. |
| Persisted transcript | JSONL message-layer record of provider requests and responses. |
| TUI transcript view | The rendered conversation in the terminal. |
| Provider | An adapter for a model endpoint, including authentication and streaming. |
| Toolset | The fixed tools promised to the model: `bash` and `open_in_browser`. |
| Workspace | The declared host directory the run may operate in. |
| Sandbox | The default bubblewrap boundary around `bash`. |
| Skill | A discovered and validated pack of agent instructions and resources. |
| Batch mode | One-shot execution that runs one `Run` and exits. |

## Runs and sessions

**Turn** is one provider request/response cycle, including any streamed tool calls. Avoid “cycle” and “iteration.”

**Run** is one bounded turn-loop execution: a batch invocation or one TUI submission. It ends with a final answer, the maximum-turn cap, or user stop. Avoid “invocation” and “request.”

**Session** is the append-only on-disk trail of one run, identified by a GUID. It is not an editable conversation or chat history.

**Persisted transcript** is the message-layer JSONL record inside a session: the ground truth for debugging and performance work. **TUI transcript view** is its rendered terminal counterpart; use the qualifier when referring to the UI.

## Providers and context

**Provider** is a model endpoint behind one adapter: model discovery, streaming, generation control, and authentication. “Dialect” is the provider-agnostic shape translated at the provider seam into a wire format.

**Compaction** is model-based summarization of older turns when context is near or beyond the provider limit. **Compression** is deterministic, zero-LLM bounding of tool output by stripping ANSI and applying line and byte caps. Do not use these terms interchangeably.

**Context overflow** is a provider refusal that the request exceeds its context window; it triggers emergency compaction.

## Tools and sandbox

**Toolset** is the fixed set of tools and backing commands unconditionally promised to the model. Eitri verifies them at launch.

**Workspace** is the session's declared scope and is writable by design. The current working directory is only the incidental process location.

**Sandbox** is the default bubblewrap boundary: read-only root, writable workspace and session temporary directory, and isolated PID, `/proc`, and `/dev` namespaces. `--yolo-unsafe` drops this boundary and runs bash directly as the user.

**Session temp** is the per-session writable directory for ephemeral artifacts. It is distinct from the system-wide `/tmp`.

## Skills

**Skill** is a discovered, validated pack of instructions and resources. Skills are resolved project > user > builtin; exact-name collisions are shadowed by the stronger scope. A skill may be human-invocable through `/skillname`, model-invocable through the rendered index, or both.

**Builtin skills root** is the materialized builtin-skill directory under `$EITRI_DIR`. Builtins are authored in the repository's skillpack source and overridden by same-named project or user skills.

**Skill activation** is the slash-command path that resolves a skill, records the invocation, and injects its body into the next turn.

**Subagent** is a batch-mode Eitri process launched by the agent in an isolated execution directory. Its machine-readable result is the `answer` field of the JSON batch envelope, not parsed prose.

## Modes and TUI

**Batch mode** runs one `Run` from `eitri -b <prompt>` and exits. Piped non-TTY stdin is appended after the prompt as fenced context; it is input, not instructions. `--format json` emits the machine-readable batch envelope.

**Debug mode** (`-d`) records raw HTTP request and response bodies in the session.

**Composer** is the TUI input surface for prompts, mentions, and slash commands. **Turn session** is the TUI owner of one run's context, cancellation, thinking state, and timeline.

**Stop** is the user's cancellation of a live run, represented by the dedicated `ErrStopped` sentinel. It is distinct from a provider or tool failure.
