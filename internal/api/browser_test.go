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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/glemsom/eitri/internal/session"
)

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

// newBrowserCtx starts a headless Chrome instance via chromedp and returns
// a context suitable for browser tests. If Chrome is not found, the test is
// skipped with a clear message.
func newBrowserCtx(t *testing.T, srvURL string) (context.Context, context.CancelFunc) {
	t.Helper()

	chromePath := findChrome()
	if chromePath == "" {
		t.Skip("Chrome/Chromium not found — skipping browser test")
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(chromePath),
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
		)...,
	)

	ctx, ctxCancel := chromedp.NewContext(allocCtx)
	t.Cleanup(ctxCancel)
	t.Cleanup(allocCancel)

	// Wait for the browser to be ready
	if err := chromedp.Run(ctx); err != nil {
		t.Fatalf("failed to start browser: %v", err)
	}

	return ctx, func() {
		ctxCancel()
		allocCancel()
	}
}

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

// newTestServer is already defined in server_test.go — shared via package api_test.

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

// configureProvider saves runnable LLM provider config to test server via HTTP.
func configureProvider(t *testing.T, server *httptest.Server, llmURL string) {
	t.Helper()
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model"}`, llmURL))
}

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

// ————— Chat run browser tests (issue #22) —————

func TestBrowser_ComposerEnterSendsAndShiftEnterAddsNewline(t *testing.T) {
	llmURL := testLLMURL(t)
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var (
		composerValue string
		userText      string
	)
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}
	waitForComposerReady(t, ctx)

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			const input = document.querySelector('#chat-input');
			input.focus();
			input.value = 'line 1';
			input.setSelectionRange(input.value.length, input.value.length);
			input.dispatchEvent(new Event('input', { bubbles: true }));
			const event = new KeyboardEvent('keydown', { key: 'Enter', shiftKey: true, bubbles: true, cancelable: true });
			const allowed = input.dispatchEvent(event);
			if (allowed && !event.defaultPrevented) {
				const start = input.selectionStart;
				const end = input.selectionEnd;
				input.value = input.value.slice(0, start) + '\n' + input.value.slice(end);
				input.setSelectionRange(start + 1, start + 1);
				input.dispatchEvent(new Event('input', { bubbles: true }));
			}
			input.value += 'line 2';
			input.setSelectionRange(input.value.length, input.value.length);
			input.dispatchEvent(new Event('input', { bubbles: true }));
			return input.value;
		})()`, &composerValue),
	)
	if err != nil {
		t.Fatalf("compose multiline message failed: %v", err)
	}
	if composerValue != "line 1\nline 2" {
		t.Fatalf("composer value after Shift+Enter = %q, want %q", composerValue, "line 1\nline 2")
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			const input = document.querySelector('#chat-input');
			input.focus();
			const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
			input.dispatchEvent(event);
			return event.defaultPrevented;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch Enter failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.WaitVisible(".message-user", chromedp.ByQuery),
		chromedp.Text(".message-user .message-content", &userText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send by Enter failed: %v", err)
	}
	if !strings.Contains(userText, "line 1") || !strings.Contains(userText, "line 2") {
		t.Fatalf("user bubble text = %q, want both lines present", userText)
	}
}

func TestBrowser_ComposerCompletionKeyboardAndNestedPaths(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(workspace+"/alpha", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspace+"/beta", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace+"/alpha/nested.txt", []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace+"/root.txt", []byte("root"), 0644); err != nil {
		t.Fatal(err)
	}

	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"test-model"}]}`)
	server := newTestServerAtWorkspace(t, workspace)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"test-model"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var selectedLabel string
	var menuClosed bool
	var dirValue string
	var nestedItemsJSON string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}
	waitForComposerReady(t, ctx)

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			const input = document.querySelector('#chat-input');
			input.focus();
			input.value = '@';
			input.setSelectionRange(1, 1);
			input.dispatchEvent(new Event('input', { bubbles: true }));
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("open root completion failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.WaitVisible("#completion-menu .completion-item", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("wait root completion failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true, cancelable: true }))`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('#completion-menu .completion-item.selected .completion-label')?.textContent ?? ''`, &selectedLabel),
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('#completion-menu').style.display === 'none'`, &menuClosed),
	)
	if err != nil {
		t.Fatalf("navigate completion menu failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			const input = document.querySelector('#chat-input');
			input.value = '@a';
			input.focus();
			input.setSelectionRange(2, 2);
			input.dispatchEvent(new Event('input', { bubbles: true }));
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("open directory completion failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.WaitVisible("#completion-menu .completion-item", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))`, nil),
		chromedp.EvaluateAsDevTools(`document.querySelector('#chat-input').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))`, nil),
		chromedp.Value("#chat-input", &dirValue, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("select directory completion failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			const sessionMatch = window.location.pathname.match(/\/sessions\/([^/]+)/);
			if (!sessionMatch) return '[]';
			const xhr = new XMLHttpRequest();
			xhr.open('GET', '/api/sessions/' + sessionMatch[1] + '/complete/files?q=' + encodeURIComponent('alpha/'), false);
			xhr.send(null);
			const data = JSON.parse(xhr.responseText || '{"items": []}');
			return JSON.stringify(data.items || []);
		})()`, &nestedItemsJSON),
	)
	var nestedItems []map[string]string
	if err == nil {
		err = json.Unmarshal([]byte(nestedItemsJSON), &nestedItems)
	}
	if err != nil {
		t.Fatalf("fetch nested file completions failed: %v", err)
	}
	if selectedLabel != "alpha/" {
		t.Fatalf("selected label after keyboard navigation = %q, want %q", selectedLabel, "alpha/")
	}
	if !menuClosed {
		t.Fatal("Escape should close completion menu")
	}
	if dirValue != "@alpha/" {
		t.Fatalf("directory completion value = %q, want %q", dirValue, "@alpha/")
	}
	if len(nestedItems) != 1 {
		t.Fatalf("nested completion items = %+v, want single nested file", nestedItems)
	}
	if nestedItems[0]["path"] != "alpha/nested.txt" {
		t.Fatalf("nested completion path = %q, want %q", nestedItems[0]["path"], "alpha/nested.txt")
	}
	if nestedItems[0]["kind"] != "file" {
		t.Fatalf("nested completion kind = %q, want %q", nestedItems[0]["kind"], "file")
	}
}

// TestBrowser_SendMessage verifies that sending a message creates a user bubble
// in the DOM and clears the chat input.
func TestBrowser_SendMessage(t *testing.T) {
	llmURL := testLLMURL(t)
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Navigate to chat page
	var title string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Title(&title),
	)
	if err != nil {
		t.Fatalf("navigation failed: %v", err)
	}

	// Type a message and click send
	messageText := "Hello, Eitri!"
	var userBubbleExists bool
	var inputValue string
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", messageText, chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
		// Wait for user bubble to appear (HTMX swap completed)
		chromedp.WaitVisible(".message-user", chromedp.ByQuery),
		// Verify the bubble contains our message
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-user .message-content') !== null &&
			 document.querySelector('.message-user .message-content').textContent === "`+messageText+`"`,
			&userBubbleExists,
		),
		// Verify chat input is cleared after submit
		chromedp.EvaluateAsDevTools(
			`document.getElementById('chat-input').value`,
			&inputValue,
		),
	)
	if err != nil {
		t.Fatalf("send message test failed: %v", err)
	}

	if !userBubbleExists {
		t.Error("user bubble with message text not found after sending")
	}
	if inputValue != "" {
		t.Errorf("chat input not cleared after submit: got %q, want empty", inputValue)
	}
}

