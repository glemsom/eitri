# Top 3 Improvement Recommendations for Eitri

## 1. Propagate cancellation to TUI background commands
**Files:** `internal/app/tui.go:1344`, `app/tui.go:1356`, `app/tui.go:1372`

`discoverCmd`, `skillCmd`, and `loginCmd` are launched on `context.Background()` inside Bubble Tea commands. When the user presses `esc`/`ctrl+c`, the engine turn stops but these goroutines keep running. They should inherit the model’s `turnCtx` (or a dedicated cancelable context) so background work respects user cancellation.

**Impact:** User experience / correctness — orphaned goroutines continue running after the user aborts.

---

## 2. Fail-fast on missing API key instead of sending sentinel credential
**Files:** `internal/provider/factory.go:29-34`, `internal/provider/openai.go:334-338`

`apiKeyOrDefault` returns `"not-configured"` when `OPENCODE_API_KEY` is empty. The production provider sends this string as a Bearer token, which guarantees an HTTP 401 but buries the root cause in a generic "provider returned HTTP 401" error. The factory should return a descriptive sentinel error (e.g. `ErrMissingAPIKey`) so users see a clear message before any network request is made.

**Impact:** Error handling / developer experience — eliminates a confusing 401 misdiagnosis.

---

## 3. Centralize hardcoded constants and extract duplicated HTTP client fallback
**Files:** `internal/tui/rail.go:50`, `internal/tui/rail.go:297`, `internal/compress/compress.go:29`, `internal/engine/compact.go:18`, `internal/app/app.go:54`, `internal/provider/openai.go:150-153`, `internal/provider/copilot.go:171-174`

Multiple packages define unrelated magic numbers with no shared constants file. The HTTP client nil-fallback pattern (`client := o.http; if client == nil { client = http.DefaultClient }`) is repeated 5+ times across provider files. Extract it into a helper or enforce via constructor defaulting, and move domain constants into a shared `internal/constants` package (or config).

**Impact:** Maintainability — reduces duplication and makes tuning behavior predictable.
