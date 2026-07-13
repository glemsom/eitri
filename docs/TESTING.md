# Testing

Eitri uses multiple testing strategies: Go unit/integration tests, browser-based E2E tests (via chromedp), and optional manual browser testing.

## Development prerequisites

- Go 1.22+
- `tmux` on `$PATH`
- `templ` CLI matching the module's templ dependency when editing `.templ` files
- Chrome on Linux for browser E2E tests
- `curl`, `tar`, and `sha256sum` for installer/release smoke tests

Generated `*_templ.go` files are committed. Developers only need `templ` when changing `.templ` files or making release builds.

```bash
# Install templ at the version pinned by go.mod when needed
go install github.com/a-h/templ/cmd/templ@<pinned-version>

# Regenerate templates after editing .templ files
templ generate
```

## Quick start

```bash
# Run all unit + non-browser integration tests (requires tmux, no browser needed)
go test ./...

# Run a specific test package
go test ./internal/api/ -v -run TestHealth
```

## Test layers

| Layer | Tool | Run command | Requires |
|-------|------|-------------|----------|
| Unit + non-browser integration | `go test` | `go test ./...` | **tmux** |
| API integration | `httptest` | `go test ./internal/api/` | Nothing |
| **Browser E2E** | **chromedp** | `go test ./internal/api/` | **Chrome on Linux** |

---

## Browser tests (chromedp)

Browser tests verify the frontend UI loads, HTMX initializes correctly, config panel works, per-run SSE streaming after chat submit, sending messages works, and other DOM-level behaviors.

### Prerequisites

- **Chrome on Linux** is the v1 supported browser and automated release gate. The test helper `findChrome()` searches common locations:
  - `google-chrome-stable`, `google-chrome`, `chromium-browser`, `chromium`
  - `/usr/bin/google-chrome-stable`, `/usr/bin/chromium-browser`
- Chrome runs **headless** automatically — no display needed. Other browsers are best-effort and not release-gated.

If Chrome is not found, browser tests are skipped with a clear message.

### How they work

1. `newTestServer` creates a real `httptest.Server` with a fake LLM endpoint (returns canned SSE streams).
2. `newBrowserCtx` launches a headless Chrome instance via chromedp.
3. Tests navigate the browser, inspect DOM, type into inputs, click buttons.
4. DOM state is inspected via `chromedp.WaitVisible` / `chromedp.Text` — HTMX state lives in the DOM.
5. SSE events are simulated via fake SSE server using `httptest.Server` with chunked `text/event-stream` responses. Browser opens `EventSource` only after a successful chat submit.

### Test file

All browser tests live in:

```
internal/api/browser_test.go
```

Browser tests are **not** gated behind a build tag. Chrome-not-found skips at runtime with a clear message.

### Running browser tests

```bash
# Run all tests including browser tests (skipped if Chrome not found)
go test ./...

# Run only API tests (includes browser tests)
go test ./internal/api/ -v

# Run a specific browser test
go test ./internal/api/ -run TestBrowser_HarnessCanary -v
```

Browser tests are included in every `go test` run. If Chrome/Chromium is not found on the system, each test skips individually with `t.Skip`. No build tag required.

### What's tested

Test scenarios target HTMX + SSE architecture (no Alpine.js, no WebSocket, no xterm.js):

| Test | What it verifies |
|------|-----------------|
| Page load | Page loads, title correct, HTMX initializes |
| SSE stream state | Stream indicator is idle on page load, then connecting/streaming during an active run |
| Chat input | Input field and Send button present |
| Workspace indicator | Launch workspace path visible in chat/settings/skills pages |
| Setup banner | Missing/invalid provider config keeps chat visible, disables composer, and links to Settings |
| Settings page | `/settings` route returns full settings page |
| Config form | Form populated from API (`hx-get` fills form), provider defaults to OpenCode Go, advanced custom OpenAI option is labeled best-effort |
| Config save | Save via `hx-put` verifies `/v1/models` with entered credentials, then toast → form updated |
| API key field | Field is `type="password"`; OpenCode key required; custom OpenAI key optional |
| Empty input guard | Send button disabled when input empty |
| Input disabled during send | Input disabled + button shows state while agent runs; visible Stop button remains enabled |
| Cancel run | Stop button/Escape posts cancel, shows cancelling state, marks partial assistant response stopped, re-enables input |
| Full send→SSE→done cycle | Send message starts background run → browser opens per-run SSE → stream emits token, tool_call, tool_result, done → stream closes |
| User/agent bubbles | Message bubbles appear in DOM |
| Session ID cookie | Cookie set by server on first request |
| No browser console errors | Console clean |
| Model discovery in UI | Models fetched from `/v1/models`; selected model displayed; 401/discovery failures show friendly field/action errors |
| Friendly errors | Auth, rate limit, provider unreachable, unsupported streaming tool calls, context-limit, command timeout, and concurrent-run errors render actionable messages |
| Multi-message flow | Multi-message session works; second send while run active is rejected clearly |
| HTMX target swaps | HTMX swaps correct DOM elements |
| SSE reconnection | Reconnects on connection drop during an active run |
| Skills page | `/skills` route lists detected skills, statuses, scopes, paths, and diagnostics |
| Slash skill activation | `/skill`, `/skill prompt`, multiple leading skills, and unknown slash `422` behavior |
| Active skill chips | Skill activation updates current session chips without duplicate entries |
| File edit cards | `file_editor` overwrite shows diff, create shows created-file preview, large edits collapse by default |
| Stale session page | Reloading a stale `/sessions/{id}` after server restart redirects to `/` and creates/selects a new session |