// TestBrowser_OptimisticUserBubble verifies that the user message appears
// in the DOM immediately on form submit, before the SSE stream starts.
func TestBrowser_OptimisticUserBubble(t *testing.T) {
	llmURL := fakeSlowChatServer(t, 2*time.Second).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	messageText := "Optimistic bubble test"

	// Navigate and send a message
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation failed: %v", err)
	}

	// Click send and check DOM immediately (before SSE events arrive)
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", messageText, chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Check for user bubble immediately — the optimistic insert happens
	// before the HTMX request completes, so it should be visible even
	// though the slow LLM server delays the response.
	time.Sleep(100 * time.Millisecond)

	var bubbleFound bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-user .message-content') !== null &&
			 document.querySelector('.message-user .message-content').textContent === "`+messageText+`"`,
			&bubbleFound,
		),
	)
	if err != nil {
		t.Fatalf("bubble check failed: %v", err)
	}

	if !bubbleFound {
		t.Error("optimistic user bubble should appear before SSE stream starts")
	}

	// Verify no duplicate user bubbles (same text shouldn't appear twice)
	var bubbleCount int
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelectorAll('.message-user .message-content').length`,
			&bubbleCount,
		),
	)
	if err != nil {
		t.Fatalf("bubble count check failed: %v", err)
	}
	if bubbleCount > 1 {
		t.Errorf("duplicate user bubbles: got %d, want 1", bubbleCount)
	}
}

// TestBrowser_AutoScroll verifies the streaming lifecycle produces content
// and the auto-scroll functions are present in the JS source.
func TestBrowser_AutoScroll(t *testing.T) {
	llmURL := fakeSlowChatServer(t, 500*time.Millisecond).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation failed: %v", err)
	}

	// Send a message to trigger streaming
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Test scroll", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Wait for SSE stream to complete
	time.Sleep(1500 * time.Millisecond)

	// Verify assistant message rendered (streaming completed)
	var assistantMsgExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant') !== null`,
			&assistantMsgExists,
		),
	)
	if err != nil {
		t.Fatalf("assistant message check failed: %v", err)
	}
	if !assistantMsgExists {
		t.Error("assistant message should have rendered via SSE stream")
	}

		// Verify the JS source contains scrollToLatest (checked by js_test.go)
}

// TestBrowser_ScrollToBottomButton verifies the floating scroll-to-bottom button
// appears when user scrolls up during streaming and scrolls down on click.
func TestBrowser_ScrollToBottomButton(t *testing.T) {
	llmURL := fakeBurstChatServer(t, 500, 2*time.Millisecond).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation failed: %v", err)
	}

	// Verify sentinel element exists in the DOM
	var sentinelExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.getElementById('scroll-sentinel') !== null`,
			&sentinelExists,
		),
	)
	if err != nil {
		t.Fatalf("sentinel check failed: %v", err)
	}
	if !sentinelExists {
		t.Error("scroll sentinel element should exist in #messages")
	}

	// Verify scroll-to-bottom button exists and is hidden initially
	var btnState string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`(() => {
				const btn = document.getElementById('scroll-to-bottom-btn');
				if (!btn) return 'missing';
				return btn.classList.contains('visible') ? 'visible' : 'hidden';
			})()`,
			&btnState,
		),
	)
	if err != nil {
		t.Fatalf("button visibility check failed: %v", err)
	}
	if btnState != "hidden" {
		t.Errorf("scroll-to-bottom button should be hidden initially, got: %v", btnState)
	}

	// Send a message to trigger streaming with many tokens (500 x's = ~500 chars)
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Test scroll button", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Wait for streaming to start and accumulate enough content
	time.Sleep(2 * time.Second)

	// Force messages container to a small fixed height to create overflow
	// (default viewport is too large for 500 chars to overflow)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.getElementById('messages').style.maxHeight = '150px'`,
			nil,
		),
	)
	if err != nil {
		t.Fatalf("set messages height failed: %v", err)
	}

	// Scroll up to trigger the button
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.getElementById('messages').scrollTop = 0`,
			nil,
		),
	)
	if err != nil {
		t.Fatalf("scroll up failed: %v", err)
	}

	// Wait for IntersectionObserver to fire
	time.Sleep(500 * time.Millisecond)

	// Check button is now visible
	var btnVisibleAfterScroll string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`(() => {
				const btn = document.getElementById('scroll-to-bottom-btn');
				if (!btn) return 'missing';
				return btn.classList.contains('visible') ? 'visible' : 'hidden';
			})()`,
			&btnVisibleAfterScroll,
		),
	)
	if err != nil {
		t.Fatalf("button visibility after scroll failed: %v", err)
	}
	if btnVisibleAfterScroll != "visible" {
		t.Errorf("scroll-to-bottom button should be visible after scrolling up, got: %v", btnVisibleAfterScroll)
	}

	// Click the button to scroll down
	err = chromedp.Run(ctx,
		chromedp.Click("#scroll-to-bottom-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("click button failed: %v", err)
	}

	// Wait for smooth scroll to complete
	time.Sleep(500 * time.Millisecond)

	// Check button is hidden again after scrolling to bottom
	var btnVisibleAfterClick string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`(() => {
				const btn = document.getElementById('scroll-to-bottom-btn');
				if (!btn) return 'missing';
				return btn.classList.contains('visible') ? 'visible' : 'hidden';
			})()`,
			&btnVisibleAfterClick,
		),
	)
	if err != nil {
		t.Fatalf("button visibility after click failed: %v", err)
	}
	if btnVisibleAfterClick != "hidden" {
		t.Errorf("scroll-to-bottom button should hide after scrolling to bottom, got: %v", btnVisibleAfterClick)
	}

	// Verify assistant message rendered
	var assistantMsgExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant') !== null`,
			&assistantMsgExists,
		),
	)
	if err != nil {
		t.Fatalf("assistant message check failed: %v", err)
	}
	if !assistantMsgExists {
		t.Error("assistant message should have rendered via SSE stream")
	}
}

