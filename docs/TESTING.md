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
# Run all tests (browser tests skip gracefully if Chrome not found; tmux required)
go test ./...

# Run a specific test package
go test ./internal/api/ -v -run TestHealth
```

## Test layers

Provider integration tests use local fake-provider HTTP servers. Automated tests must not call live OpenCode Go, GitHub Copilot, GitHub OAuth, or any external model service.

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

All currently implemented browser tests target the HTMX + SSE architecture (no Alpine.js, no WebSocket, no xterm.js):

| Test | What it verifies |
|------|-----------------|
| `TestBrowser_PageLoads` | Page loads with title "Eitri — Chat", HTMX initializes, `#chat-view`, `#messages`, `#composer` all present |
| `TestBrowser_HarnessCanary` | Basic navigation works, title contains "Eitri" |
| `TestBrowser_SetupBannerVisible` | Missing provider config keeps chat visible, disables composer, shows `#setup-banner` linking to `/settings` |
| `TestBrowser_SettingsPage` | `/settings` route loads, `#provider` select element present |
| `TestBrowser_SettingsFormElements` | Settings form renders `#provider`, `#api_key`, `#base_url`, `#model` fields; no chat-specific `#send-btn` on settings page |
| `TestBrowser_SettingsDirectNavigationPopulatesModels` | Direct navigation to `/settings` with saved provider config populates `#model` dropdown from live discovery |
| `TestBrowser_ConfigSavePopulatesModels` | Save via `hx-put` revalidates provider discovery, preserves discovered model options, and keeps selected model |
| `TestBrowser_ConfigSaveProviderFailure` | 4xx from provider validation does NOT populate models; form stays unchanged (HTMX no-swap on error responses) |
| `TestBrowser_HealthPage` | `/health` page renders with body containing "ok" |
| `TestBrowser_SendMessage` | Sends a message → user bubble appears with correct text, `#chat-input` disabled during active run |
| `TestBrowser_InputDisabledDuringRun` | During active run: `#chat-input` disabled, `#send-btn` disabled, `#stop-btn` visible |
| `TestBrowser_CancelRun` | Stop button re-enables input, hides stop button; partial assistant bubble present after cancellation |
| `TestBrowser_FindChrome` | `findChrome()` returns a path that exists and is executable |
| `TestBrowser_ChromeNotFoundSkips` | Chrome-not-found skip behavior works (self-verifying) |

#### Planned (not yet implemented)

The following scenarios are planned for future iterations:

| Scenario | Notes |
|----------|-------|
| SSE stream state (idle → connecting → streaming) | Stream indicator lifecycle |
| Workspace indicator | Launch workspace path visible across pages |
| API key field type | `type="password"` verification |
| Empty input guard | Send button disabled when input empty |
| Session ID cookie | Cookie set by server on first request |
| Console error check | No browser console errors during test |
| Model discovery error states | 401/discovery failures in UI |
| Friendly error rendering | Auth, rate limit, unreachable, context-limit, etc. |
| Multi-message flow | Second send while run active rejected |
| HTMX target swaps | Correct DOM element swapping |
| SSE reconnection | Reconnect on connection drop |
| Skills page and slash activation | `/skills` route, `/skill`, unknown slash `422` |
| Active skill chips | No duplicate entries on activation |
| File edit cards | Diff view, create preview, large-edit collapse |
| Stale session page | Redirect on stale `/sessions/{id}` |

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
| `internal/agent/openai_model_test.go` | OpenAI-compatible model calls, OpenCode Go and GitHub Copilot provider paths/headers, Bearer auth, streaming text/tool-call assembly, unsupported provider behavior, all via fake provider servers |
| `internal/agent/tools_test.go` | Tool execution, `file_editor` edit payloads, create-parent-dir behavior, direct write without confirmation |
| `internal/config/config_test.go` | Config load/save/merge, provider enum validation, model-discovery validation through fake provider servers, secure file/dir permissions |
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
make release-check
make release
```

Equivalent manual commands:

```bash
templ generate
go test ./...
go test -tags=browser ./internal/api/
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/eitri ./cmd/eitri
tar -C dist -czf dist/eitri-linux-amd64.tar.gz eitri
(cd dist && sha256sum eitri-linux-amd64.tar.gz > checksums.txt)
```

Smoke installer behavior with a local fixture tarball/checksum before publishing:

- tarball contains binary named `eitri`
- checksum mismatch fails before overwrite
- missing `checksums.txt` fails before overwrite
- successful install writes `~/.local/bin/eitri`
- missing `tmux` prints distro-specific hint
- if no SHA256 tool exists locally, installer warns and skips verification only after `checksums.txt` download succeeds

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
