<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://github.com/glemsom/eitri/blob/main/internal/api/assets/face.webp">
  <img alt="Eitri" src="https://github.com/glemsom/eitri/blob/main/internal/api/assets/face.webp" width="160" height="160">
</picture>

# Eitri

**Self-hosted, single-binary AI coding agent for Linux.**

Named after the Norse blacksmith who forged Mjölnir. Eitri is an AI agent that runs on your own machine — it reads, writes, and runs code in your workspace, guided by natural language conversations.

[![CI](https://github.com/glemsom/eitri/actions/workflows/ci.yml/badge.svg)](https://github.com/glemsom/eitri/actions/workflows/ci.yml)

---

## Features

- **Single binary** — no runtime dependencies, no Docker, no Kubernetes. Just `eitri` on your `$PATH`.
- **Agent loop with built-in tools** — `bash`, `glob`, `grep`, `read`, `write`, `edit`, `web_fetch`, `render_mermaid_diagram`, and more.
- **Multi-provider LLM support** — works with OpenCode Go, GitHub Copilot, and Custom OpenAI providers. Configurable per session.
- **Agent Skills** — modular skill packs that extend the agent's capabilities per-project (like Agent Skills for GitHub Copilot).
- **Sub-agents** — the agent can delegate sub-tasks to subordinate agents via `delegate`/`collect` tools for parallel exploration.
- **Chat UI** — HTMX-based browser UI with SSE streaming, Mermaid diagram rendering, file diffs, and a live context panel.
- **Headless batch mode** — `eitri -b "your prompt"` runs the agent from the terminal without a browser, streaming output to stdout.
- **Self-hosted** — your data stays on your machine. No third-party cloud.

---

## Quick start

### 1. Install

**Linux (amd64 or arm64):**

```bash
curl -sSf https://raw.githubusercontent.com/glemsom/eitri/main/scripts/install.sh | bash
```

This downloads the latest release tarball, verifies its SHA256 checksum, and installs the `eitri` binary to `~/.local/bin`.

> Make sure `~/.local/bin` is on your `$PATH`. The installer will remind you if it isn't.

**Alternative — download manually:**

Download the tarball for your platform from the [releases page](https://github.com/glemsom/eitri/releases), then:

```bash
tar -xzf eitri-linux-amd64.tar.gz
sudo install -m 755 eitri /usr/local/bin/eitri
```

### 2. Configure (via the UI)

Eitri includes a settings page at [http://127.0.0.1:8080/settings](http://127.0.0.1:8080/settings).
After starting the server (step 3), open the settings page to configure:

- **LLM provider** — OpenCode Go, GitHub Copilot (device-flow OAuth), or Custom OpenAI
- **Model** — discoverable from the provider after configuring credentials
- **API key / token**
- **Thinking level** (for reasoning models)
- **System prompt**
- **Timeouts & limits** (session timeout, command timeout, max turns, context window)

All changes are saved and applied immediately — no config file editing needed.

### 3. Run

```bash
cd /your/project
eitri
```

Open [http://127.0.0.1:8080](http://127.0.0.1:8080) in a browser on Linux and start a conversation.

> Eitri works from the workspace you launch it in. It can read, write, and execute commands in that directory via the agent loop.

### 4. Headless mode (optional)

```bash
eitri -b "Refactor the database connection pool to use context cancellation"
```

This runs the agent once and streams text output to stdout — no browser needed.

---

## Configuration

Most settings are configured through the UI at [http://127.0.0.1:8080/settings](http://127.0.0.1:8080/settings)
— provider, model, API key, timeouts, system prompt, and more are all editable there.

Eitri stores configuration in `~/.eitri/config.json` under the hood, but you should use the
settings page rather than hand-editing the file.

### Environment variables

| Variable | Purpose |
|----------|---------|
| `EITRI_ADDR` | Listen address (default `127.0.0.1:8080`) |
| `EITRI_CONFIG` | Path to config file (default `~/.eitri/config.json`) |
| `EITRI_WORKSPACE` | Workspace directory (default current working directory) |
| `EITRI_OPEN_BROWSER` | `1` to force open browser, `0` to disable, unset for auto-detect |
| `EITRI_GITHUB_CLIENT_ID` | Override the built-in GitHub Copilot OAuth client ID |

---

## For developers

See [`CONTEXT.md`](CONTEXT.md) for the architecture guide, domain glossary, and development & release flow. Key commands:

```bash
make build    # Compile the binary
make test     # Run all tests
make run      # Build and start the server
```

---

## Security

- Eitri executes shell commands on your host machine via the agent loop. **Do not bind to a non-loopback address** unless you understand the risk — the UI has no authentication.
- Batch mode (`-b`) runs with confirmation requests automatically denied, which prevents the agent from performing risky operations.
- By default, Eitri listens on `127.0.0.1:8080` (loopback only).

---

## License

[GNU General Public License v3.0](LICENSE)