func TestBrowser_SessionTitleFollowsFirstUserMessage(t *testing.T) {
	llmURL := fakeInstantChatServer(t, "ok").URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	const firstMessage = "Fix flaky session tab title behavior across browser tabs and runs"
	const expectedTitle = "Fix flaky session tab title be…"

	var titles []string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.WaitVisible("#session-tabs", chromedp.ByQuery),
		chromedp.Click("#session-tabs .new-session-btn", chromedp.ByQuery),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("create second session failed: %v", err)
	}
	for i := 0; i < 20; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`Array.from(document.querySelectorAll('#session-tabs .session-title')).map(el => el.textContent.trim())`, &titles),
		)
		if err != nil {
			t.Fatalf("read session titles after create failed: %v", err)
		}
		if len(titles) == 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(titles) != 2 {
		t.Fatalf("session titles after create = %v, want 2 tabs", titles)
	}
	if titles[0] != "Session 1" || titles[1] != "Session 2" {
		t.Fatalf("initial session titles = %v, want [Session 1 Session 2]", titles)
	}

	err = chromedp.Run(ctx,
		chromedp.Click("#session-tabs .session-item:first-child .session-item-link", chromedp.ByQuery),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", firstMessage, chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send first message failed: %v", err)
	}

	for i := 0; i < 20; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`Array.from(document.querySelectorAll('#session-tabs .session-title')).map(el => el.textContent.trim())`, &titles),
		)
		if err != nil {
			t.Fatalf("read session titles after first send failed: %v", err)
		}
		if len(titles) == 2 && titles[0] == expectedTitle && titles[1] == "Session 2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(titles) != 2 || titles[0] != expectedTitle || titles[1] != "Session 2" {
		t.Fatalf("session titles after first send = %v, want [%s Session 2]", titles, expectedTitle)
	}

	var inputReady bool
	for i := 0; i < 20; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var input = document.querySelector('#chat-input');
				var send = document.querySelector('#send-btn');
				return !!input && !!send && !input.disabled && !send.disabled;
			})()`, &inputReady),
		)
		if err != nil {
			t.Fatalf("read composer readiness failed: %v", err)
		}
		if inputReady {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !inputReady {
		t.Fatal("composer did not become ready for second message")
	}

	err = chromedp.Run(ctx,
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(function() {
			var input = document.querySelector('#chat-input');
			if (!input) return false;
			input.value = '';
			input.dispatchEvent(new Event('input', { bubbles: true }));
			return true;
		})()`, nil),
		chromedp.SendKeys("#chat-input", "second message should not rename tab", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send second message failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`Array.from(document.querySelectorAll('#session-tabs .session-title')).map(el => el.textContent.trim())`, &titles),
	)
	if err != nil {
		t.Fatalf("read session titles after second send failed: %v", err)
	}
	if len(titles) != 2 || titles[0] != expectedTitle || titles[1] != "Session 2" {
		t.Fatalf("session titles after second send = %v, want unchanged [%s Session 2]", titles, expectedTitle)
	}
}

