# Batch mode (`-b` flag)

Eitri supports a headless batch mode via the `-b` flag. Instead of starting the HTTP server and browser UI, it runs a single prompt against the agent loop and streams the output to stdout.

## Usage

```bash
eitri -b "implement the feature in issue #42"
```

The `-b` flag expects a prompt string (the remaining arguments after the flag). Output is streamed token-by-token to stdout as plain text (no SSE, no tool cards, no HTML).

## How it works

1. Loads config from `EITRI_CONFIG` env var or `~/.eitri/config.json`
2. Builds a minimal `RunService` — no UI session manager, no browser
3. Calls `BatchRun` which runs the agent loop with a request-based history manager
4. Streams text tokens to stdout in real-time (tool calls execute silently — only final text is streamed)
5. Exits with code 0 on success, non-zero on failure

## Caveats

- **No confirmations:** Confirmation requests are automatically denied. Ops that require confirmation will return errors to the LLM.
- **No browser session:** The agent cannot open browser tabs or interact with a UI.
- **No SSE/streaming UI:** Output is raw text only. Tool cards, chat bubbles, and the HTMX frontend are not available.
- **Config-driven:** The model, provider, workspace, and system prompt come from the config file. Set `EITRI_CONFIG` to use a non-default config.
- **Single-shot:** Each `eitri -b` invocation runs one prompt and exits. For processing multiple prompts in sequence, use the agent loop script (see below).

## Agent loop pattern

For processing a series of `ready-for-agent` issues in sequence, use `scripts/agent-loop.sh`:

```bash
./scripts/agent-loop.sh /path/to/repo
```

The script:

1. Lists open `ready-for-agent` issues sorted by number (oldest first)
2. For each issue, calls `eitri -b "implement the feature in issue #N — TITLE"`
3. Exits with 0 when no more issues remain
4. Exits non-zero if a batch run fails

This is designed for AFK (away-from-keyboard) batch processing of fully specified issues.

## Exit codes

| Code | Meaning                                      |
|------|----------------------------------------------|
| 0    | Success — agent loop completed               |
| 1    | Config load failure, auth failure, or run error |
| ≥2   | Unexpected runtime error or context cancelled |
