# AI Agents for Linux — Research & Landscape

> **Last updated:** 2025-07-17  
> **Sources:** GitHub API, [awesome-cli-coding-agents](https://github.com/bradAGI/awesome-cli-coding-agents), official project pages

---

## 1. Terminal-Native Coding Agents

These are **AI agents that live in your terminal** — they can read, write, and execute code, run commands, and interact with your filesystem. The most popular category of AI agent on Linux.

### Tier 1: Major Platforms (⭐ > 50k)

| Agent | Stars | Provider | Description |
|-------|-------|----------|-------------|
| **OpenCode** | ⭐ 185k | Open-source | Terminal-native with 75+ provider support, LSP integration, privacy-first. Formerly `opencode-ai`. |
| **Claw Code** | ⭐ 195k | MIT | Clean-room Python/Rust rewrite of Claude Code architecture. Fastest repo to 100k stars. |
| **Hermes Agent** | ⭐ 214k | MIT | Nous Research's self-improving agent with persistent memory, skill creation, sandboxed execution. |
| **Gemini CLI** | ⭐ 106k | Google (Apache-2.0) | Google's terminal agent powered by Gemini models. |
| **Codex CLI** | ⭐ 97.6k | OpenAI (Apache-2.0) | OpenAI's local coding agent with interactive TUI and tool execution. |
| **OpenHands** | ⭐ 80.6k | MIT | Agentic dev environment (formerly OpenDevin) with CLI + web entrypoints. |
| **Pi** | ⭐ 70.3k | MIT | Minimal, adaptable terminal coding harness; unified LLM API, TUI, skills, MCP. |
| **Cline CLI** | ⭐ 64.6k | Apache-2.0 | Model-agnostic autonomous agent for planning, file edits, commands, browser use. |
| **Open Interpreter** | ⭐ 64.4k | AGPL-3.0 | "Do things on my machine" — executes code and actions from natural language. |
| **Goose** | ⭐ 51.1k | Apache-2.0 | Local, extensible agent; on-device execution, MCP integrations. |
| **Aider** | ⭐ 47.3k | Apache-2.0 | Pair-programming agent; strong git workflows, diffs/patches, multi-file. |

### Tier 2: Major Players (⭐ 10k – 50k)

| Agent | Stars | Description |
|-------|-------|-------------|
| **Continue CLI** | ⭐ 34.8k | Multi-model coding extension; local/privacy focus. |
| **Crush** | ⭐ 26.5k | Charmbracelet's glamorous TUI coding agent in Go; multi-provider, LSP-aware. |
| **Deep Agents Code** | ⭐ 26.2k | LangChain's official terminal agent; interactive TUI, subagents, human-in-the-loop. |
| **Kilo Code CLI** | ⭐ 26.1k | Agentic engineering platform; orchestrator mode, 100s of LLMs, skills. |
| **Qwen Code** | ⭐ 26k | Alibaba's CLI agent for Qwen coder models. |
| **Roo Code CLI** | ⭐ 24.3k | Multi-mode CLI agent (architect/code/debug/orchestrator). |
| **SWE-agent** | ⭐ 19.8k | Agent for resolving real repo issues/PRs; SWE-bench oriented. |
| **OH-MY-PI** | ⭐ 17.5k | Pi terminal coding agent; TypeScript/Rust monorepo. |
| **Plandex** | ⭐ 15.5k | "Plan-first" CLI agent; structured multi-file builds, 2M token context. |
| **OpenClaw** | ⭐ 383k | Personal AI assistant you run locally; CLI + multi-channel (WhatsApp/Slack/Discord). |
| **Smol Developer** | ⭐ 12.2k | Embeddable agent that generates entire codebases from a prompt. |

### Tier 3: Notable (⭐ 1k – 10k)

| Agent | Stars | Description |
|-------|-------|-------------|
| **MiMo Code** | ⭐ 11.9k | Xiaomi's official CLI agent; TUI, MCP, skills, git worktrees. |
| **Trae Agent** | ⭐ 11.8k | ByteDance's research-friendly CLI agent. |
| **Claude Engineer** | ⭐ 11.2k | Community-driven CLI for agentic Claude workflows. |
| **Claurst** | ⭐ 10.1k | Claude Code rewritten in idiomatic Rust. |
| **Kimi CLI** | ⭐ 9.2k | Moonshot AI's CLI coding agent with skills and MCP. |
| **Free Code** | ⭐ 8.6k | Claude Code fork — all telemetry removed, experimental features enabled. |
| **ForgeCode** | ⭐ 7.5k | AI pair programmer supporting 300+ models. |
| **Codebuff** | ⭐ 7.3k | Multi-agent coding assistant with CLI. |
| **OpenSquilla** | ⭐ 5.9k | Self-hostable microkernel agent runtime; sandboxed, persistent memory. |
| **Mistral Vibe** | ⭐ 4.7k | Mistral's CLI coding assistant. |
| **gptme** | ⭐ 4.4k | AI agent in terminal; persistent autonomous agents, git-backed memory. |
| **Letta Code** | ⭐ 2.8k | Memory-first CLI agent; persistent memory across sessions. |
| **Amazon Q Developer CLI** | ⭐ 2k | AWS's agentic terminal chat for building, debugging, DevOps. |
| **Codel** | ⭐ 2.5k | Autonomous agent via terminal; runs in Docker with web UI. |
| **RA.Aid** | ⭐ 2.2k | Autonomous coding agent (LangGraph); research/plan/implement pipeline. |
| **Nanocoder** | ⭐ 2.2k | Local-first CLI agent; BYO model, native tool calling, MCP. |

---

## 2. Agent Harnesses & Orchestration

Tools for **running, managing, and orchestrating multiple AI agents** side-by-side.

### Session Managers & Parallel Runners

| Tool | Stars | Description |
|------|-------|-------------|
| **vibe-kanban** | ⭐ 27.4k | Kanban interface for administering AI coding agents. |
| **cmux** | ⭐ 24.4k | Platform for running multiple coding agents in parallel. |
| **Superset** | ⭐ 12.4k | Terminal built for coding agents; orchestrates parallel sessions. |
| **Claude Squad** | ⭐ 8.1k | tmux-based harness for multiple Claude Code sessions. |
| **Emdash** | ⭐ 5.1k | Run multiple coding agents concurrently. |
| **agent-of-empires** | ⭐ 2.8k | Manage Claude Code, OpenCode, Codex, Gemini, Pi, etc. from TUI/web. |
| **Cate** | ⭐ 1.8k | Desktop app with infinite canvas; terminals + agent panels + editors. |
| **agent-deck** | ⭐ 486 | TUI session manager for multiple coding agents. |
| **ntm** | ⭐ 384 | Named Tmux Manager — spawn, tile, coordinate agents across tmux. |
| **amux** | ⭐ 296 | Run dozens of parallel sessions; web dashboard, self-healing. |

### Orchestrators & Autonomous Loops

| Tool | Stars | Description |
|------|-------|-------------|
| **claude-flow** | ⭐ 64.3k | Deploy multi-agent swarms with coordinated workflows. |
| **gastown** | ⭐ 17k | Multi-agent orchestration with persistent work tracking. |
| **AgentsMesh** | ⭐ 2.3k | AI Agent Workforce Platform; remote sandboxed agent pods. |
| **zeroshot** | ⭐ 1.6k | Planner + implementer + validators in isolated environments. |
| **Bernstein** | ⭐ 668 | Deterministic Python orchestrator; parallel agents, test verification. |
| **Aeon** | ⭐ 574 | Autonomous agent framework on GitHub Actions; 90+ skills. |

---

## 3. AI Agent Frameworks (Build Your Own)

Frameworks for **building custom AI agents** on Linux.

| Framework | Stars | Description |
|-----------|-------|-------------|
| **AutoGPT** | ⭐ 185k | Vision of accessible AI for everyone; build autonomous agents. |
| **CrewAI** | ⭐ 55.8k | Orchestrate role-playing, autonomous AI agents. |
| **LangChain** | ⭐ 104k (langchain) | Framework for LLM-powered applications and agents. |
| **SuperAgent** | ⭐ 5.2k | Deploy general-purpose AI agents to solve any task. |
| **n8n** (AI workflows) | ⭐ 58k+ | Workflow automation with AI agent nodes; self-hostable on Linux. |
| **Dify** | ⭐ 58k+ | LLMOps platform for building AI applications and agents. |
| **Flowise** | ⭐ 34k+ | Low-code LLM app builder with agent capabilities. |

---

## 4. Desktop AI Assistants on Linux

General-purpose AI assistants that work on Linux desktops.

| Tool | Description |
|------|-------------|
| **OpenClaw** | ⭐ 383k — Personal AI assistant; CLI + WhatsApp/Slack/Discord/Telegram. Fully local. |
| **Kristal** | AI desktop assistant for Linux with screen awareness and local processing. |
| **Ubuntu AI** | Canonical's AI assistant integration (snap-based). |
| **LLaMA.cpp** servers | Run local models and expose as assistant backends. |
| **Ollama** | ⭐ 115k — Run LLMs locally; not an agent itself, but the most common local model runner agents are built on. |
| **LocalAI** | ⭐ 30k+ — Local OpenAI API alternative; run models locally for agent backends. |
| **MCP Servers** | Model Context Protocol servers — expose Linux tools (filesystem, git, browser, etc.) to any MCP-compatible agent. |

---

## 5. Key Ecosystem Stats

- **90+ CLI coding agents** exist as of mid-2025 (per awesome list).
- **20+ harness/orchestration tools** for running agents in parallel.
- Most agents support **multiple LLM providers** (OpenAI, Anthropic, Google, local via Ollama/LM Studio).
- **MCP (Model Context Protocol)** is the emerging standard for agent-tool integration.
- **tmux** is the dominant multiplexer for running agents side-by-side on Linux.
- Languages: Rust (fastest growing), TypeScript/Node, Python, Go.

---

## 6. Quick Recommendations

**If you want a coding assistant:**
- **Aider** — best git integration, mature, easy setup
- **Open Interpreter** — general "do things on my machine" agent
- **Gemini CLI** — free tier, Google-backed
- **Pi** (this repo's tool) — minimal, hackable, local-first
- **Codex CLI** — OpenAI's official agent

**If you want to run agents in parallel:**
- **Claude Squad** — tmux-based, simple
- **agent-deck** — TUI for multiple agents
- **agent-of-empires** — most comprehensive agent manager

**If you want to build your own agent:**
- **CrewAI** — popular multi-agent framework
- **LangChain** — most mature LLM framework
- **OpenClaw** — full personal assistant platform

**If you want local/private agents:**
- **Ollama** + any agent that supports it (most do)
- **Nanocoder** — local-first design
- **Goose** — on-device execution
- **Pi** — local-first, minimal telemetry

---

*Sources: [awesome-cli-coding-agents](https://github.com/bradAGI/awesome-cli-coding-agents), GitHub API, official project documentation.*