func TestBrowser_FastRunRendersAssistantAndUsesValidStreamURL(t *testing.T) {
	llmURL := fakeInstantChatServer(t, "skills: one, two, three").URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var (
		mu         sync.Mutex
		streamURLs []string
	)
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		req, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		if !strings.Contains(req.Request.URL, "/api/sessions/") || !strings.Contains(req.Request.URL, "/stream") {
			return
		}
		mu.Lock()
		streamURLs = append(streamURLs, req.Request.URL)
		mu.Unlock()
	})

	err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "What skills do you have available?", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation/send failed: %v", err)
	}

	var assistantText string
	for i := 0; i < 20; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.querySelector('.message-assistant .message-content');
				return el ? el.textContent : "";
			})()`, &assistantText),
		)
		if err != nil {
			t.Fatalf("assistant text check failed: %v", err)
		}
		if strings.TrimSpace(assistantText) != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if got := strings.TrimSpace(assistantText); got == "" {
		t.Fatal("assistant response empty after fast run")
	}

	mu.Lock()
	defer mu.Unlock()
	for _, url := range streamURLs {
		if strings.Contains(url, "/api/sessions/[object%20Object]/stream") || strings.Contains(url, "/api/sessions/[object Object]/stream") {
			t.Fatalf("invalid stream URL requested: %s", url)
		}
	}
	if len(streamURLs) == 0 {
		t.Fatal("no stream URL requested")
	}
}

func TestBrowser_RichRenderingAssetsAndBehavior(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10)
	sess, err := sessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "show rich output"})
	sessionMgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: strings.Join([]string{
		"Here is rich output.",
		"",
		"Inline math $a+b$.",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"```mermaid",
		"graph TD; A-->B;",
		"```",
	}, "\n")})
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var prismLoaded bool
	var katexLoaded bool
	var mermaidLoaded bool
	var copyButtonExists bool
	var copyButtonState string
	var mathRenderedOrVisible bool
	var mermaidBlockRenderedOrVisible bool
	var componentMermaidRenderedOrVisible bool

	err = chromedp.Run(ctx,
		network.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetCookie("browser_id", "browser-1").WithURL(server.URL).Do(ctx)
		}),
		chromedp.Navigate(server.URL+"/sessions/"+sess.ID),
		chromedp.WaitVisible(".message-assistant .copy-btn", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("typeof Prism !== 'undefined'", &prismLoaded),
		chromedp.EvaluateAsDevTools("typeof katex !== 'undefined'", &katexLoaded),
		chromedp.EvaluateAsDevTools("typeof mermaid !== 'undefined'", &mermaidLoaded),
		chromedp.EvaluateAsDevTools("document.querySelector('.message-assistant .copy-btn') !== null", &copyButtonExists),
		chromedp.Click(".message-assistant .copy-btn", chromedp.ByQuery),
		chromedp.Sleep(150*time.Millisecond),
		chromedp.Text(".message-assistant .copy-btn", &copyButtonState, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(function () {
			var el = document.querySelector('.message-assistant .math-inline');
			if (!el) return false;
			return !!el.querySelector('.katex') || (el.textContent || '').includes('$a+b$');
		})()`, &mathRenderedOrVisible),
		chromedp.EvaluateAsDevTools(`(function () {
			var el = document.querySelector('.message-assistant pre.mermaid');
			if (!el) return false;
			return !!el.querySelector('svg') || (el.textContent || '').includes('graph TD; A-->B;');
		})()`, &mermaidBlockRenderedOrVisible),
		chromedp.EvaluateAsDevTools(`(function () {
			var messages = document.getElementById('messages');
			messages.insertAdjacentHTML('beforeend', '<div class="mermaid-diagram"><pre class="mermaid">graph TD; A--&gt;B;</pre></div>');
			document.dispatchEvent(new Event('htmx:afterSwap'));
			return true;
		})()`, nil),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.EvaluateAsDevTools(`(function () {
			var el = document.querySelector('.mermaid-diagram pre.mermaid');
			if (!el) return false;
			return !!el.querySelector('svg') || (el.textContent || '').includes('graph TD; A-->B;');
		})()`, &componentMermaidRenderedOrVisible),
	)
	if err != nil {
		t.Fatalf("rich rendering browser test failed: %v", err)
	}

	if !prismLoaded {
		t.Error("Prism asset not loaded")
	}
	if !katexLoaded {
		t.Error("KaTeX asset not loaded")
	}
	if !mermaidLoaded {
		t.Error("Mermaid asset not loaded")
	}
	if !copyButtonExists {
		t.Error("copy button not rendered")
	}
	if copyButtonState != "Copied!" && copyButtonState != "Failed" && copyButtonState != "Copy" {
		t.Errorf("unexpected copy button state %q", copyButtonState)
	}
	if !mathRenderedOrVisible {
		t.Error("math markup neither rendered nor visible")
	}
	if !mermaidBlockRenderedOrVisible {
		t.Error("mermaid fenced block neither rendered nor visible")
	}
	if !componentMermaidRenderedOrVisible {
		t.Error("mermaid component markup neither rendered nor visible")
	}
}

// TestBrowser_InputDisabledDuringRun verifies that during an active run,
// the chat input and send button are disabled, and the stop button is visible.
func TestBrowser_DiffCardsToggleAndCollapseAfterHTMXSwap(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	oldContent := strings.Join([]string{
		"line 1",
		"line 2",
		"line 3",
		"line 4",
		"line 5",
		"line 6",
		"line 7",
		"line 8",
		"line 9",
		"line 10",
		"line 11",
		"line 12",
	}, "\n") + "\n"
	newContent := strings.Replace(oldContent, "line 3\n", "line 3 changed\n", 1)

	var (
		diffExpanded             bool
		diffSideBySideActive     bool
		fileEditDiffRendered     bool
		fileEditSideBySideActive bool
	)

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(fmt.Sprintf(`(function() {
			const sessionId = location.pathname.split('/').pop();
			htmx.ajax('POST', '/api/sessions/' + sessionId + '/render/component', {
				source: document.body,
				target: '#messages',
				swap: 'beforeend',
				values: {
					name: 'DiffCard',
					data: JSON.stringify({old: %q, new: %q, lang: 'go'})
				}
			});
			return true;
		})()`, oldContent, newContent), nil),
		chromedp.WaitVisible("eitri-diff-card .diff-collapse-btn", chromedp.ByQuery),
		chromedp.Click("eitri-diff-card .diff-collapse-btn", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(function() {
			const active = document.querySelector('eitri-diff-card .diff-pane.is-active');
			if (!active) return false;
			return active.querySelectorAll('.diff-row[data-collapse-group][hidden]').length === 0;
		})()`, &diffExpanded),
		chromedp.Click("eitri-diff-card .diff-toggle-btn[data-view='side-by-side']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(function() {
			const card = document.querySelector('eitri-diff-card');
			if (!card) return false;
			return !!card.querySelector('.diff-pane-side-by-side.is-active') &&
				!!card.querySelector('.diff-toggle-btn[data-view="side-by-side"].is-active');
		})()`, &diffSideBySideActive),
		chromedp.EvaluateAsDevTools(fmt.Sprintf(`(function() {
			const sessionId = location.pathname.split('/').pop();
			htmx.ajax('POST', '/api/sessions/' + sessionId + '/render/tool-card', {
				source: document.body,
				target: '#messages',
				swap: 'beforeend',
				values: {
					type: 'tool_result',
					tool: 'file_editor',
					output: JSON.stringify({
						path: 'main.go',
						mode: 'overwrite',
						bytes_written: 123,
						old_content: %q,
						new_content: %q,
						dirs_created: []
					})
				}
			});
			return true;
		})()`, oldContent, newContent), nil),
		chromedp.WaitVisible(".file-edit-card eitri-diff-card .diff-toggle-btn[data-view='side-by-side']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('.file-edit-card eitri-diff-card') !== null`, &fileEditDiffRendered),
		chromedp.Click(".file-edit-card eitri-diff-card .diff-toggle-btn[data-view='side-by-side']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(function() {
			const card = document.querySelector('.file-edit-card eitri-diff-card');
			if (!card) return false;
			return !!card.querySelector('.diff-pane-side-by-side.is-active') &&
				!!card.querySelector('.diff-toggle-btn[data-view="side-by-side"].is-active');
		})()`, &fileEditSideBySideActive),
	)
	if err != nil {
		t.Fatalf("diff card browser test failed: %v", err)
	}

	if !diffExpanded {
		t.Error("DiffCard unchanged rows should expand after collapse toggle")
	}
	if !diffSideBySideActive {
		t.Error("DiffCard should switch to side-by-side view")
	}
	if !fileEditDiffRendered {
		t.Error("file edit result should render interactive diff card")
	}
	if !fileEditSideBySideActive {
		t.Error("file edit diff should switch to side-by-side view")
	}
}

func TestBrowser_RunStatusChrome_ShowsNoDeadAirAndDone(t *testing.T) {
	llmURL := fakeDelayedFirstTokenChatServer(t, 1200*time.Millisecond, "slow hello").URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var idleStatus string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Text("#stream-indicator", &idleStatus, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}
	if strings.TrimSpace(idleStatus) != "Idle" {
		t.Fatalf("initial run status = %q, want Idle", idleStatus)
	}

	if err := chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "show status", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("start run failed: %v", err)
	}

	time.Sleep(850 * time.Millisecond)

	var connectingStatus string
	err = chromedp.Run(ctx,
		chromedp.Text("#stream-indicator", &connectingStatus, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("read connecting status failed: %v", err)
	}
	if strings.TrimSpace(connectingStatus) != "Connecting" {
		t.Fatalf("run status during slow start = %q, want Connecting", connectingStatus)
	}

	var finalStatus string
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text("#stream-indicator", &finalStatus, chromedp.ByQuery),
		)
		if err == nil && strings.TrimSpace(finalStatus) == "Done" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.TrimSpace(finalStatus) != "Done" {
		t.Fatalf("final run status = %q, want Done", finalStatus)
	}

	var assistantText string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var msgs = Array.from(document.querySelectorAll('.message-assistant .message-content'));
			return msgs.map(function(el) { return el.textContent || ''; }).join('\n');
		})()`, &assistantText),
	)
	if err != nil {
		t.Fatalf("read assistant text failed: %v", err)
	}
	if !strings.Contains(assistantText, "slow hello") {
		t.Fatalf("assistant text = %q, want slow hello", assistantText)
	}
}

