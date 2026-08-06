# Architecture Decision Records

Architecture decisions are documented as ADRs in `docs/adr/`. This file is the canonical index.

| ADR | Title | Status |
|-----|-------|--------|
| [0001](adr/0001-htmx-templ-ui.md) | HTMX + Templ shell with browser islands | Accepted |
| [0002](adr/0002-agent-skills.md) | Agent Skills support | Accepted |
| [0003](adr/0003-provider-profiles-and-github-copilot.md) | Provider profiles and GitHub Copilot | Accepted |
| [0004](adr/0004-merge-tool-activity-into-inline-tool-cards.md) | Merge tool activity into inline tool cards | Accepted |
| [0005](adr/0005-prompt-caching.md) | Session-scoped prompt caching | Accepted |
| [0006](adr/0006-remove-adk-litellm-transport.md) | Remove ADK, adopt litellm transport + custom agent loop | Superseded by 0019 |
| [0007](adr/0007-split-render-component-into-per-component-tools.md) | Split render_component into per-component tools | Accepted |
| [0008](adr/0008-add-context-lines-to-grep-tool.md) | Add context lines to grep tool | Accepted |
| [0009](adr/0009-live-context-panel.md) | Live context window utilization panel | Accepted |
| [0010](adr/0010-remove-tmux-executor.md) | Replace tmux executor with direct exec.Command | Accepted |
| [0011](adr/0011-runagent-seam-interfaces.md) | Extract HistoryManager and Confirmer seam interfaces from RunAgent | Accepted |
| [0012](adr/0012-web-fetch-tool.md) | web_fetch tool for fetching URLs | Accepted |
| [0013](adr/0013-sub-agents.md) | Sub-agent support via delegate/collect tools | Accepted (amended) |
| [0014](adr/0014-crash-dumps.md) | Crash dump directory for unexpected failures | Accepted |
| [0015](adr/0015-per-session-workspaces.md) | Per-session workspaces with directory browser | Accepted |
| [0016](adr/0016-session-persistence-json-snapshots.md) | Session persistence via JSON snapshots | Accepted |
| [0017](adr/0017-bwrap-sandbox.md) | bwrap sandbox for bash tool | Accepted (amended) |
| [0018](adr/0018-personas.md) | Personas — named system prompts with skill injection | Accepted |
| [0019](adr/0019-adopt-litellm-client-for-transport.md) | Adopt litellm.Client for all LLM transport — replace hand-rolled adapters | Accepted |
| [0020](adr/0020-browser-tool-newremoteallocator.md) | `browser` tool via chromedp NewRemoteAllocator | Accepted |
| [0021](adr/0021-pattern-compression-for-bash-output.md) | Deterministic pattern compression for bash tool output | Accepted |
| [0022](adr/0022-save-only-settings-drafts.md) | Save-only settings drafts | Accepted |
| [0023](adr/0023-batch-runs-persist-like-ui-sessions.md) | Batch runs persist like UI sessions | Accepted (amended by 0025) |
| [0024](adr/0024-unified-parent-run-preparation.md) | Unified parent-run preparation across UI and batch modes | Accepted |
| [0025](adr/0025-unified-run-engine-across-subagent-and-batch.md) | Unified run engine across sub-agents and batch | Accepted |
