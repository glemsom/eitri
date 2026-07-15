# Eitri — Context

Self-hosted, single-binary AI Agent for Linux. Named after the Norse blacksmith who forged Mjölnir. V1 runs from the user's chosen workspace (process CWD), serves a Chrome-on-Linux browser UI, and supports OpenCode Go via its OpenAI-compatible endpoints.

## Domain glossary

| Term | Meaning |
|------|---------|
| **Agent** | ADK-driven LLM agent that processes prompts, calls tools, and returns responses. |
| **Session** | Single in-memory chat conversation. Has unique ID, message/render history, active-run state, and session-scoped tmux executor. Lost on server restart in v1. |
| **Tool** | Capability agent can invoke (`terminal_execute`, `file_viewer`, `file_editor`, `render_component`, `activate_skill`). Registered with ADK at agent creation. |
| **Provider** | External LLM service integration that owns authentication, model discovery, endpoint selection, and chat transport. A Provider exposes one or more Models. |
| **Skill** | Agent Skills-compatible directory containing `SKILL.md` instructions and optional `scripts/`, `references/`, and `assets/`. Discovered from fixed project/user roots and activated per session. |
| **Executor** | Session-scoped tmux-managed shell used for direct host command execution. No sandbox in v1. Starts in the launch workspace. |
| **Model** | LLM accessible via OpenCode Go's OpenAI-compatible API in v1, with custom OpenAI-compatible endpoints available as advanced/best-effort. Configured via Settings or `~/.eitri/config.json`; model IDs discovered from `/v1/models`. |
| **HTML-over-wire shell** | Go/Templ/HTMX-rendered application frame and fragments. Server owns canonical UI state and rendering. |
| **Browser island** | Isolated client-side behavior attached to server-rendered markup; owns only local ephemeral UI state. |
| **Stream island** | Browser island managing `EventSource` lifecycle and token display for one assistant run. |

## Architecture decisions

Architecture decisions are documented as ADRs in `docs/adr/`:

| ADR | Title | Status |
|-----|-------|--------|
| [0001](docs/adr/0001-google-adk-go-sdk.md) | Google ADK Go SDK for agent orchestration | Accepted |
| [0002](docs/adr/0002-openai-compatible-model.md) | OpenAI-compatible model abstraction | Accepted |
| [0003](docs/adr/0003-command-execution.md) | Direct tmux command execution, no sandbox | Accepted |
| [0004](docs/adr/0004-session-scoped-executor-lifecycle.md) | Session-scoped executor lifecycle | Accepted |
| [0005](docs/adr/0005-htmx-templ-ui.md) | HTMX + Templ shell with browser islands | Accepted |
| [0006](docs/adr/0006-agent-skills.md) | Agent Skills support | Accepted |
| [0007](docs/adr/0007-provider-profiles-and-github-copilot.md) | Provider profiles and GitHub Copilot | Accepted |

## Project structure

```
eitri/
├── cmd/eitri/                 # Entry point — starts HTTP+SSE server
├── internal/
│   ├── agent/                 # ADK agent setup + custom model.LLM + tools
│   ├── api/                   # HTTP server, SSE, HTMX/Templ render endpoints
│   │   └── templates/         # Templ source files and generated Go
│   ├── config/                # ~/.eitri config management
│   ├── runner/                # RunService — run lifecycle + ADK runner cache, SSE broadcast, auth persist callbacks
│   ├── skills/                # Agent Skills discovery, registry, activation
│   └── executor/              # tmux command execution + session lifecycle
├── scripts/                   # Install script
├── docs/ARCHITECTURE.md       # Architecture guide for AI agents
├── docs/TESTING.md            # Test runbook
├── docs/providers/            # User-facing provider setup/operation guides
├── docs/ROADMAP.md            # Non-normative post-v1 ideas
├── docs/adr/                  # Architecture Decision Records
├── docs/agents/               # Agent documentation framework
├── go.mod
├── initial.md                 # Original product vision
```

> **AI agents**: read `docs/ARCHITECTURE.md` before making changes — it covers module boundaries, key types, data flow, and extension points in detail.

## Running

```bash
go run ./cmd/eitri
# Open http://127.0.0.1:8080
```

Start Eitri from the workspace you want it to read/write. Configure the OpenCode Go API key and model via Settings or `~/.eitri/config.json`.
