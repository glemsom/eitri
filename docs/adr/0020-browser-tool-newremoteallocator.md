# 0020 — `browser` tool via chromedp NewRemoteAllocator

- **Status:** Accepted
- **Date:** 2025-07-27

## Context

Eitri's agent can read web content via `web_fetch` (plain HTTP), but cannot inspect or interact with a live browser — no DOM inspection, no JavaScript execution, no click/type/navigate operations. Adding a `browser` tool gives the agent the ability to debug a running web application by connecting directly to the user's Chrome instance via the Chrome DevTools Protocol (CDP).

## Decision

Add a built-in `browser` tool in `internal/tool/browser.go` that uses `chromedp.NewRemoteAllocator` to connect to an already-running Chrome browser on the user's machine.

### Connection model

- **Default endpoint:** `ws://127.0.0.1:9222` (configurable via Settings UI → `~/.eitri/config.json`)
- **Lifecycle:** Lazy persistent connection — connects on first tool use, held for the session. Disconnects when the session ends.
- **Browser:** The user starts Chrome themselves with `--remote-debugging-port=9222`. Eitri does not manage the browser process.

### Tool surface

Single verb-driven tool `browser` with a `"action"` parameter covering: `list_targets`, `get_dom`, `screenshot`, `click`, `type`, `navigate`, `new_tab`, `close_tab`, `select`, `get_value`.

- `new_tab` opens a fresh tab and returns its `target_id` for subsequent actions.
- `close_tab` closes a tab.
- `select` sets an HTML `<select>` dropdown to a given option value.
- `get_value` reads back the current value of a form element.

### Tab interaction

- **Mode:** Attach to existing user tabs (the user's actual Chrome tabs). `list_targets` returns all open tabs.
- **Navigation:** `navigate` can redirect the user's current tab (full power, no guardrails).
- **No confirmation prompts** — user stops the session via Chrome's connection UI if needed.

### Why NewRemoteAllocator over alternatives

| Option | Why rejected |
|--------|-------------|
| **Launch our own Chrome** (`NewExecAllocator`) | Creates a hidden headless browser invisible to the user. Cannot debug the user's actual running application. Adds process lifecycle management, duplicate Chrome instances, and resource overhead. |
| **Selenium / Playwright** | Heavier dependency tree. chromedp is already in the dependency graph for browser tests. No benefit over raw CDP for the tool's requirements. |
| **Puppeteer (Node)** | Would require a Node runtime dependency. Eitri is a Go binary. |

## Consequences

- Positive: agent can inspect the real DOM of a running web app, click buttons, fill forms, navigate — enabling debugging, testing, and automation workflows previously impossible.
- Positive: leverages `chromedp` which is already a dependency (used in browser tests) — no new major dependencies.
- Positive: `NewRemoteAllocator` handles WebSocket URL discovery automatically (resolves `http://127.0.0.1:9222/` to the correct `ws://...` endpoint).
- Positive: no process lifecycle management — Chrome is the user's responsibility.
- Negative: user must start Chrome with `--remote-debugging-port=9222` themselves. Requires one-time setup.
- Negative: tool has no effect if Chrome isn't running with the debugging port open — error messages guide the user.
- Negative: persistent WebSocket connection consumes Chrome resources per attached target.
- Negative: the LLM sees all open tabs (privacy consideration — the user is informed via Settings UI).
