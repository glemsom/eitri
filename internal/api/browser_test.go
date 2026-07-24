package api_test

import (
	"context"
	"encoding/json"
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

	ctx, ctxCancel := chromedp.NewContext(sharedAllocCtx)

	// Wait for the browser to be ready
	if err := chromedp.Run(ctx); err != nil {
		t.Fatalf("failed to start browser: %v", err)
	}

	return ctx, ctxCancel
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

// fakeChatServer returns an httptest.Server that acts as an OpenAI-compatible
// LLM provider for chat tests. It handles:
//   - GET /v1/models — returns a model list for config validation
//   - POST /v1/chat/completions — returns a streaming SSE chat completion
//
// Mode "ok" returns a short completion, "error" returns a 500 error.
func fakeChatServer(t *testing.T, mode string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)

		case "/v1/chat/completions":
			if mode == "error" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `{"error":{"message":"Internal error","type":"server_error"}}`)
				return
			}

			// Streaming SSE response
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "Streaming not supported", http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")

			// Initial chunk with role
			now := time.Now().Unix()
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`+"\n\n", now)
			flusher.Flush()

			// Content chunks
			for _, word := range []string{"Hello", " from", " the", " fake", " LLM"} {
				fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{"content":"%s"},"finish_reason":null}]}`+"\n\n", now, word)
				flusher.Flush()
				time.Sleep(5 * time.Millisecond)
			}

			// Final stop chunk
			fmt.Fprintf(w, `data: {"id":"chatcmpl-test","object":"chat.completion.chunk","created":%d,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n", now)
			flusher.Flush()

			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

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

// configureProvider saves runnable LLM provider config to test server via HTTP.
func configureProvider(t *testing.T, server *httptest.Server, llmURL string) {
	t.Helper()
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model"}`, llmURL))
}

// putBrowserConfig saves an arbitrary config JSON body to the test server via PUT /api/config.
func putBrowserConfig(t *testing.T, server *httptest.Server, body string) {
	t.Helper()
	req, err := http.NewRequest("PUT", server.URL+"/api/config", strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create config request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to PUT config: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(resp.Body).Decode(&errResp) == nil {
			t.Fatalf("config save failed (status %d): %s", resp.StatusCode, errResp.Error)
		}
		t.Fatalf("config save failed with status %d", resp.StatusCode)
	}
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

	deadline := time.Now().Add(timeout)
	var ok bool
	for time.Now().Before(deadline) {
		if check(ctx) {
			ok = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ok {
		t.Error("assertion never passed")
	}
}

// fakeThinkingChatServer returns an instant chat server that includes <think> tags.
func fakeThinkingChatServer(t *testing.T) *httptest.Server {
	t.Helper()
	return fakeInstantChatServer(t, "Before <think>hidden reasoning</think> After")
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

	var contentText string
	streamingMarkdownTestHelper(t, tc.Markdown, streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var hasLink, streamingExists bool
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.getElementById('streaming') !== null`, &streamingExists),
			chromedp.EvaluateAsDevTools(`document.getElementById('streaming') !== null && document.querySelector('#streaming .message-content a') !== null`, &hasLink),
		)
		if err != nil || !streamingExists {
			return false
		}

		if tc.ExpectLink && !hasLink {
			return false
		}
		if !tc.ExpectLink && hasLink {
			return false
		}

		// Extract attributes and content text
		var href, target, rel string
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`var el = document.querySelector('#streaming .message-content a'); el ? el.getAttribute('href') : ''`, &href),
			chromedp.EvaluateAsDevTools(`var el = document.querySelector('#streaming .message-content a'); el ? el.getAttribute('target') : ''`, &target),
			chromedp.EvaluateAsDevTools(`var el = document.querySelector('#streaming .message-content a'); el ? el.getAttribute('rel') : ''`, &rel),
			chromedp.EvaluateAsDevTools(`var el = document.querySelector('#streaming .message-content'); el ? el.textContent : ''`, &contentText),
		)

		// For disallowed-scheme tests, wait for text content to appear
		if tc.ExpectedText != "" && !strings.Contains(contentText, tc.ExpectedText) {
			return false
		}

		// All checks passed within the poll loop — now do assertions
		if tc.ExpectedHref != "" && href != tc.ExpectedHref {
			t.Errorf("link href should be %q, got %q", tc.ExpectedHref, href)
		}
		if tc.ExpectedTarget != "" && target != tc.ExpectedTarget {
			t.Errorf("link target should be %q, got %q", tc.ExpectedTarget, target)
		}
		if tc.ExpectedRel != "" && rel != tc.ExpectedRel {
			t.Errorf("link rel should be %q, got %q", tc.ExpectedRel, rel)
		}
		if tc.ExpectedText != "" && !strings.Contains(contentText, tc.ExpectedText) {
			t.Errorf("expected text %q in content, got %q", tc.ExpectedText, contentText)
		}

		return true
	})
}
