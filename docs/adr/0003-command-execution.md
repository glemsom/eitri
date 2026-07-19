# Direct tmux command execution, no sandbox

> **Superseded by [ADR-0015](0015-remove-tmux-executor.md)** — the tmux-backed executor was replaced with direct `exec.Command` execution in the bash tool.

Execute shell commands directly on the host via tmux instead of sandboxing with Bubblewrap. Removes an entire subsystem (namespace setup, mount management, isolation verification) for v1, where Eitri runs single-user on localhost. Sandboxing is deferred — the `CommandExecutor` interface is designed to accommodate it later.

**Status**: Superseded

## Considered Options

- **Bubblewrap sandbox**: Isolate commands in Linux namespaces. Host root omitted, system paths mount read-only. Original prototype design. Adds complexity (namespace setup, binary whitelisting, mount configuration) with limited security benefit for single-user localhost deployment.
- **Direct tmux execution**: Commands run on host with full user privileges. Simpler implementation, faster startup, all host tools available by default. No isolation between agent and host filesystem.

## Consequences

No filesystem isolation means `file_viewer`/`file_editor` path validation (rejecting paths outside CWD) becomes a critical security control. Adding sandbox later means migrating the executor implementation behind the existing `CommandExecutor` interface.
