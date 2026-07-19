# Testing

Eitri uses multiple testing strategies: Go unit/integration tests, browser-based E2E tests (via chromedp), and optional manual browser testing.

## Development prerequisites

- Go 1.22+
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

## Agent test/build output policy

Do not paste large command output into chat.

For noisy commands (`go test`, builds, linters):
- write full output to log file
- report only summary, failures, and final 100-200 lines
- preserve original exit code
- use `set -o pipefail` when piping
- avoid verbose flags unless diagnosing
- rerun narrower scope before broader scope

Go-specific:
- avoid `go test -v ./...`
- start with package- or test-scoped runs
- run full `./...` near end

### Reading failures in test logs

A failed test outputs its failure message on the line(s) *after* the `FAIL` line. Grepping only for `FAIL` misses the actual failure details. Always include lines below each `FAIL` match.

Example: a log snippet may show the `FAIL` marker on one line and the assertion failure (`got ..., want ...`) on the next. The relevant output is the line(s) immediately following `FAIL`.

```bash
go test ./... > /tmp/go-test.log 2>&1
status=$?
grep -nE '^(FAIL|--- FAIL:)|panic:|^FAIL\t|^# ' /tmp/go-test.log -A 2 | tail -n 120 || true
tail -n 80 /tmp/go-test.log
exit $status
```

## Quick start

```bash
# Run all tests (browser tests skip gracefully if Chrome not found)
go test ./...

# Run a specific test package
go test ./internal/api/ -v -run TestHealth
```

## Test layers

Provider integration tests use local fake-provider HTTP servers. Automated tests must not call live OpenCode Go, GitHub Copilot, GitHub OAuth, or any external model service.

| Layer | Tool | Run command | Requires |
|-------|------|-------------|----------|
| Unit + non-browser integration | `go test` | `go test ./...` | Nothing |
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
| `TestBrowser_SettingsFormElements` | Settings form renders `#provider`, `#api_key`, `#base_url`, `#model`, and `#system_prompt` fields; no chat-specific `#send-btn` on settings page |
| `TestBrowser_SettingsDirectNavigationPopulatesModels` | Direct navigation to `/settings` with saved provider config populates `#model` dropdown from live discovery |
| `TestBrowser_InitialConfigSavePopulatesModels` | First save without selected model discovers models, swaps updated form, and leaves model unselected for second save |
| `TestBrowser_ConfigSavePopulatesModels` | Save via `hx-put` revalidates provider discovery, preserves discovered model options, and keeps selected model |
| `TestBrowser_ConfigSaveProviderFailure` | Provider validation failure swaps updated settings form with visible error feedback; model list stays unpopulated |
| `TestBrowser_HealthPage` | `/health` page renders with body containing "ok" |
| `TestBrowser_SendMessage` | Sends a message → user bubble appears with correct text, `#chat-input` disabled during active run |
| `TestBrowser_RunStatusChrome_ShowsNoDeadAirAndDone` | Run-status chrome starts at idle, shows connecting/no-dead-air copy while first token is delayed, then reaches done after final render |
| `TestBrowser_RunStatusChrome_ReconnectAndActivityPanel` | Stream chrome shows reconnecting and rendering phases, and hidden-by-default activity panel records tool start/finish entries for current session |
| `TestBrowser_InputDisabledDuringRun` | During active run: `#chat-input` disabled, `#send-btn` disabled, `#stop-btn` visible |
| `TestBrowser_CancelRun` | Stop button re-enables input, hides stop button; partial assistant bubble present after cancellation |
| `TestBrowser_ComposerEnterSendsAndShiftEnterAddsNewline` | Enter sends chat message; Shift+Enter keeps multiline composer content intact before send |
| `TestBrowser_ComposerCompletionKeyboardAndNestedPaths` | Completion keyboard controls handle Tab/Shift+Tab/Arrow/Escape flow; file completions keep workspace-relative nested paths |
| `TestBrowser_EscapeCancelsActiveRun` | Global Escape key cancels active run even after composer disables during streaming |
| `TestBrowser_FindChrome` | `findChrome()` returns a path that exists and is executable |
| `TestBrowser_ChromeNotFoundSkips` | Chrome-not-found skip behavior works (self-verifying) |
| `TestBrowser_RichRenderingAssetsAndBehavior` | Embedded Prism/KaTeX/Mermaid assets load; code copy button, Mermaid blocks/components, and math rendering degrade/read correctly |
| `TestBrowser_DiffCardsToggleAndCollapseAfterHTMXSwap` | DiffCard and file edit diff UIs support unified/side-by-side toggle plus collapsed unchanged-region expansion after HTMX swaps |
| `TestBrowser_ToolCardsRunningToDone` | Tool entry appears in sidebar with running timer, morphs to done with checkmark/name/output; no tool entries in `#messages` |
| `TestBrowser_ToolCardsInsertBeforeSentinel` | Sidebar entry appears for tools-run-first scenario (no streaming bubble); streaming correctly placed before scroll-sentinel; no tool entries in `#messages` |
| `TestBrowser_ToolCardMorphInPlace` | Three sequential tool calls each produce unique sidebar entries with done status; no duplicate data-tool-key |
| `TestBrowser_ToolCardsInScrollContainer` | Tool entry appears in scrollable sidebar panel after token creates streaming bubble; streaming unaffected; final render preserves scroll-sentinel; no tool entries in `#messages` |
| `TestBrowser_StreamingMarkdownBold` | `**bold**` renders as `<strong>` during streaming |
| `TestBrowser_StreamingMarkdownItalic` | `*italic*` renders as `<em>` during streaming |
| `TestBrowser_StreamingMarkdownInlineCode` | `` `code` `` renders as `<code>` during streaming |
| `TestBrowser_StreamingMarkdownLink` | `[text](url)` renders as `<a href="...">` during streaming |
| `TestBrowser_StreamingMarkdownLinkXSS` | `[click](javascript:alert(1))` renders as plain text (no `<a>`) |
| `TestBrowser_StreamingMarkdownDataURL` | `[bad](data:text/html,...)` renders as plain text (no `<a>`) |
| `TestBrowser_StreamingMarkdownMailto` | `[me](mailto:u@h.com)` produces `<a href="mailto:...">` with `target=_blank rel=noopener` |
| `TestBrowser_StreamingMarkdownHTTPLink` | `[text](http://...)` produces `<a>` with `target=_blank rel=noopener` |
| `TestBrowser_StreamingMarkdownHTTPSLink` | `[text](https://...)` produces `<a>` with `target=_blank rel=noopener` |
| `TestBrowser_StreamingMarkdownParagraphs` | `\n\n` creates `<p>` boundaries during streaming |
| `TestBrowser_StreamingMarkdownMixed` | Mixed **bold**, *italic*, and `code` render correctly during streaming |
| `TestBrowser_StreamingMarkdownIncomplete` | Unclosed `**text` stays as raw text (graceful degradation) |
| `TestBrowser_StreamingMarkdownRenderingPhaseCSS` | `.streaming-message.rendering` appears after `done` before final swap |
| `TestBrowser_StreamingMarkdownFinalRenderCodeBlock` | Fenced code block with Prism highlighting renders after `done` |
| `TestBrowser_StreamingMarkdownFinalRenderMath` | `$$formula$$` renders with KaTeX after `done` |
| `TestBrowser_StreamingMarkdownFinalRenderMermaid` | `\`\`mermaid\`\`` diagram renders after `done` |
| `TestBrowser_StreamingMarkdownAutoScrollRegression` | Multi-paragraph markdown scrolls to bottom during streaming |

