//go:build e2e

package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// sharedAllocCtx is a shared Chrome allocator reused across all browser tests
// to avoid launching a separate Chrome process for each test, which causes
// memory/resource contention on constrained CI runners.
var sharedAllocCtx context.Context

// TestMain initialises the shared Chrome allocator once for all browser tests.
func TestMain(m *testing.M) {
	chromePath := findChrome()
	if chromePath == "" {
		// No Chrome available — all browser tests will skip.
		os.Exit(m.Run())
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(chromePath),
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.WSURLReadTimeout(60*time.Second), // CI runners can be slow to start Chrome
		)...)

	sharedAllocCtx = allocCtx

	code := m.Run()
	allocCancel()
	os.Exit(code)
}

// streamFlushWindow is the delay before sending the stop signal in streaming
// markdown tests, giving the browser's flush timer time to fire.
const streamFlushWindow = 1 * time.Second

// findChrome searches common locations for a Chrome/Chromium binary.
// Returns empty string if not found.
func findChrome() string {
	candidates := []string{
		"google-chrome-stable",
		"google-chrome",
		"chromium-browser",
		"chromium",
		"/usr/bin/google-chrome-stable",
		"/usr/bin/chromium-browser",
	}
	for _, path := range candidates {
		if _, err := exec.LookPath(path); err == nil {
			return path
		}
	}
	return ""
}