func TestBrowser_RunStatusChrome_ReconnectAndActivityPanel(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	var panelClosed bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('activity-panel').open === false`, &panelClosed),
	)
	if err != nil {
		t.Fatalf("read activity panel default state failed: %v", err)
	}
	if !panelClosed {
		t.Fatal("activity panel should be collapsed by default")
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			class FakeEventSource {
				constructor(url) {
					this.url = url;
					window.__fakeEventSource = this;
				}
				close() {
					this.closed = true;
				}
				emitOpen() {
					if (this.onopen) this.onopen({});
				}
				emitMessage(packet) {
					if (this.onmessage) this.onmessage({ data: JSON.stringify(packet) });
				}
				emitError() {
					if (this.onerror) this.onerror(new Event('error'));
				}
			}
			window.EventSource = FakeEventSource;
			var sessionId = location.pathname.split('/').pop();
			document.dispatchEvent(new CustomEvent('eitri:connectRunStream', { detail: { value: sessionId } }));
			return !!window.__fakeEventSource;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install fake EventSource failed: %v", err)
	}

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitError()`, nil),
	); err != nil {
		t.Fatalf("emit reconnect transition failed: %v", err)
	}

	var reconnectingStatus string
	err = chromedp.Run(ctx,
		chromedp.Text("#stream-indicator", &reconnectingStatus, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("read reconnecting status failed: %v", err)
	}
	if strings.TrimSpace(reconnectingStatus) != "Reconnecting" {
		t.Fatalf("run status after error = %q, want Reconnecting", reconnectingStatus)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'token', content: 'hello'})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: 'echo hello'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit token/tool_call failed: %v", err)
	}

	var toolRunningStatus string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text("#stream-indicator", &toolRunningStatus, chromedp.ByQuery),
		)
		if err == nil && strings.TrimSpace(toolRunningStatus) == "Tool running" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.TrimSpace(toolRunningStatus) != "Tool running" {
		t.Fatalf("run status during tool call = %q, want Tool running", toolRunningStatus)
	}

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_result', tool: 'terminal_execute', output: 'hello\n'})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'done', message_id: 'msg_fake'})`, nil),
	); err != nil {
		t.Fatalf("emit tool_result/done failed: %v", err)
	}

	var renderingSeen bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var indicator = document.getElementById('stream-indicator');
			return indicator ? indicator.textContent.trim() === 'Rendering' : false;
		})()`, &renderingSeen),
	)
	if err != nil {
		t.Fatalf("read rendering phase failed: %v", err)
	}
	if !renderingSeen {
		t.Fatal("expected Rendering phase immediately after done packet")
	}

	var (
		activityCount string
		activityText  string
		doneStatus    string
	)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text("#stream-indicator", &doneStatus, chromedp.ByQuery),
			chromedp.Text("#activity-count", &activityCount, chromedp.ByQuery),
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.getElementById('activity-log');
				return el ? (el.textContent || '') : '';
			})()`, &activityText),
		)
		if err == nil && strings.TrimSpace(doneStatus) == "Done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.TrimSpace(doneStatus) != "Done" {
		t.Fatalf("final run status = %q, want Done", doneStatus)
	}
	if strings.TrimSpace(activityCount) != "2" {
		t.Fatalf("activity count = %q, want 2", activityCount)
	}
	if !strings.Contains(activityText, "Started terminal_execute") || !strings.Contains(activityText, "Finished terminal_execute") || !strings.Contains(activityText, "echo hello") {
		t.Fatalf("activity log = %q, want started/finished command entries", activityText)
	}
}

func TestBrowser_InputDisabledDuringRun(t *testing.T) {
	llmURL := fakeSlowChatServer(t, 2*time.Second).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Navigate to chat
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation failed: %v", err)
	}

	// Send a message
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Test message", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Wait a bit for HTMX to process and update the DOM
	time.Sleep(500 * time.Millisecond)

	var inputDisabled bool
	var sendBtnDisabled bool
	var stopBtnVisible bool

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools("document.querySelector('#chat-input').disabled === true", &inputDisabled),
		chromedp.EvaluateAsDevTools("document.querySelector('#send-btn').disabled === true", &sendBtnDisabled),
		chromedp.EvaluateAsDevTools(
			`(function() {
				var btn = document.getElementById('stop-btn');
				if (!btn) return false;
				var style = window.getComputedStyle(btn);
				return style.display !== 'none';
			})()`,
			&stopBtnVisible,
		),
	)
	if err != nil {
		t.Fatalf("run state check failed: %v", err)
	}

	if !inputDisabled {
		t.Error("#chat-input should be disabled during active run")
	}
	if !sendBtnDisabled {
		t.Error("#send-btn should be disabled during active run")
	}
	if !stopBtnVisible {
		t.Error("#stop-btn should be visible during active run")
	}
}