### Adding new browser tests

1. Add a new `func TestBrowser_YourFeature(t *testing.T)` to `browser_test.go`
2. Use `newTestServer` + `newBrowserCtx` helpers
3. Use `chromedp.WaitVisible` / `chromedp.Text` for HTMX-rendered DOM assertions
4. Prefer `chromedp.SendKeys` over `chromedp.SetValue` (triggers HTMX events)

Example:

```go
func TestBrowser_MyFeature(t *testing.T) {
    // Create fake LLM server ("ok" or "error" mode)
    llmSrv := fakeChatServer(t, "ok")
    defer llmSrv.Close()

    // Create test server with RunManager (for chat/run features)
    server := newTestServerWithRuns(t)

    // Configure provider to point at fake LLM server
    configureProvider(t, server, llmSrv.URL)

    ctx, cancel := newBrowserCtx(t, server.URL)
    defer cancel()

    var title string
    err := chromedp.Run(ctx,
        chromedp.Navigate(server.URL+"/"),
        chromedp.Title(&title),
    )
    if err != nil {
        t.Fatalf("test failed: %v", err)
    }
    if title != "Eitri — AI Assistant" {
        t.Errorf("title = %q", title)
    }
}
```

For manual testing against a real server, set `EITRI_TEST_LLM_URL`:

```bash
EITRI_TEST_LLM_URL=https://my-opencode-server.example.com go test ./internal/api/ -run TestBrowser_SendMessage -v
```

---

## Unit & integration tests (no browser)

These test the Go backend without a browser:

| File | Tests |
|------|-------|
| `internal/api/server_test.go` | HTTP endpoints (health, chat, config, SSE) |
| `internal/agent/agent_test.go` | Agent initialization |
| `internal/agent/openai_model_test.go` | OpenAI-compatible model calls, OpenCode Go endpoints, Bearer auth, streaming tool-call assembly, unsupported provider behavior |
| `internal/agent/tools_test.go` | Tool execution, `file_editor` edit payloads, create-parent-dir behavior, direct write without confirmation |
| `internal/config/config_test.go` | Config load/save/merge, provider enum validation, model-discovery validation, secure file/dir permissions |
| `internal/executor/tmux_test.go` | Tmux command execution and initial working directory = launch workspace |
| `internal/executor/session_test.go` | Session lifecycle |
| `internal/executor/audit_test.go` | Preflight audit (tmux binary check) |
| `internal/runner/manager_test.go` | Runner manager |
| `internal/skills/skills_test.go` | Agent Skills discovery roots, precedence, shadowing, lenient validation, diagnostics, resource manifests, 200KB activation cap |
| `cmd/eitri/main_test.go` | CLI entry point, startup URL/workspace output, bind failure hint, `xdg-open` auto-open behavior |

Run them with:

```bash
go test ./... -v
```

### Release build and installer smoke

Release readiness requires:

```bash
templ generate
go test ./...
go test -tags=browser ./internal/api/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/eitri ./cmd/eitri
tar -C dist -czf dist/eitri-linux-amd64.tar.gz eitri
sha256sum dist/eitri-linux-amd64.tar.gz > dist/checksums.txt
```

Smoke installer behavior with a local fixture tarball/checksum before publishing:

- tarball contains binary named `eitri`
- checksum mismatch fails before overwrite
- successful install writes `~/.local/bin/eitri`
- missing `tmux` prints distro-specific hint
- missing `sha256sum` skips verification with clear warning only if spec allows that path

### Agent Skills required coverage

Because Agent Skills are v1 scope, release readiness requires tests for:

- Fixed discovery roots and precedence (`project-eitri` > `project-agents` > `user-eitri` > `user-agents`).
- Shadowing by duplicate `name`.
- Lenient validation hard skips vs warnings.
- `/skills`, `/api/skills`, and `/api/skills/refresh` contracts.
- `activate_skill` accepting only effective loaded skills, deduping per session, and enforcing 200KB body cap.
- Resource manifest cap and depth behavior.
- `file_viewer` path validation for workspace + skill directories, including path-escape rejection.
- `file_editor` remaining workspace-only.
- Slash activation parser for `/skill`, `/skill prompt`, multiple leading skills, and unknown slash `422`.

---

## Manual browser testing

For quick interactive testing:

```bash
go run ./cmd/eitri
# Open http://localhost:8080
```

Or use the `chrome-cdp` skill for scripted inspection (this is a test/dev helper, not an Eitri application feature):

```bash
# Take a screenshot
scripts/cdp.mjs shot <target>

# Get page HTML
scripts/cdp.mjs html <target>
```