// newBrowserCtx creates a new chromedp context from the shared Chrome allocator.
// This avoids launching a separate Chrome process for each test, reducing
// memory and CPU contention on CI runners.
func newBrowserCtx(t *testing.T, srvURL string) (context.Context, context.CancelFunc) {
	t.Helper()

	if sharedAllocCtx == nil {
		t.Skip("Chrome/Chromium not found — skipping browser test")
	}

	// Retry browser startup on flaky CI runners — websocket URL timeout
	// is the most common failure mode on resource-constrained GitHub runners.
	const maxRetries = 3
	var (
		ctx                       context.Context
		ctxCancel                 context.CancelFunc
		err                       error
	)
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, ctxCancel = chromedp.NewContext(sharedAllocCtx)
		err = chromedp.Run(ctx)
		if err == nil {
			return ctx, ctxCancel
		}
		ctxCancel()
		if attempt < maxRetries {
			t.Logf("browser startup attempt %d/%d failed: %v — retrying", attempt, maxRetries, err)
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	t.Fatalf("failed to start browser after %d attempts: %v", maxRetries, err)
	return nil, nil // unreachable
}

// waitForComposerReady waits until the composer input and completion menu are
// fully initialised and connected.
func waitForComposerReady(t *testing.T, ctx context.Context) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var ready bool
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var input = document.querySelector('#chat-input');
				var menu = document.querySelector('#completion-menu');
				return !!input && !!menu && input.getAttribute('aria-controls') === 'completion-menu';
			})()`, &ready),
		)
		if err == nil && ready {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("composer did not initialize")
}

// pollForCondition repeatedly evaluates check until it returns true
// or the deadline expires. Useful as a deterministic replacement for time.Sleep
// when waiting for browser-side state changes (IntersectionObserver, streaming).
func pollForCondition(t testing.TB, timeout, interval time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(interval)
	}
}

// waitForLazyLibraries waits until the given JS globals are defined in the
// browser. The heavy rendering libraries (mermaid, katex, Prism) are now loaded
// on demand by eitri-lazy-load.js, so browser tests must wait for them to
// arrive before asserting rendering behaviour. (issue #968)
func waitForLazyLibraries(t testing.TB, ctx context.Context, globals ...string) {
	t.Helper()

	want := make([]string, 0, len(globals))
	for _, g := range globals {
		want = append(want, `typeof `+g+` !== 'undefined'`)
	}
	expr := strings.Join(want, " && ")

	pollForCondition(t, 20*time.Second, 100*time.Millisecond, func() bool {
		var ready bool
		_ = chromedp.Run(ctx, chromedp.EvaluateAsDevTools(expr, &ready))
		return ready
	})

	// Verify every global actually became defined.
	var check string
	for _, g := range globals {
		var defined bool
		if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`typeof `+g+` !== 'undefined'`, &defined)); err != nil {
			t.Fatalf("evaluating %s presence: %v", g, err)
		}
		if !defined {
			check += " " + g
		}
	}
	if check != "" {
		t.Fatalf("lazy libraries never loaded:%s", check)
	}
}

// fakeChatServer, configureProvider and putBrowserConfig used to live here as
// shared helpers; they moved to testhelpers_provider_test.go (untagged) so the
// non-browser integration tests and browser E2E tests both compile. testLLMURL
// depends on fakeChatServer.

// testLLMURL returns the LLM provider URL for browser chat tests.
// If EITRI_TEST_LLM_URL is set, it returns that value for manual testing.
// Otherwise, it returns the fakeChatServer URL.
func testLLMURL(t *testing.T) string {
	if envURL := os.Getenv("EITRI_TEST_LLM_URL"); envURL != "" {
		return envURL
	}
	return fakeChatServer(t, "ok").URL
}

func fakeInstantChatServer(t *testing.T, reply string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", now, reply)
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeDelayedFirstTokenChatServer(t *testing.T, delay time.Duration, reply string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay):
			}

			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", now, reply)
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeMarkdownChatServer emits streaming tokens with markdown content for testing
// streaming markdown formatting in the browser.
// When singleToken is true, emits the full reply as one chunk and skips the pre-stop delay.
// preStopDelay controls how long the server waits before sending the stop signal
// after all content tokens (ignored when singleToken is true). This is NOT an inter-token
// pacing delay — it gives the browser's flush timer window to fire before completion.
func fakeMarkdownChatServer(t *testing.T, reply string, preStopDelay time.Duration, singleToken bool) *httptest.Server {
	t.Helper()

	tokens := []string{reply}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)

		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			if singleToken {
				// Emit entire reply as one token chunk, skip pre-stop delay
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", now, reply)
				flusher.Flush()
			} else {
				for _, tok := range tokens {
					select {
					case <-r.Context().Done():
						return
					default:
					}
					fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", now, tok)
					flusher.Flush()
				}

				// Wait before sending stop/done so browser flush timer fires (80ms)
				select {
				case <-r.Context().Done():
					return
				case <-time.After(preStopDelay):
				}
			}

			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// streamingMarkdownTestOptions controls single-token vs multi-token streaming mode.
type streamingMarkdownTestOptions struct {
	// SingleToken sets whether the fake LLM server emits the full reply as one token.
	// Used for final-render regression tests (code blocks, math, mermaid).
	SingleToken bool
	// Timeout overrides the default 4s deadline for the render assertion.
	// Used when SingleToken=true (tests need longer for Prism/KaTeX/Mermaid).
	Timeout time.Duration
}

// streamingRenderedRootJS returns the current on-screen assistant message.
// While a run is still streaming this is the live #streaming element; once the
// final render has swapped it out, it is the latest committed
// .message-assistant:not(#streaming). Tests assert on this resolved root so
// they pass whether they observe the in-progress stream or the finished render
// (on slow/race-instrumented runners the #streaming element may be gone before
// a poll lands).
const streamingRenderedRootJS = `
function eitriRenderedRoot() {
  var s = document.getElementById('streaming');
  if (s) return s;
  var msgs = document.querySelectorAll('.message-assistant:not(#streaming)');
  return msgs.length ? msgs[msgs.length - 1] : null;
}
`

// streamingMarkdownTestHelper is a unified test helper for streaming markdown browser tests.
// For single-token mode (final-render tests), set SingleToken=true in opts.
// For multi-token mode (streaming tests), leave opts zero-valued.
func streamingMarkdownTestHelper(t *testing.T, markdown string, opts streamingMarkdownTestOptions, check func(ctx context.Context) bool) {
	t.Helper()

	var llmSrv *httptest.Server
	if opts.SingleToken {
		llmSrv = fakeMarkdownChatServer(t, markdown, 0, true)
	} else {
		llmSrv = fakeMarkdownChatServer(t, markdown, streamFlushWindow, false)
	}
	defer llmSrv.Close()

	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmSrv.URL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "test", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	timeout := 8 * time.Second
	if opts.Timeout > 0 {
		timeout = opts.Timeout
	}

	// Poll the assertion until it passes or the generous fallback deadline is
	// reached. Checks are content-driven: each resolves the current rendered
	// stream (see streamingRenderedRootJS) and passes once the content is
	// actually present, so readiness is observed DOM state rather than a blind
	// wall-clock wait (see issue #1121). The deadline below is only a fallback
	// guard, not the primary completion signal. Running the check on every
	// iteration (rather than gating on a terminal state first) also lets tests
	// that observe the transient RENDERING state do so while the stream is
	// still live.
	var ok bool
	pollForCondition(t, timeout, 100*time.Millisecond, func() bool {
		if check(ctx) {
			ok = true
			return true
		}
		return false
	})
	if !ok {
		t.Error("assertion never passed")
	}
}

// fakeThinkingChatServer returns an instant chat server that includes <think> tags.
func fakeThinkingChatServer(t *testing.T) *httptest.Server {
	t.Helper()
	return fakeInstantChatServer(t, "Before <think>hidden reasoning</think> After")
}

// fakeReasoningStreamChatServer streams a long sequence of reasoning_content
// deltas (no visible content), then a short final answer. Exercises the live
// sidebar thinking panel (#thinking-panel .thinking-content) via thinking_delta
// SSE events — the path that used to do a full textContent rewrite + scroll
// reflow per delta (O(n²), freezing the main thread on long reasoning).
// fakeReasoningStreamChatServer streams a sequence of reasoning_content deltas.
// perChunkDelay paces the deltas so they span multiple server-side batch flush
// intervals (and multiple SSE frames), mimicking a real streaming reasoning
// model. A perChunkDelay of 0 bursts all deltas as fast as possible.
func fakeReasoningStreamChatServer(t *testing.T, nDeltas int, perChunkDelay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":""},"finish_reason":null}]}`+"\n\n", now)
			for i := 0; i < nDeltas; i++ {
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"reasoning_content":"tok%d "},"finish_reason":null}]}`+"\n\n", now, i)
				if i%50 == 49 {
					flusher.Flush()
				}
				if perChunkDelay > 0 {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(perChunkDelay):
					}
				}
			}
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakePacedChatServer streams a sequence of content chunks with a per-chunk
// delay, so the reply arrives over a controllable multi-second window (unlike
// fakeMarkdownChatServer, which emits the reply as a single burst token).
// Useful for tests that need to observe streaming behaviour mid-run.
func fakePacedChatServer(t *testing.T, chunks []string, perChunkDelay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
		case "/v1/chat/completions":
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			for _, chunk := range chunks {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`+"\n\n", now, chunk)
				flusher.Flush()
				if perChunkDelay > 0 {
					select {
					case <-r.Context().Done():
						return
					case <-time.After(perChunkDelay):
					}
				}
			}

			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// streamingMarkdownLinkTest describes one link-rendering test case.