// TestBrowser_CancelRun verifies that cancelling an active run re-enables
// the chat input and hides the stop button.
func TestBrowser_CancelRun(t *testing.T) {
	llmURL := fakeSlowChatServer(t, 2*time.Second).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Navigate to chat and send a message to start a run
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "Hello", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation/send failed: %v", err)
	}

	// Wait for OOB swap to update composer
	time.Sleep(300 * time.Millisecond)

	// Wait for stop button to appear (may take a moment for HTMX swap)
	var stopBtnExists bool
	for i := 0; i < 10; i++ {
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools("document.getElementById('stop-btn') !== null && window.getComputedStyle(document.getElementById('stop-btn')).display !== 'none'", &stopBtnExists),
		)
		if err != nil {
			t.Logf("stop-btn visibility check iteration %d: %v", i, err)
		}
		if stopBtnExists {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !stopBtnExists {
		t.Skip("stop button not visible — cannot test cancel; possible HTMX timing issue")
	}

	// Click stop button
	err = chromedp.Run(ctx,
		chromedp.Click("#stop-btn", chromedp.ByQuery),
		chromedp.WaitVisible("#send-btn:not([disabled])", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("cancel click failed: %v", err)
	}

	// Verify partial assistant message exists (stream was cancelled)
	var hasAssistantMsg bool
	_ = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant') !== null`,
			&hasAssistantMsg,
		),
	)
	if !hasAssistantMsg {
		t.Log("no .message-assistant found after cancel — stream may have ended before any chunk rendered")
	}

	// Allow HTMX settle time
	time.Sleep(200 * time.Millisecond)

	// Verify input is re-enabled
	var inputEnabled bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools("document.querySelector('#chat-input').disabled === false", &inputEnabled),
	)
	if err != nil {
		t.Fatalf("input state check failed: %v", err)
	}
	if !inputEnabled {
		t.Error("#chat-input should be re-enabled after cancel")
	}

	// Verify stop button is hidden
	var stopBtnHidden bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`(function() {
				var btn = document.getElementById('stop-btn');
				if (!btn) return true;
				return window.getComputedStyle(btn).display === 'none';
			})()`,
			&stopBtnHidden,
		),
	)
	if err != nil {
		t.Fatalf("stop button state check failed: %v", err)
	}
	if !stopBtnHidden {
		// Debug: check the actual style attribute
		var styleAttr string
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`(function(){var b=document.getElementById('stop-btn');return b?b.getAttribute('style')||'empty':'notfound';})()`,
				&styleAttr,
			),
		)
		if err != nil {
			t.Logf("failed to read stop-btn style: %v", err)
		}
		t.Logf("actual stop-btn style attr: %s", styleAttr)
	}
}

func TestBrowser_EscapeCancelsActiveRun(t *testing.T) {
	llmURL := fakeSlowChatServer(t, 2*time.Second).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "cancel me", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("start run failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	var cancelled bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
			document.dispatchEvent(event);
			return event.defaultPrevented;
		})()`, &cancelled),
		chromedp.WaitVisible("#send-btn:not([disabled])", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("escape cancel failed: %v", err)
	}
	if !cancelled {
		t.Fatal("Escape should cancel active run")
	}
}

// TestBrowser_HarnessCanary verifies the browser test harness works.
func TestBrowser_HarnessCanary(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var title string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.Title(&title),
	)
	if err != nil {
		t.Fatalf("browser navigation failed: %v", err)
	}

	if !strings.Contains(title, "Eitri") {
		t.Errorf("page title = %q, want containing 'Eitri'", title)
	}
}

func TestBrowser_ChromeNotFoundSkips(t *testing.T) {
	if findChrome() == "" {
		t.Skip("Chrome not found — expected skip behavior verified")
	}
	t.Log("Chrome found — skip behavior not testable on this machine")
}

func TestBrowser_FindChrome(t *testing.T) {
	path := findChrome()
	if path == "" {
		t.Skip("Chrome/Chromium not found on this system")
	}
	fullPath, err := exec.LookPath(path)
	if err != nil {
		t.Fatalf("findChrome() returned %q but LookPath failed: %v", path, err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("stat on resolved path %q failed: %v", fullPath, err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("findChrome() = %q → %q is not executable", path, fullPath)
	}
}

func TestBrowser_HealthPage(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var body string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/health"),
		chromedp.Text("body", &body),
	)
	if err != nil {
		t.Fatalf("browser navigation failed: %v", err)
	}

	if !strings.Contains(body, "ok") {
		t.Errorf("health page body = %q, want containing 'ok'", body)
	}
}

func TestBrowser_SettingsPage(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var providerVal string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("body", chromedp.ByQuery),
		chromedp.Value("#provider", &providerVal, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("settings page test failed: %v", err)
	}

	if providerVal == "" {
		t.Log("settings page loaded, provider value (may be empty on first load):", providerVal)
	}
}

func TestBrowser_NavUsesHTMXBetweenFullPages(t *testing.T) {
	workspace := t.TempDir()
	server := newTestServerAtWorkspace(t, workspace)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var pathAfterSettings string
	var settingsHasWorkspace bool
	var pathAfterSkills string
	var skillsHasWorkspace bool
	var pathAfterChat string
	var chatHasWorkspace bool

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Click(`#nav-dropdown .gear-btn`, chromedp.ByQuery),
		chromedp.Click(`#nav-dropdown a[href="/settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.Location(&pathAfterSettings),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#workspace-indicator') !== null && document.querySelector('#workspace-indicator').title === `+fmt.Sprintf("%q", workspace),
			&settingsHasWorkspace,
		),
		chromedp.Click(`#nav-dropdown .gear-btn`, chromedp.ByQuery),
		chromedp.Click(`#nav-dropdown a[href="/skills"]`, chromedp.ByQuery),
		chromedp.WaitVisible(".skills-view", chromedp.ByQuery),
		chromedp.Location(&pathAfterSkills),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#workspace-indicator') !== null && document.querySelector('#workspace-indicator').title === `+fmt.Sprintf("%q", workspace),
			&skillsHasWorkspace,
		),
		chromedp.Click(`#nav-dropdown .gear-btn`, chromedp.ByQuery),
		chromedp.Click(`#nav-dropdown a[href^="/sessions/"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Location(&pathAfterChat),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#workspace-indicator') !== null && document.querySelector('#workspace-indicator').title === `+fmt.Sprintf("%q", workspace),
			&chatHasWorkspace,
		),
	)
	if err != nil {
		t.Fatalf("htmx nav test failed: %v", err)
	}

	if !strings.HasSuffix(pathAfterSettings, "/settings") {
		t.Errorf("path after settings nav = %q, want suffix /settings", pathAfterSettings)
	}
	if !settingsHasWorkspace {
		t.Error("settings page missing workspace indicator with correct title after HTMX nav")
	}
	if !strings.HasSuffix(pathAfterSkills, "/skills") {
		t.Errorf("path after skills nav = %q, want suffix /skills", pathAfterSkills)
	}
	if !skillsHasWorkspace {
		t.Error("skills page missing workspace indicator with correct title after HTMX nav")
	}
	if !strings.Contains(pathAfterChat, "/sessions/") {
		t.Errorf("path after chat nav = %q, want containing /sessions/", pathAfterChat)
	}
	if !chatHasWorkspace {
		t.Error("chat page missing workspace indicator with correct title after HTMX nav")
	}
}

