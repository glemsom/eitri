# Architecture Decision Records

This is the canonical index of all Architecture Decision Records (ADRs) in the project. Each ADR lives in `docs/adr/` and records a significant architectural decision, its context, and its consequences.

| Number | Title | Status | Link |
|--------|-------|--------|------|
| 0001 | HTMX + Templ shell with browser islands | Accepted | [docs/adr/0001-htmx-templ-ui.md](adr/0001-htmx-templ-ui.md) |
| 0002 | Agent Skills support | Accepted | [docs/adr/0002-agent-skills.md](adr/0002-agent-skills.md) |
| 0003 | Provider profiles and GitHub Copilot | Accepted | [docs/adr/0003-provider-profiles-and-github-copilot.md](adr/0003-provider-profiles-and-github-copilot.md) |
| 0004 | Merge tool activity into inline tool cards | Accepted | [docs/adr/0004-merge-tool-activity-into-inline-tool-cards.md](adr/0004-merge-tool-activity-into-inline-tool-cards.md) |
| 0005 | Session-scoped prompt caching | Accepted | [docs/adr/0005-prompt-caching.md](adr/0005-prompt-caching.md) |
| 0006 | Remove ADK, adopt litellm transport + custom agent loop | Accepted | [docs/adr/0006-remove-adk-litellm-transport.md](adr/0006-remove-adk-litellm-transport.md) |
| 0007 | Split render_component into per-component tools | Accepted | [docs/adr/0007-split-render-component-into-per-component-tools.md](adr/0007-split-render-component-into-per-component-tools.md) |
| 0008 | Add context lines to grep tool | Accepted | [docs/adr/0008-add-context-lines-to-grep-tool.md](adr/0008-add-context-lines-to-grep-tool.md) |
| 0009 | Live context window utilization panel | Accepted | [docs/adr/0009-live-context-panel.md](adr/0009-live-context-panel.md) |
| 0010 | Replace tmux executor with direct exec.Command | Accepted | [docs/adr/0010-remove-tmux-executor.md](adr/0010-remove-tmux-executor.md) |
| 0011 | Extract HistoryManager and Confirmer seam interfaces from RunAgent | Accepted | [docs/adr/0011-runagent-seam-interfaces.md](adr/0011-runagent-seam-interfaces.md) |
| 0012 | web_fetch tool for fetching URLs | Accepted | [docs/adr/0012-web-fetch-tool.md](adr/0012-web-fetch-tool.md) |
| 0013 | Sub-agent support via delegate/collect tools | Accepted | [docs/adr/0013-sub-agents.md](adr/0013-sub-agents.md) |
| 0014 | Crash dump directory for unexpected failures | Accepted | [docs/adr/0014-crash-dumps.md](adr/0014-crash-dumps.md) |
| 0015 | Per-session workspaces with directory browser | Accepted | [docs/adr/0015-per-session-workspaces.md](adr/0015-per-session-workspaces.md) |
| 0016 | Session persistence via JSON snapshots | Accepted | [docs/adr/0016-session-persistence-json-snapshots.md](adr/0016-session-persistence-json-snapshots.md) |
| 0017 | bwrap sandbox for bash tool | Accepted (amended) | [docs/adr/0017-bwrap-sandbox.md](adr/0017-bwrap-sandbox.md) |
| 0018 | Personas — named system prompts with skill injection | Accepted | [docs/adr/0018-personas.md](adr/0018-personas.md) |
| 0019 | Adopt litellm.Client for all LLM transport — replace hand-rolled adapters | Accepted | [docs/adr/0019-adopt-litellm-client-for-transport.md](adr/0019-adopt-litellm-client-for-transport.md) |
| 0020 | `browser` tool via chromedp NewRemoteAllocator | Accepted | [docs/adr/0020-browser-tool-newremoteallocator.md](adr/0020-browser-tool-newremoteallocator.md) |
| 0021 | Deterministic pattern compression for bash tool output | Accepted | [docs/adr/0021-pattern-compression-for-bash-output.md](adr/0021-pattern-compression-for-bash-output.md) |
| 0022 | Save-only settings drafts | Accepted | [docs/adr/0022-save-only-settings-drafts.md](adr/0022-save-only-settings-drafts.md) |