type streamingMarkdownLinkTest struct {
	// Name is the test case name (used for subtest naming).
	Name string
	// Markdown is the input markdown text containing the link.
	Markdown string
	// ExpectLink is true if an <a> element should be present in the rendered output.
	ExpectLink bool
	// ExpectedText is the link text that should appear in the content (for disallowed-scheme tests).
	ExpectedText string
	// ExpectedHref is the expected href attribute value (empty if not checking href).
	ExpectedHref string
	// ExpectedTarget is the expected target attribute value (empty if not checking target).
	ExpectedTarget string
	// ExpectedRel is the expected rel attribute value (empty if not checking rel).
	ExpectedRel string
}

// streamingMarkdownLinkHelper runs a single link-rendering browser test case.
// It verifies link presence/absence and optional href/target/rel attributes.
func streamingMarkdownLinkHelper(t *testing.T, tc streamingMarkdownLinkTest) {
	t.Helper()

	streamingMarkdownTestHelper(t, tc.Markdown, streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		// Resolve the current rendered stream root (#streaming while live, or
		// the final committed assistant message once the render swap is done)
		// so the assertion holds whether it lands during streaming or after.
		var res struct {
			Found   bool
			HasLink bool
			Href    string
			Target  string
			Rel     string
			Content string
		}
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(streamingRenderedRootJS+
				`(function(){
					var r = eitriRenderedRoot();
					if (!r) return {Found:false,HasLink:false,Href:'',Target:'',Rel:'',Content:''};
					var c = r.querySelector('.message-content');
					if (!c) return {Found:true,HasLink:false,Href:'',Target:'',Rel:'',Content:''};
					var a = c.querySelector('a');
					return {
						Found:true,
						HasLink:a!==null,
						Href:a?(a.getAttribute('href')||''):'',
						Target:a?(a.getAttribute('target')||''):'',
						Rel:a?(a.getAttribute('rel')||''):'',
						Content:c.textContent||''
					};
				})()`, &res),
		)
		if err != nil || !res.Found {
			return false
		}

		if tc.ExpectLink && !res.HasLink {
			return false
		}
		if !tc.ExpectLink && res.HasLink {
			return false
		}
		// For disallowed-scheme tests, wait for text content to appear.
		if tc.ExpectedText != "" && !strings.Contains(res.Content, tc.ExpectedText) {
			return false
		}

		// All checks passed within the poll loop — now do assertions.
		if tc.ExpectedHref != "" && res.Href != tc.ExpectedHref {
			t.Errorf("link href should be %q, got %q", tc.ExpectedHref, res.Href)
		}
		if tc.ExpectedTarget != "" && res.Target != tc.ExpectedTarget {
			t.Errorf("link target should be %q, got %q", tc.ExpectedTarget, res.Target)
		}
		if tc.ExpectedRel != "" && res.Rel != tc.ExpectedRel {
			t.Errorf("link rel should be %q, got %q", tc.ExpectedRel, res.Rel)
		}

		return true
	})
}