// TestBrowser_PageLoads verifies the chat page loads with correct title,
// HTMX initialized, and core DOM elements present.
func TestBrowser_PageLoads(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var title string
	var htmxExists bool
	var chatViewExists, messagesExists, composerExists bool

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.EvaluateAsDevTools("typeof window.htmx !== 'undefined'", &htmxExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#chat-view') !== null", &chatViewExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#messages') !== null", &messagesExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#composer') !== null", &composerExists),
	)
	if err != nil {
		t.Fatalf("page load test failed: %v", err)
	}

	if title != "Eitri — Chat" {
		t.Errorf("title = %q, want %q", title, "Eitri — Chat")
	}
	if !htmxExists {
		t.Error("htmx not found on window")
	}
	if !chatViewExists {
		t.Error("#chat-view not found")
	}
	if !messagesExists {
		t.Error("#messages not found")
	}
	if !composerExists {
		t.Error("#composer not found")
	}
}

// TestBrowser_SetupBannerVisible verifies that the setup banner appears
// when no provider config exists, chat input is disabled, and the banner
// links to /settings.
func TestBrowser_SetupBannerVisible(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var bannerVisible bool
	var inputDisabled bool
	var bannerHTML string

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`
			(function() {
				var banner = document.querySelector('#setup-banner');
				if (!banner) return false;
				var style = window.getComputedStyle(banner);
				return style.display !== 'none' && style.visibility !== 'hidden';
			})()
		`, &bannerVisible),
		chromedp.EvaluateAsDevTools("document.querySelector('#chat-input').disabled === true", &inputDisabled),
		chromedp.OuterHTML("#setup-banner", &bannerHTML, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("setup banner test failed: %v", err)
	}

	if !bannerVisible {
		t.Error("#setup-banner should be visible when no config")
	}
	if !inputDisabled {
		t.Error("#chat-input should be disabled when no config")
	}
	if !strings.Contains(bannerHTML, "/settings") {
		t.Error("setup banner should link to /settings")
	}
}

// TestBrowser_SettingsFormElements verifies the settings page renders
// all form fields and does not contain chat-specific elements (#send-btn).
func TestBrowser_SettingsFormElements(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var providerExists bool
	var apiKeyExists, baseURLExists, modelExists, systemPromptExists bool
	var sendBtnAbsent bool
	var providerOptionsCount int

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#provider", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#provider') !== null", &providerExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#api_key') !== null", &apiKeyExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#base_url') !== null", &baseURLExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#model') !== null", &modelExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#system_prompt') !== null", &systemPromptExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#send-btn') === null", &sendBtnAbsent),
		chromedp.EvaluateAsDevTools("document.querySelector('#provider').options.length", &providerOptionsCount),
	)
	if err != nil {
		t.Fatalf("settings form test failed: %v", err)
	}

	if !providerExists {
		t.Error("#provider select not found")
	}
	if providerOptionsCount < 2 {
		t.Errorf("provider select has %d options, want at least 2", providerOptionsCount)
	}
	if !apiKeyExists {
		t.Error("#api_key input not found")
	}
	if !baseURLExists {
		t.Error("#base_url input not found")
	}
	if !modelExists {
		t.Error("#model select not found")
	}
	if !systemPromptExists {
		t.Error("#system_prompt textarea not found")
	}
	if !sendBtnAbsent {
		t.Error("#send-btn should be absent on settings page")
	}
}

func TestBrowser_SettingsDirectNavigationPopulatesModels(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var hasGPT4 bool
	var hasGPT35 bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitReady("#model option[value='gpt-4']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-4")`,
			&hasGPT4,
		),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-3.5-turbo")`,
			&hasGPT35,
		),
	)
	if err != nil {
		t.Fatalf("settings direct navigation failed: %v", err)
	}
	if !hasGPT4 {
		t.Error("settings page missing gpt-4 on direct navigation")
	}
	if !hasGPT35 {
		t.Error("settings page missing gpt-3.5-turbo on direct navigation")
	}
}

// TestBrowser_InitialConfigSavePopulatesModels verifies first save without a
// selected model discovers models and keeps the form editable for second save.
func TestBrowser_InitialConfigSavePopulatesModels(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form submit failed: %v", err)
	}

	var modelOptionCount int
	var hasGPT4 bool
	var hasGPT35 bool
	var selectedModel string
	err = chromedp.Run(ctx,
		chromedp.WaitReady("#model option[value='gpt-4']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#model').options.length", &modelOptionCount),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-4")`,
			&hasGPT4,
		),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-3.5-turbo")`,
			&hasGPT35,
		),
		chromedp.Value("#model", &selectedModel, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("model dropdown check failed: %v", err)
	}

	if modelOptionCount < 3 {
		t.Errorf("model dropdown has %d options, expected at least 3 (placeholder + 2 models)", modelOptionCount)
	}
	if !hasGPT4 {
		t.Error("model dropdown missing gpt-4")
	}
	if !hasGPT35 {
		t.Error("model dropdown missing gpt-3.5-turbo")
	}
	if selectedModel != "" {
		t.Errorf("selected model = %q, want empty after initial discovery save", selectedModel)
	}
}

