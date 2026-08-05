# Testing

Eitri uses Go unit/integration tests and browser-based E2E tests (via chromedp).

## Quick start

```bash
# Run all tests with compact summary (browser tests skip gracefully if Chrome not found)
make test

# Run with race detector (same compact summary; DATA RACE warnings surfaced)
make test-race

# Full release readiness gate (includes race + browser tests, verbose output)
make release-check

# Run a specific package
cd internal/api && go test -v -run TestHealth
```

### Compact test summary

`make test` and `make test-race` route `go test` through `scripts/test.sh`,
which replaces the per-package boilerplate with a single verdict line:

```
VERDICT: FAIL 1/15 packages failed (14 passed, 2 failed test(s): TestLogin,TestWorkspace) in 47s — full log: dist/test-output.log
```

- **Verdict line** — one line with packages passed/failed and failing test
  names (comma-separated, truncated at 12). All-pass runs print
  `VERDICT: PASS N/N packages in …s`.
- **Failure details** — only the failing tests' `--- FAIL:` headers and their
  error excerpts are printed; passing-test output is not shown.
- **DATA RACE** — `make test-race` counts `DATA RACE` warnings in the verdict
  line and prints each full race report.
- **Build failures** — compile/vet error blocks (`# package` + errors) are
  surfaced instead of the raw `[build failed]` summary.
- **Artifact** — the full raw `go test` output is teed to
  `dist/test-output.log` (or `dist/test-race-output.log` for `--race`) so
  details can be grepped on demand. The file is overwritten on each run.
- **Exit code** — mirrors `go test` (0 all pass, 1 test/build failures).

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
| `internal/api/assets/css_test.go` | Embedded CSS lint: balanced braces, critical selectors, self-hosted fonts, design-token invariants (every `var()` token is declared, dark/light token roots are symmetric, no bare hex/rgba outside the token root — the CI stylelint rule for issue #1068) |
| `internal/history/session_test.go` | Session lifecycle, history, sliding window |
| `internal/session/session_test.go` | Session Manager lifecycle, CRUD, sub-stores, shared read accessors, copy helpers |
| `internal/session/bench_test.go` | Read-path allocation benchmarks (`Get`/shared accessors vs `CopySession`/`CopyConversation`) |
| `internal/fileutil/path_test.go` | Path validation |
| `internal/fileutil/filetools_test.go` | File operations |
| `internal/api/render_helpers_test.go` | Render helpers (hasMermaidComponent, stripMermaidCodeBlocks, renderSessionForPage, renderComponentsToHTML) |
| `internal/api/templates/helpers_test.go` | Template helpers (pathBase, scopeLabel, scopeIcon, statusDot, GravatarURL, SandboxBadge) |
| `internal/config/config_test.go` | Config load/save/merge, provider validation |
| `internal/sandbox/sandbox_test.go` | Sandbox `BwrapIsUsable`, `WrapCommand` unit tests and integration tests (`TestWrapCommand_*` skip if bwrap not usable) |
| `internal/runner/service_test.go`, `runconfig_test.go`, `loop_test.go`, `batch_test.go`, `batch_persist_test.go`, etc. | Runner service, run config, run loop, batch runs (incl. session snapshot/trace/timeline persistence and `EITRI_BATCH_SESSION_ID`), broadcasts |
| `internal/debug/recorder_test.go`, `tracemeta_test.go` | HTTP trace recording/enrichment; run/turn correlation IDs, time-to-first-token, round-tripper stamping |
| `internal/report/report_test.go` | Session report assembly, turn↔trace ID joins, retry attempt surfacing, timestamp-heuristic fallback |
| `internal/runstate/timeline_test.go` | Timeline condensation, `llm_call` correlation events, run ID generation |
| `internal/skills/skills_test.go` | Agent Skills discovery, shadowing, validation, resource caps |
| `cmd/eitri/main_test.go` | CLI entry point, bind/warning behavior, HTTP connection timeouts (stalled-header conn reaping, streaming exempt from write deadlines) |

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

All browser tests live across multiple files in `internal/api/`:

| File | Tests |
|------|-------|
| `browser_test.go` | Foundational tests (page load, HTMX init) |
| `browser_chat_test.go` | Send message, SSE streaming, tool cards |
| `browser_confirmation_test.go` | Confirmation prompts (approve/deny) |
| `browser_sessions_test.go` | Session CRUD, sidebar, load from disk |
| `browser_settings_test.go` | Settings page, provider config, model discovery |
| `browser_skills_test.go` | Skills UI, activation, diagnostics |
| `browser_workspace_test.go` | Workspace directory browser |
| `browser_fonts_test.go` | No external font/CDN requests; fonts served from embedded `/static/fonts/*` |
| `browser_stream_responsiveness_test.go` | Main-thread responsiveness during large reasoning streams |
| `browser_persona_keyboard_test.go` | Persona dropdown keyboard accessibility (arrow navigation, Enter/Space activation, Escape focus return, ARIA wiring) |

Browser tests are **not** gated behind a build tag. Chrome-not-found skips at
runtime with `t.Skip`.

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

1. Add `func TestBrowser_YourFeature(t *testing.T)` to the appropriate `internal/api/browser_*.go` file (or create a new one if it tests a new feature area).
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

## Verifying the deferred/lazy-load script strategy

All page scripts are non-render-blocking (`defer`), and the heavy rendering
libraries (mermaid ~2.7MB, KaTeX, Prism) are loaded on demand by
`eitri-lazy-load.js` only when content needs them (issue #968). To check the
improvement manually:

1. Open Chrome DevTools → **Network** (with "Disable cache" off) and reload the
   chat page.
2. A plain chat page must request **no** `mermaid.min.js`, `katex.min.js`, or
   `prism-*.js` — the only scripts in the document `<head>` are `htmx.min.js`
   and the small `eitri-*.js` islands, all marked `defer`.
3. Send a message containing a ```mermaid block, a code block, or a `$$...$$`
   equation: the corresponding library is fetched on that swap and the content
   renders identically to before.
4. In DevTools → **Performance**, record a page load and compare
   `DOMContentLoaded`/`First Contentful Paint` against the same page with the
   old build: first-interactive should be measurably faster on pages without
   rich content because ~4.7MB of JavaScript is no longer parsed up front.

### Verifying lazy-load failure degradation (issue #1078)

When an on-demand library fails to load (offline, blocked request), the page
must degrade gracefully instead of throwing an unhandled promise rejection and
silently losing the diagram/formatting:

1. Open Chrome DevTools → **Network** → right-click `mermaid.min.js` → **Block
   request URL**, reload a page containing a `mermaid` fenced code block.
2. The console shows **exactly one** `eitri-lazy-load: mermaid failed to load
   (...)` error — no `Uncaught (in promise)` — and the diagram's raw source is
   shown under a "Diagram renderer could not be loaded. Raw code:" message.
3. Repeat with `katex.min.js` (equations keep their raw LaTeX, now marked with
   a dotted underline) and `prism-core.min.js` (code blocks keep their raw
   text, now marked with a warning left border). Each library logs once and
   does not retry on later HTMX swaps.