#### Planned (not yet implemented)

The following scenarios are planned for future iterations:

| Scenario | Notes |
|----------|-------|
| Workspace indicator | Launch workspace path visible across pages |
| API key field type | `type="password"` verification |
| Empty input guard | Send button disabled when input empty |
| Session ID cookie | Cookie set by server on first request |
| Console error check | No browser console errors during test |
| Model discovery error states | 401/discovery failures in UI |
| Friendly error rendering | Auth, rate limit, unreachable, context-limit, etc. |
| Multi-message flow | Second send while run active rejected |
| HTMX target swaps | Correct DOM element swapping |
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
    if !strings.Contains(title, "Eitri") {
        t.Errorf("title does not contain Eitri: %q", title)
    }
}
```

For manual testing against a real server, set `EITRI_TEST_LLM_URL`:

```bash
EITRI_TEST_LLM_URL=https://my-opencode-server.example.com go test ./internal/api/ -run TestBrowser_SendMessage -v
```

---

## Unit & integration tests (no browser)

`js_test.go` also contains `TestLightweightMarkdown`, a pure-function unit test that extracts the
`lightweightMarkdown` function from `eitri-stream.js` and executes it via Goja (a Go JS runtime).
This tests the function's correctness directly — bold, italic, inline code, links (http/https/mailto),
disallowed schemes (javascript:, data:), incomplete/unclosed markers, paragraph breaks, and mixed
patterns — without launching a browser. It runs as part of `go test ./internal/api/assets/`.

These test the Go backend without a browser:

| File | Tests |
|------|-------|
| `internal/api/server_test.go` | HTTP endpoints (health, chat, config, SSE), request-body limits, request logging |
| `internal/api/assets/js_test.go` | Static checks: JS/CSS file presence, expected function exports, removed deactivated functions; `TestLightweightMarkdown` runs `lightweightMarkdown` via Goja |
| `internal/history/session_test.go` | Session lifecycle, history, sliding window cap |
| `internal/fileutil/path_test.go` | Path validation |
| `internal/fileutil/filetools_test.go` | File operations (ReadFile, EditFile, InsertLine, WriteFile, ListDirectory) |
| `internal/config/config_test.go` | Config load/save/merge, provider enum validation, model-discovery validation through fake provider servers, secure file/dir permissions |
| `internal/runner/manager_test.go` | Runner manager, including cache keys for config-dependent agent prompt changes |
| `internal/skills/skills_test.go` | Agent Skills discovery roots, precedence, shadowing, lenient validation, diagnostics, resource manifests, 200KB activation cap |
| `cmd/eitri/main_test.go` | CLI entry point, startup URL/workspace output, bind failure hint, non-loopback warning, `xdg-open` auto-open behavior |

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
- missing `tmux` prints distro-specific hint (installer still works; tmux no longer required)
- if no SHA256 tool exists locally, installer warns and skips verification only after `checksums.txt` download succeeds

### Agent Skills required coverage

Because Agent Skills are v1 scope, release readiness requires tests for:

- Fixed discovery roots and precedence (`project-eitri` > `project-agents` > `user-eitri` > `user-agents`).
- Shadowing by duplicate `name`.
- Lenient validation hard skips vs warnings.
- `/skills`, `/api/skills`, and `/api/skills/refresh` contracts, including refresh-trigger behavior and surfaced diagnostics for malformed/unreadable Skills.
- `skill` accepting only refreshed effective loaded skills, deduping per session, and enforcing 200KB body cap.
- Resource manifest cap and depth behavior.
- `read` path validation for workspace + skill directories, including path-escape rejection.
- `write` and `edit` workspace-only validation.
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