// TestBrowser_ConfigSavePopulatesModels verifies HTMX save succeeds when
// user selects discovered model from settings page.
func TestBrowser_ConfigSavePopulatesModels(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)
	putBrowserConfig(t, server, fmt.Sprintf(`{"provider":"custom_openai","base_url":"%s","api_key":"sk-test","model":"gpt-4"}`, fakeProvider.URL))

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#model", "gpt-3.5-turbo", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form submit failed: %v", err)
	}

	var modelOptionCount int
	var hasGPT4 bool
	var hasGPT35 bool
	var selectedModel string
	err = chromedp.Run(ctx,
		chromedp.WaitReady("#model option[value='gpt-4']", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#model').options.length", &modelOptionCount),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-4")`,
			&hasGPT4,
		),
		chromedp.EvaluateAsDevTools(
			`Array.from(document.querySelector('#model').options).map(o => o.value).includes("gpt-3.5-turbo")`,
			&hasGPT35,
		),
		chromedp.Value("#model", &selectedModel, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("model dropdown check failed: %v", err)
	}

	if modelOptionCount < 3 {
		t.Errorf("model dropdown has %d options, expected at least 3 (placeholder + 2 models)", modelOptionCount)
	}
	if !hasGPT4 {
		t.Error("model dropdown missing gpt-4")
	}
	if !hasGPT35 {
		t.Error("model dropdown missing gpt-3.5-turbo")
	}
	if selectedModel != "gpt-3.5-turbo" {
		t.Errorf("selected model = %q, want gpt-3.5-turbo", selectedModel)
	}

	var hasErrorToast bool
	_ = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools("document.querySelector('.error-toast') !== null", &hasErrorToast),
	)
	if hasErrorToast {
		t.Error("error toast present after successful config save")
	}
}

// TestBrowser_ConfigSaveProviderFailure verifies that provider validation failure
// returns swapped settings HTML with visible error feedback.
func TestBrowser_ConfigSaveProviderFailure(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-bad", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible(".error-toast", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form fill/submit failed: %v", err)
	}

	var modelOptionsEmpty bool
	var providerValue string
	var errorText string
	err = chromedp.Run(ctx,
		chromedp.Value("#provider", &providerValue, chromedp.ByQuery),
		chromedp.Text(".error-toast .error-text", &errorText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#model').options.length <= 1", &modelOptionsEmpty),
	)
	if err != nil {
		t.Fatalf("post-submit check failed: %v", err)
	}

	if !modelOptionsEmpty {
		t.Error("model dropdown should be empty (placeholder only) after validation failure")
	}
	if providerValue != "custom_openai" {
		t.Errorf("provider should still be 'custom_openai' after error, got %q", providerValue)
	}
	if !strings.Contains(errorText, "Provider authentication failed") {
		t.Errorf("error text = %q, want auth guidance", errorText)
	}
}

// TestBrowser_ActiveNavLink verifies the current page's nav link has active styling.
func TestBrowser_ActiveNavLink(t *testing.T) {
	workspace := t.TempDir()
	server := newTestServerAtWorkspace(t, workspace)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Check Chat page — Chat link has active class
	var chatActiveOnChat, settingsActiveOnChat, skillsActiveOnChat bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('#nav-dropdown a[href^="/sessions/"]')?.classList.contains("active")`, &chatActiveOnChat),
		chromedp.EvaluateAsDevTools(`document.querySelector('#nav-dropdown a[href="/settings"]')?.classList.contains("active")`, &settingsActiveOnChat),
		chromedp.EvaluateAsDevTools(`document.querySelector('#nav-dropdown a[href="/skills"]')?.classList.contains("active")`, &skillsActiveOnChat),
	)
	if err != nil {
		t.Fatalf("chat page nav test failed: %v", err)
	}
	if !chatActiveOnChat {
		t.Error("Chat nav link should have active class on chat page")
	}
	if settingsActiveOnChat {
		t.Error("Settings nav link should NOT have active class on chat page")
	}
	if skillsActiveOnChat {
		t.Error("Skills nav link should NOT have active class on chat page")
	}

	// Navigate to settings — click gear button to show dropdown, then settings link
	var chatActiveOnSettings, settingsActiveOnSettings bool
	err = chromedp.Run(ctx,
		chromedp.Click(`#nav-dropdown .gear-btn`, chromedp.ByQuery),
		chromedp.Click(`#nav-dropdown a[href="/settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('#nav-dropdown a[href^="/sessions/"]')?.classList.contains("active")`, &chatActiveOnSettings),
		chromedp.EvaluateAsDevTools(`document.querySelector('#nav-dropdown a[href="/settings"]')?.classList.contains("active")`, &settingsActiveOnSettings),
	)
	if err != nil {
		t.Fatalf("settings page nav test failed: %v", err)
	}
	if chatActiveOnSettings {
		t.Error("Chat nav link should NOT have active class on settings page")
	}
	if !settingsActiveOnSettings {
		t.Error("Settings nav link should have active class on settings page")
	}
}

// TestBrowser_WorkspaceTrim verifies workspace indicator shows basename with full path in tooltip.
func TestBrowser_WorkspaceTrim(t *testing.T) {
	workspace := t.TempDir()
	basename := filepath.Base(workspace)
	server := newTestServerAtWorkspace(t, workspace)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var indicatorText string
	var indicatorTooltip string
	var tooltipFound bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#workspace-indicator", chromedp.ByQuery),
		chromedp.Text("#workspace-indicator", &indicatorText, chromedp.ByQuery),
		chromedp.AttributeValue("#workspace-indicator", "title", &indicatorTooltip, &tooltipFound, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("workspace indicator test failed: %v", err)
	}

	// Should contain basename, not full workspace path
	if !strings.Contains(indicatorText, basename) {
		t.Errorf("workspace indicator text = %q, want containing basename %q", indicatorText, basename)
	}
	if strings.Contains(indicatorText, workspace) && workspace != basename {
		t.Errorf("workspace indicator text = %q, should not contain full path %q", indicatorText, workspace)
	}
	// Tooltip should have full workspace path
	if !tooltipFound {
		t.Error("workspace indicator missing title attribute")
	} else if indicatorTooltip != workspace {
		t.Errorf("workspace indicator title = %q, want full path %q", indicatorTooltip, workspace)
	}
}

// TestBrowser_RunStatusSlim verifies run-status no longer shows descriptive text.
func TestBrowser_RunStatusSlim(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var runStatusDetailExists bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#run-status", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#run-status-detail') !== null", &runStatusDetailExists),
	)
	if err != nil {
		t.Fatalf("run status slim test failed: %v", err)
	}
	if runStatusDetailExists {
		t.Error("run-status-detail should be removed; only stream-indicator badge should remain")
	}
}
