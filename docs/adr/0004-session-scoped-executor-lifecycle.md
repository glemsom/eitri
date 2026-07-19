# Session-scoped executor lifecycle

> **Superseded by [ADR-0015](0015-remove-tmux-executor.md)** — the tmux-backed executor was replaced with direct `exec.Command` execution in the bash tool.

One long-lived tmux session per chat session, created on first command and destroyed on session timeout or explicit close. Shell state (env vars, CWD, running processes) persists between tool calls — matching how humans use a terminal. Idle timeout (default 30 min) and max session limit (10 concurrent) prevent resource exhaustion.

**Status**: Superseded

## Considered Options

- **Per-command executor**: Create/destroy tmux session per tool call. Clean isolation, no state leakage. But breaks natural workflows like `cd some-dir` followed by `go build`.
- **Session-scoped executor**: One tmux session per chat session. State persists across tool calls. Risk of state leakage between turns and zombie processes if Go process crashes.

## Consequences

Concurrent command safety requires mutex enforcement (one command at a time per session). Process group kill on `Close()` needed to prevent zombie tmux sessions after crash. Idle sessions consume tmux processes — timeout and max-session limits are mandatory, not optional.
