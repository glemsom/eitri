# Testing

Eitri uses Go unit/integration tests and browser-based E2E tests (via chromedp).

## Quick start

```bash
# Run all tests (browser tests skip gracefully if Chrome not found)
go test ./...

# Run with race detector
make test-race

# Full release readiness gate (includes race + browser tests)
make release-check

# Run a specific package
go test ./internal/api/ -v -run TestHealth
```

## Test layers

Provider integration tests use local fake-provider HTTP servers. Automated tests
must not call live OpenCode Go, GitHub Copilot, GitHub OAuth, or any external
model service.

| Layer | Tool | Run command | Requires |
|-------|------|-------------|----------|
| Unit + non-browser integration | `go test` | `go test ./...` | Nothing |
| API integration | `httptest` | `go test ./internal/api/` | Nothing |
| Browser E2E | chromedp | `go test ./internal/api/` | Chrome on Linux |

## Unit & integration tests (no browser)

| File | Tests |
|------|-------|
| `internal/api/server_test.go` | HTTP endpoints, request-body limits, logging |
| `internal/api/debug_test.go` | Debug API handler tests (sessions, runtime, config, HTTP traces, health) |
| `internal/api/debug_internal_test.go` | Debug API helper function unit tests (writeJSON, writeError, sanitizeConfig, sessionToSummary, loadConfig) |
| `internal/api/assets/js_test.go` | Static JS/CSS checks; `lightweightMarkdown` via Goja |
| `internal/history/session_test.go` | Session lifecycle, history, sliding window |
| `internal/fileutil/path_test.go` | Path validation |
| `internal/fileutil/filetools_test.go` | File operations |
| `internal/api/render_helpers_test.go` | Render helpers (hasMermaidComponent, stripMermaidCodeBlocks, renderSessionForPage, renderComponentsToHTML) |
| `internal/api/templates/helpers_test.go` | Template helpers (pathBase, scopeLabel, scopeIcon, statusDot, countLines) |
| `internal/api/templates/diff_test.go` | Diff text helpers (diffText, splitLines, escapeDiff, countLines) |
| `internal/config/config_test.go` | Config load/save/merge, provider validation |
| `internal/runner/manager_test.go` | Runner manager, cache keys |
| `internal/skills/skills_test.go` | Agent Skills discovery, shadowing, validation, resource caps |
| `cmd/eitri/main_test.go` | CLI entry point, bind/warning behavior |

## Browser tests (chromedp)

Browser tests verify frontend UI loads, HTMX initialization, config panel, SSE
streaming, chat submit, and other DOM-level behaviors.

### Prerequisites

- **Chrome on Linux** is the primary supported browser and automated release gate.
  The helper `findChrome()` searches common locations (google-chrome, chromium,
  etc.). Chrome runs headless automatically — no display needed.
- If Chrome is not found, tests skip with a clear message.

### How they work

1. `newTestServer` creates a real `httptest.Server` with a fake LLM endpoint (canned SSE streams).
2. `newBrowserCtx` launches headless Chrome via chromedp.
3. Tests navigate, inspect DOM, type, click — HTMX state lives in the DOM.
4. SSE events are simulated via chunked `text/event-stream` responses.

All browser tests live in a single file:

    internal/api/browser_test.go

Browser tests are **not** gated behind a build tag. Chrome-not-found skips at
runtime with `t.Skip`. All tests are listed in `browser_test.go` — the file is
the source of truth.

### Running

```bash
# All tests (browser skipped if no Chrome)
go test ./...

# API tests including browser
go test ./internal/api/ -run TestBrowser_SendMessage -v

# With race detector
make test-race
```

### Adding a new browser test

1. Add `func TestBrowser_YourFeature(t *testing.T)` to `browser_test.go`.
2. Use `newTestServer` / `newTestServerWithRuns` + `newBrowserCtx` helpers.
3. Use `chromedp.WaitVisible` / `chromedp.Text` for DOM assertions.
4. Prefer `chromedp.SendKeys` over `SetValue` (triggers HTMX events).

For manual testing against a real server:

```bash
EITRI_TEST_LLM_URL=https://my-server.example.com go test ./internal/api/ -run TestBrowser_SendMessage -v
```

## Manual browser testing

```bash
go run ./cmd/eitri
# Open http://localhost:8080
```

Or use the `chrome-cdp` skill for scripted inspection (test helper, not an Eitri feature):

```bash
scripts/cdp.mjs shot <target>
scripts/cdp.mjs html <target>
```
