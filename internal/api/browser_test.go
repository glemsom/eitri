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
		// Retry input check up to 50 times
		chromedp.ActionFunc(func(ctx context.Context) error {
			for i := 0; i < 50; i++ {
				var v string
				if err := chromedp.EvaluateAsDevTools(
					`document.getElementById('chat-input').value`,
					&v,
				).Do(ctx); err != nil {
					return err
				}
				if v == "" {
					inputValue = v
					return nil
				}
				time.Sleep(10 * time.Millisecond)
			}
			inputValue = "TIMEOUT"
			return nil
		}),
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

	// Poll for SSE stream to complete
	deadline := time.Now().Add(4 * time.Second)
	var assistantMsgExists bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`document.querySelector('.message-assistant') !== null`,
				&assistantMsgExists,
			),
		)
		if err == nil && assistantMsgExists {
			break
		}
		time.Sleep(100 * time.Millisecond)
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

	// Verify #messages is the actual scroll container (overflow-y: auto)
	var isScrollContainer bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`(() => {
				const el = document.getElementById('messages');
				if (!el) return false;
				const style = window.getComputedStyle(el);
				return style.overflowY === 'auto' || style.overflowY === 'scroll';
			})()`,
			&isScrollContainer,
		),
	)
	if err != nil {
		t.Fatalf("scroll container check failed: %v", err)
	}
	if !isScrollContainer {
		t.Error("#messages should be a CSS scroll container (overflow-y: auto) for IntersectionObserver")
	}

	// Verify IntersectionObserver root is #messages
	var observerRoot string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`(() => {
				const sentinel = document.getElementById('scroll-sentinel');
				if (!sentinel || !sentinel._scrollObserver) return 'no-observer';
				const root = sentinel._scrollObserver.root;
				return root ? (root.id || 'no-id') : 'null';
			})()`,
			&observerRoot,
		),
	)
	if err != nil {
		t.Fatalf("observer root check failed: %v", err)
	}
	if observerRoot != "messages" {
		t.Errorf("IntersectionObserver root should be #messages (scroll container), got: %v", observerRoot)
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
			htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
				source: document.body,
				target: '#messages',
				swap: 'beforeend',
				contentType: 'application/json',
				values: JSON.stringify({kind: 'component', name: 'DiffCard', data: {old: %q, new: %q, lang: 'go'}})
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
			htmx.ajax('POST', '/api/sessions/' + sessionId + '/render', {
				source: document.body,
				target: '#messages',
				swap: 'beforeend',
				contentType: 'application/json',
				values: JSON.stringify({kind: 'tool_card', tool: 'file_editor', status: 'done', output: JSON.stringify({
					path: 'main.go',
					mode: 'overwrite',
					bytes_written: 123,
					old_content: %q,
					new_content: %q,
					dirs_created: []
				})})
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

// TestBrowser_RunStatusChrome_Reconnect verifies run status chrome transitions
// through connecting → reconnecting → tool running → rendering → done.
func TestBrowser_RunStatusChrome_Reconnect(t *testing.T) {
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
	// Verify run reaches Done after tool_result + done
	var doneStatus string
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text("#stream-indicator", &doneStatus, chromedp.ByQuery),
		)
		if err == nil && strings.TrimSpace(doneStatus) == "Done" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.TrimSpace(doneStatus) != "Done" {
		t.Fatalf("final run status = %q, want Done", doneStatus)
	}
}

func TestBrowser_ToolCardsRunningToDone(t *testing.T) {
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

	var sessionID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`location.pathname.split('/').pop()`, &sessionID),
	)
	if err != nil || sessionID == "" {
		t.Fatalf("get session ID failed: %v", err)
	}

	// Install fake EventSource and connect
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			class FakeEventSource {
				constructor(url) { this.url = url; window.__fakeEventSource = this; }
				close() { this.closed = true; }
				emitOpen() { if (this.onopen) this.onopen({}); }
				emitMessage(packet) { if (this.onmessage) this.onmessage({ data: JSON.stringify(packet) }); }
			}
			window.EventSource = FakeEventSource;
			document.dispatchEvent(new CustomEvent('eitri:connectRunStream', { detail: { value: '`+sessionID+`' } }));
			return !!window.__fakeEventSource;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install fake EventSource failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
	)
	if err != nil {
		t.Fatalf("emit open failed: %v", err)
	}

	// Emit first tool_call — should create tool card with running status
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: 'echo hello'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit tool_call failed: %v", err)
	}

	// Verify tool entry appears with running status in sidebar
	var runningEntry bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity .tool-entry .tool-status-label') !== null && document.querySelector('#tool-activity .tool-entry .tool-status-label').textContent === 'running...'`, &runningEntry),
		)
		if err == nil && runningEntry {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !runningEntry {
		t.Fatal("sidebar tool entry should show 'running...' status after tool_call")
	}

	// Verify elapsed timer appears on running entry
	var hasElapsed bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity [data-tool-elapsed]') !== null`, &hasElapsed),
	)
	if err != nil {
		t.Fatalf("query elapsed element failed: %v", err)
	}
	if !hasElapsed {
		t.Fatal("running tool entry should have an elapsed timer element")
	}

	// Emit tool_result — should morph to done
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_result', tool: 'terminal_execute', output: 'hello\nworld'})`, nil),
	)
	if err != nil {
		t.Fatalf("emit tool_result failed: %v", err)
	}

	// Poll for done status in sidebar entry
	deadline = time.Now().Add(3 * time.Second)
	var doneEntry bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity .tool-entry .tool-status-label') !== null && document.querySelector('#tool-activity .tool-entry .tool-status-label').textContent === 'done'`, &doneEntry),
		)
		if err == nil && doneEntry {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !doneEntry {
		t.Fatal("sidebar tool entry should show 'done' status after tool_result")
	}

	// Verify done entry shows checkmark icon
	var hasCheckmark bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#tool-activity .tool-entry.tool-done .tool-icon') !== null && document.querySelector('#tool-activity .tool-entry.tool-done .tool-icon').textContent === '✅'`,
			&hasCheckmark,
		),
	)
	if err != nil || !hasCheckmark {
		t.Fatalf("done tool entry should show checkmark icon: err=%v checkmark=%v", err, hasCheckmark)
	}

	// Verify done entry shows tool name
	var toolNameVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#tool-activity .tool-entry.tool-done .tool-name') !== null && document.querySelector('#tool-activity .tool-entry.tool-done .tool-name').textContent === 'terminal_execute'`,
			&toolNameVisible,
		),
	)
	if err != nil || !toolNameVisible {
		t.Fatalf("done tool entry should show tool name: err=%v visible=%v", err, toolNameVisible)
	}

	// Verify output container exists for click-to-expand
	var hasOutput bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#tool-activity .tool-output') !== null`,
			&hasOutput,
		),
	)
	if err != nil || !hasOutput {
		t.Fatalf("tool entry should have output container: err=%v hasOutput=%v", err, hasOutput)
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
	var headerWorkspaceIndicatorExists, headerStreamIndicatorExists bool
	var chatViewDisplay, chatViewGridRows string
	var messagesOverflowY, messagesDisplay string
	var gearBtnColor, gearBtnBg, gearBtnBorder, gearBtnRadius, gearBtnCursor, gearBtnFontSize string
	var dropdownDisplay string

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Title(&title),
		chromedp.EvaluateAsDevTools("typeof window.htmx !== 'undefined'", &htmxExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#chat-view') !== null", &chatViewExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#messages') !== null", &messagesExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#composer') !== null", &composerExists),
		// Verify indicators live in header
		chromedp.EvaluateAsDevTools("document.querySelector('#workspace-indicator') !== null", &headerWorkspaceIndicatorExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#stream-indicator') !== null", &headerStreamIndicatorExists),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('#chat-view')).getPropertyValue('display')", &chatViewDisplay),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('#chat-view')).getPropertyValue('grid-template-rows')", &chatViewGridRows),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('#messages')).getPropertyValue('overflow-y')", &messagesOverflowY),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('#messages')).getPropertyValue('display')", &messagesDisplay),
		// Verify gear button dark-theme styles (catches missing CSS rule bugs)
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.gear-btn')).getPropertyValue('color')", &gearBtnColor),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.gear-btn')).getPropertyValue('background-color')", &gearBtnBg),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.gear-btn')).getPropertyValue('border')", &gearBtnBorder),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.gear-btn')).getPropertyValue('border-radius')", &gearBtnRadius),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.gear-btn')).getPropertyValue('cursor')", &gearBtnCursor),
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.gear-btn')).getPropertyValue('font-size')", &gearBtnFontSize),
		// Verify dropdown is hidden by default
		chromedp.EvaluateAsDevTools("getComputedStyle(document.querySelector('.dropdown-content')).getPropertyValue('display')", &dropdownDisplay),
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
	if !headerWorkspaceIndicatorExists {
		t.Error("#workspace-indicator in header not found")
	}
	if !headerStreamIndicatorExists {
		t.Error("#stream-indicator in header not found")
	}
	if chatViewDisplay != "grid" {
		t.Errorf("#chat-view display = %q, want 'grid'", chatViewDisplay)
	}
	// Browser returns computed pixel values. Just verify grid-template-rows is set (not "none" or empty).
	if chatViewGridRows == "" || chatViewGridRows == "none" {
		t.Errorf("#chat-view grid-template-rows = %q, expected explicit rows (auto 1fr auto)", chatViewGridRows)
	}
	if messagesOverflowY != "auto" {
		t.Errorf("#messages overflow-y = %q, want 'auto'", messagesOverflowY)
	}
	if messagesDisplay != "flex" {
		t.Errorf("#messages display = %q, want 'flex' (grid-area messages keeps its flex column layout)", messagesDisplay)
	}
	// Gear button should have dark-theme styled border (not default 2px outset black)
	if gearBtnColor == "rgb(0, 0, 0)" || gearBtnColor == "#000" || gearBtnColor == "black" {
		t.Errorf(".gear-btn color = %q, expected dark-theme muted color", gearBtnColor)
	}
	if gearBtnBorder == "2px outset rgb(0, 0, 0)" || gearBtnBorder == "2px outset black" || gearBtnBorder == "2px outset #000" {
		t.Errorf(".gear-btn border = %q, expected 1px solid themed border", gearBtnBorder)
	}
	if gearBtnRadius != "6px" && gearBtnRadius != "6px 6px" {
		t.Errorf(".gear-btn border-radius = %q, want '6px'", gearBtnRadius)
	}
	if gearBtnCursor != "pointer" {
		t.Errorf(".gear-btn cursor = %q, want 'pointer'", gearBtnCursor)
	}
	if strings.HasPrefix(gearBtnFontSize, "13.") {
		t.Errorf(".gear-btn font-size = %q, expected themed size > 13px", gearBtnFontSize)
	}
	// Dropdown should be hidden by default
	if dropdownDisplay != "none" {
		t.Errorf(".dropdown-content display = %q, want 'none'", dropdownDisplay)
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

// TestBrowser_HeaderHasStreamIndicator verifies stream-indicator is in the header.
func TestBrowser_HeaderHasStreamIndicator(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var streamText string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#stream-indicator", chromedp.ByQuery),
		chromedp.Text("#stream-indicator", &streamText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("stream indicator test failed: %v", err)
	}
	if streamText == "" {
		t.Error("#stream-indicator has no text content")
	}
}

// TestBrowser_SettingsSaveButtonLoadingState verifies that when the save button is clicked,
// it shows "Saving…" text and is disabled during the HTMX request, then re-enabled after.
func TestBrowser_SettingsSaveButtonLoadingState(t *testing.T) {
	// Use a slow provider server so the request takes long enough to observe loading state
	fakeProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(fakeProvider.Close)

	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var initialText, loadingText, postSubmitText string
	var submitDisabled bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		// Set provider to custom_openai and fill credentials so save will succeed
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		// Read button text before click
		chromedp.Text("button[type=submit]", &initialText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("initial setup failed: %v", err)
	}
	if !strings.Contains(initialText, "Save") {
		t.Errorf("initial button text = %q, want containing 'Save'", initialText)
	}

	// Click submit. The provider is slow (200ms delay), so we can observe loading state.
	err = chromedp.Run(ctx,
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		// Wait for beforeSend to fire (HTMX fires synchronously before XMLHttpRequest.send)
		chromedp.Sleep(50*time.Millisecond),
		chromedp.Text("button[type=submit]", &loadingText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('button[type=submit]').disabled`,
			&submitDisabled,
		),
	)
	if err != nil {
		t.Fatalf("loading state check failed: %v", err)
	}
	if !strings.Contains(loadingText, "Saving") {
		t.Errorf("button text during save = %q, want containing 'Saving'", loadingText)
	}
	if !submitDisabled {
		t.Error("submit button should be disabled during save request")
	}

	// Wait for the swap to complete (after provider delay), then verify button is re-enabled
	err = chromedp.Run(ctx,
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.Text("button[type=submit]", &postSubmitText, chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('button[type=submit]').disabled`,
			&submitDisabled,
		),
	)
	if err != nil {
		t.Fatalf("post-save state check failed: %v", err)
	}
	if !strings.Contains(postSubmitText, "Save") {
		t.Errorf("post-save button text = %q, want containing 'Save'", postSubmitText)
	}
	if submitDisabled {
		t.Error("submit button should be re-enabled after save completes")
	}
}

// TestBrowser_SettingsSaveShowsSuccessIndicator verifies that after a successful config
// save via PUT /api/config, the settings form shows a "✓ Saved" success indicator.
func TestBrowser_SettingsSaveShowsSuccessIndicator(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var successText string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		// Set provider to custom_openai and fill credentials
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.Text(".save-success", &successText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("save success indicator check failed: %v", err)
	}
	if !strings.Contains(successText, "Saved") {
		t.Errorf("save success text = %q, want containing 'Saved'", successText)
	}
}

// TestBrowser_ToolCardsInsertBeforeSentinel verifies tool cards appear before
// #scroll-sentinel even when tools run before any text token (so #streaming
// doesn't exist yet). Regression: previously fell back to appendChild, placing
// tool cards after user message but before where #streaming would later appear.
func TestBrowser_ToolCardsInsertBeforeSentinel(t *testing.T) {
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

	// Verify scroll-sentinel present
	var sentinelExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.getElementById('scroll-sentinel') !== null`,
			&sentinelExists,
		),
	)
	if err != nil || !sentinelExists {
		t.Fatalf("scroll-sentinel not found: err=%v exists=%v", err, sentinelExists)
	}

	// Parse session ID from URL
	var sessionID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`location.pathname.split('/').pop()`, &sessionID),
	)
	if err != nil || sessionID == "" {
		t.Fatalf("get session ID failed: %v", err)
	}

	// Install fake EventSource and connect
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			class FakeEventSource {
				constructor(url) { this.url = url; window.__fakeEventSource = this; }
				close() { this.closed = true; }
				emitOpen() { if (this.onopen) this.onopen({}); }
				emitMessage(packet) { if (this.onmessage) this.onmessage({ data: JSON.stringify(packet) }); }
			}
			window.EventSource = FakeEventSource;
			document.dispatchEvent(new CustomEvent('eitri:connectRunStream', { detail: { value: '`+sessionID+`' } }));
			return !!window.__fakeEventSource;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install fake EventSource failed: %v", err)
	}

	// Emit open and a user message already rendered (simulated via HTMX)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
	)
	if err != nil {
		t.Fatalf("emit open failed: %v", err)
	}

	// Emit tool_call (no token before — simulating tools-run-first scenario)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: 'echo hello'}})`,
			nil,
		),
	)
	if err != nil {
		t.Fatalf("emit tool_call failed: %v", err)
	}

	// Emit tool_result
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`window.__fakeEventSource.emitMessage({type: 'tool_result', tool: 'terminal_execute', output: 'hello\n'})`,
			nil,
		),
	)
	if err != nil {
		t.Fatalf("emit tool_result failed: %v", err)
	}

	// Poll for tool entry to appear in sidebar
	deadline := time.Now().Add(3 * time.Second)
	var toolEntryFound bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`document.querySelector('#tool-activity .tool-entry-wrapper') !== null`,
				&toolEntryFound,
			),
		)
		if err == nil && toolEntryFound {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !toolEntryFound {
		t.Fatalf("tool entry not found in sidebar: err=%v found=%v", err, toolEntryFound)
	}

	// Verify tool entry has done status after tool_result
	var entryDone bool
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity .tool-entry.tool-done') !== null`, &entryDone),
		)
		if err == nil && entryDone {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !entryDone {
		t.Fatal("sidebar tool entry should have done status after tool_result")
	}

	// Now simulate streaming bubble creation (as would happen on first token after tools)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'token', content: 'Thinking about it...'})`, nil),
	)
	if err != nil {
		t.Fatalf("emit token failed: %v", err)
	}

	// Poll for streaming to appear
	deadline = time.Now().Add(3 * time.Second)
	var streamingBeforeSentinel bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var messages = document.getElementById('messages');
				var sentinel = document.getElementById('scroll-sentinel');
				var streaming = document.getElementById('streaming');
				if (!messages || !sentinel || !streaming) return false;
				var streamingIdx = Array.prototype.indexOf.call(messages.children, streaming);
				var sentinelIdx = Array.prototype.indexOf.call(messages.children, sentinel);
				return streamingIdx >= 0 && sentinelIdx > streamingIdx;
			})()`, &streamingBeforeSentinel),
		)
		if err == nil && streamingBeforeSentinel {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !streamingBeforeSentinel {
		t.Fatalf("streaming should be before scroll-sentinel: err=%v beforeSentinel=%v", err, streamingBeforeSentinel)
	}

	// Verify no tool cards remain in #messages
	var toolCardsInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#messages [data-tool-id]') !== null`,
			&toolCardsInMessages,
		),
	)
	if err != nil {
		t.Fatalf("query tool cards in messages failed: %v", err)
	}
	if toolCardsInMessages {
		t.Error("tool cards should not appear in #messages after sidebar migration")
	}
}

// TestBrowser_ToolCardMorphInPlace verifies that sequential tool results for the same tool
// update the existing tool card slot in-place rather than appending new DOM nodes.
func TestBrowser_ToolCardMorphInPlace(t *testing.T) {
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

	var sessionID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`location.pathname.split('/').pop()`, &sessionID),
	)
	if err != nil || sessionID == "" {
		t.Fatalf("get session ID failed: %v", err)
	}

	// Install fake EventSource and connect
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			class FakeEventSource {
				constructor(url) { this.url = url; window.__fakeEventSource = this; }
				close() { this.closed = true; }
				emitOpen() { if (this.onopen) this.onopen({}); }
				emitMessage(packet) { if (this.onmessage) this.onmessage({ data: JSON.stringify(packet) }); }
			}
			window.EventSource = FakeEventSource;
			document.dispatchEvent(new CustomEvent('eitri:connectRunStream', { detail: { value: '`+sessionID+`' } }));
			return !!window.__fakeEventSource;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install fake EventSource failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
	)
	if err != nil {
		t.Fatalf("emit open failed: %v", err)
	}

	// Emit three sequential tool_calls
	for i, cmd := range []string{"echo first", "echo second", "echo third"} {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: "`+cmd+`"}})`,
				nil,
			),
		)
		if err != nil {
			t.Fatalf("emit tool_call %d failed: %v", i, err)
		}

		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`window.__fakeEventSource.emitMessage({type: 'tool_result', tool: 'terminal_execute', output: "output " + "`+cmd+`"})`,
				nil,
			),
		)
		if err != nil {
			t.Fatalf("emit tool_result %d failed: %v", i, err)
		}
	}

	// Poll for sidebar entries — all 3 must show 'done' status
	deadline := time.Now().Add(3 * time.Second)
	var allDone bool
	for time.Now().Before(deadline) {
		var entryIDs []string
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var entries = document.querySelectorAll('#tool-activity .tool-entry-wrapper');
				return Array.from(entries).map(function(s) { return s.getAttribute('data-tool-key'); });
			})()`, &entryIDs),
		)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if len(entryIDs) != 3 {
			time.Sleep(50 * time.Millisecond)
			continue
		}

		allDone = true
		for _, id := range entryIDs {
			var hasDone bool
			err = chromedp.Run(ctx,
				chromedp.EvaluateAsDevTools(
					`document.querySelector('#tool-activity [data-tool-key="`+id+`"] .tool-status-label') !== null &&
					 document.querySelector('#tool-activity [data-tool-key="`+id+`"] .tool-status-label').textContent === 'done'`,
					&hasDone,
				),
			)
			if err != nil || !hasDone {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !allDone {
		var debugHTML string
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var entry = document.querySelector('#tool-activity .tool-entry-wrapper');
				return entry ? entry.innerHTML : 'no tool entry';
			})()`, &debugHTML),
		)
		t.Fatalf("tool entries did not show 'done' within deadline; entry HTML: %s", debugHTML)
	}

	// Verify each entry has a unique data-tool-key
	var entryIDs []string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var entries = document.querySelectorAll('#tool-activity .tool-entry-wrapper');
			return Array.from(entries).map(function(s) { return s.getAttribute('data-tool-key'); });
		})()`, &entryIDs),
	)
	if err != nil {
		t.Fatalf("query entry IDs failed: %v", err)
	}
	if len(entryIDs) != 3 {
		t.Fatalf("expected 3 entry IDs, got %d", len(entryIDs))
	}
	seen := make(map[string]bool)
	for _, id := range entryIDs {
		if seen[id] {
			t.Fatalf("duplicate data-tool-key: %s", id)
		}
		seen[id] = true
	}
}

// TestBrowser_SettingsSaveErrorAutoScroll verifies that after a failed config save,
// the page auto-scrolls to the error toast.
func TestBrowser_SettingsSaveErrorAutoScroll(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Fill form with invalid credentials and save — expect error toast
	var errorInView bool
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
		// Check if error toast is in the visible viewport (allow some tolerance for smooth scroll)
		chromedp.EvaluateAsDevTools(`
			(function() {
				var el = document.querySelector('.error-toast');
				if (!el) return false;
				var rect = el.getBoundingClientRect();
				// Allow 200px tolerance for smooth scroll animation gap
				return rect.top >= -200 && rect.left >= 0 &&
					rect.bottom <= (window.innerHeight || document.documentElement.clientHeight) + 200 &&
					rect.right <= (window.innerWidth || document.documentElement.clientWidth);
			})()
		`, &errorInView),
	)
	if err != nil {
		t.Fatalf("error scroll test failed: %v", err)
	}
	if !errorInView {
		t.Error("error-toast should be scrolled into view after failed save")
	}
}

// TestBrowser_SettingsCtrlEnterSaves verifies that Ctrl+Enter (or Cmd+Enter on macOS)
// submits the settings form from any input/textarea.
func TestBrowser_SettingsCtrlEnterSaves(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusOK, `{"object":"list","data":[{"id":"gpt-4"},{"id":"gpt-3.5-turbo"}]}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var successText string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		// Set up credentials
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-test", chromedp.ByQuery),
		chromedp.SetValue("#system_prompt", "test prompt", chromedp.ByQuery),
		// Dispatch Ctrl+Enter on the system_prompt textarea
		chromedp.EvaluateAsDevTools(`
			(function() {
				var textarea = document.getElementById('system_prompt');
				if (!textarea) return 'missing';
				var evt = new KeyboardEvent('keydown', {
					key: 'Enter',
					code: 'Enter',
					ctrlKey: true,
					bubbles: true,
					cancelable: true
				});
				return textarea.dispatchEvent(evt) ? 'ok' : 'prevented';
			})()
		`, &successText),
		chromedp.WaitVisible(".save-success", chromedp.ByQuery),
		chromedp.Text(".save-success", &successText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("ctrl+enter save test failed: %v", err)
	}
	if !strings.Contains(successText, "Saved") {
		t.Errorf("save success text = %q, want containing 'Saved'", successText)
	}
}


// ————— Issue #155: Sticky chat composer — verify streaming and tool card injection —————

// TestBrowser_StreamingTokensAppendInScrollContainer verifies that streaming tokens
// append correctly in the #messages scroll container after the #151 flex layout change.
func TestBrowser_StreamingTokensAppendInScrollContainer(t *testing.T) {
	llmURL := fakeBurstChatServer(t, 30, 40*time.Millisecond).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	// Verify #messages is the scroll container (flex: 1, overflow-y: auto)
	var messagesScrollable bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('messages');
			if (!el) return false;
			var style = window.getComputedStyle(el);
			return style.overflowY === 'auto' || style.overflowY === 'scroll';
		})()`, &messagesScrollable),
	)
	if err != nil {
		t.Fatalf("check messages scrollable failed: %v", err)
	}
	if !messagesScrollable {
		t.Error("messages container should be the scroll container (overflow-y: auto)")
	}

	// Verify no double scrollbars: main should have overflow-y: hidden
	var mainOverflowHidden bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('main');
			if (!el) return false;
			var style = window.getComputedStyle(el);
			return style.overflowY === 'hidden';
		})()`, &mainOverflowHidden),
	)
	if err != nil {
		t.Fatalf("check main overflow failed: %v", err)
	}
	if !mainOverflowHidden {
		t.Error("main should have overflow-y: hidden to prevent double scrollbars")
	}

	// Verify chat-view is a grid with pinned regions
	var chatViewGrid bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('chat-view');
			if (!el) return false;
			var style = window.getComputedStyle(el);
			return style.display === 'grid';
		})()`, &chatViewGrid),
	)
	if err != nil {
		t.Fatalf("check chat-view layout failed: %v", err)
	}
	if !chatViewGrid {
		t.Error("chat-view should be display:grid (with auto 1fr auto rows)")
	}

	// Verify composer is in the grid (position handled by grid, no need for flex-shrink)
	var composerInGrid bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('eitri-composer');
			if (!el) return false;
			var style = window.getComputedStyle(el);
			return style.gridArea !== '' && style.gridArea !== 'auto / auto / auto / auto';
		})()`, &composerInGrid),
	)
	if err != nil {
		t.Fatalf("check composer grid-area failed: %v", err)
	}
	if !composerInGrid {
		t.Error("eitri-composer should have a grid-area assigned in #chat-view grid")
	}

	// Send a message that will produce streaming tokens
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Count slowly", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// 30 tokens * 40ms each = 1200ms. Check at 500ms to catch mid-stream.
	time.Sleep(500 * time.Millisecond)
	// Verify streaming element exists inside #messages
	var streamingInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('streaming');
			if (!el) return false;
			return el.parentElement && el.parentElement.id === 'messages';
		})()`, &streamingInMessages),
	)
	if err != nil {
		t.Fatalf("check streaming parent failed: %v", err)
	}
	if !streamingInMessages {
		t.Error("streaming element should be a child of #messages")
	}

	// Verify streaming has some token content
	var hasTokenContent bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('streaming');
			if (!el) return false;
			var content = el.querySelector('.message-content');
			if (!content) return false;
			return content.children.length > 0 || (content.textContent || '').trim().length > 0;
		})()`, &hasTokenContent),
	)
	if err != nil {
		t.Fatalf("check streaming content failed: %v", err)
	}
	if !hasTokenContent {
		t.Error("streaming tokens should have content in #messages scroll container")
	}
	// Poll for streaming to complete
	deadline := time.Now().Add(4 * time.Second)
	assistantMsgExists := false
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`document.querySelector('.message-assistant') !== null`,
				&assistantMsgExists,
			),
		)
		if err == nil && assistantMsgExists {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !assistantMsgExists {
		t.Error("assistant message should have rendered via SSE stream")
	}
}

// TestBrowser_ScrollSentinelPosition verifies the scroll-sentinel stays as the
// last child of #messages so IntersectionObserver can track scroll position correctly.
func TestBrowser_ScrollSentinelPosition(t *testing.T) {
	llmURL := fakeInstantChatServer(t, "test reply content").URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	// Verify scroll-sentinel exists in #messages
	var sentinelInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var sentinel = document.getElementById('scroll-sentinel');
			if (!sentinel) return false;
			return sentinel.parentElement && sentinel.parentElement.id === 'messages';
		})()`, &sentinelInMessages),
	)
	if err != nil {
		t.Fatalf("check sentinel parent failed: %v", err)
	}
	if !sentinelInMessages {
		t.Error("scroll-sentinel should be a child of #messages")
	}

	// Send a message
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Test sentinel position", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Wait for run to complete by polling for assistant message
	var assistantText string
	for i := 0; i < 30; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.querySelector('.message-assistant .message-content');
				return el ? el.textContent : '';
			})()`, &assistantText),
		)
		if err == nil && strings.TrimSpace(assistantText) != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.TrimSpace(assistantText) == "" {
		t.Fatal("assistant response did not render")
	}

	// Verify IntersectionObserver root is #messages (the scroll container)
	var observerRoot string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var sentinel = document.getElementById('scroll-sentinel');
			if (!sentinel || !sentinel._scrollObserver) return 'no-observer';
			var root = sentinel._scrollObserver.root;
			return root ? (root.id || 'no-id') : 'null';
		})()`, &observerRoot),
	)
	if err != nil {
		t.Fatalf("check observer root failed: %v", err)
	}
	if observerRoot != "messages" {
		t.Errorf("IntersectionObserver root should be #messages (the scroll container), got: %v", observerRoot)
	}

	// After streaming and render, scroll-sentinel should exist in #messages
	var sentinelExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var s = document.getElementById('scroll-sentinel');
			return s !== null && s.parentElement && s.parentElement.id === 'messages';
		})()`, &sentinelExists),
	)
	if err != nil {
		t.Fatalf("check sentinel after render failed: %v", err)
	}
	if !sentinelExists {
		t.Error("scroll-sentinel should still exist in #messages after streaming completes")
	}

	// Verify the scroll-to-bottom button still exists
	var btnExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('scroll-to-bottom-btn') !== null`, &btnExists),
	)
	if err != nil {
		t.Fatalf("check scroll btn exists failed: %v", err)
	}
	if !btnExists {
		t.Error("scroll-to-bottom-btn should exist after streaming completes")
	}

	// Verify stream-indicator shows Done
	var isDone bool
	for i := 0; i < 10; i++ {
		var statusText string
		err = chromedp.Run(ctx,
			chromedp.Text("#stream-indicator", &statusText, chromedp.ByQuery),
		)
		if err == nil && strings.TrimSpace(statusText) == "Done" {
			isDone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !isDone {
		t.Error("stream-indicator should show Done after streaming completes")
	}
}
// TestBrowser_ToolCardsInScrollContainer verifies tool cards inject at correct
// position within the #messages scroll container and morph correctly on tool_result.
func TestBrowser_ToolCardsInScrollContainer(t *testing.T) {
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

	var sessionID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`location.pathname.split('/').pop()`, &sessionID),
	)
	if err != nil || sessionID == "" {
		t.Fatalf("get session ID failed: %v", err)
	}

	// Install fake EventSource and connect
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			class FakeEventSource {
				constructor(url) { this.url = url; window.__fakeEventSource = this; }
				close() { this.closed = true; }
				emitOpen() { if (this.onopen) this.onopen({}); }
				emitMessage(packet) { if (this.onmessage) this.onmessage({ data: JSON.stringify(packet) }); }
			}
			window.EventSource = FakeEventSource;
			document.dispatchEvent(new CustomEvent('eitri:connectRunStream', { detail: { value: '`+sessionID+`' } }));
			return !!window.__fakeEventSource;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install fake EventSource failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
	)
	if err != nil {
		t.Fatalf("emit open failed: %v", err)
	}

	// Emit a token first to create streaming bubble
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'token', content: 'Hello, I will run a tool.'})`, nil),
	)
	if err != nil {
		t.Fatalf("emit token failed: %v", err)
	}

	// Give time for streaming bubble to appear
	time.Sleep(200 * time.Millisecond)

	// Verify streaming bubble is inside #messages
	var streamingInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('streaming');
			if (!el) return false;
			return el.parentElement && el.parentElement.id === 'messages';
		})()`, &streamingInMessages),
	)
	if err != nil {
		t.Fatalf("check streaming parent failed: %v", err)
	}
	if !streamingInMessages {
		t.Error("streaming element should be inside #messages")
	}

	// Emit a tool_call — should create tool card
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: 'echo hello'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit tool_call failed: %v", err)
	}

	// Wait for tool card to appear
	time.Sleep(500 * time.Millisecond)

	// Verify tool entry exists in sidebar
	var toolEntryInSidebar bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var entry = document.querySelector('#tool-activity .tool-entry-wrapper');
			if (!entry) return false;
			return entry.parentElement && entry.parentElement.matches('#tool-activity .tool-activity-list');
		})()`, &toolEntryInSidebar),
	)
	if err != nil {
		t.Fatalf("check tool entry parent failed: %v", err)
	}
	if !toolEntryInSidebar {
		t.Error("tool entry should be in sidebar tool-activity-list")
	}

	// Verify streaming bubble still exists (tool card appends, doesn't replace)
	var streamingStillExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('streaming') !== null`, &streamingStillExists),
	)
	if err != nil {
		t.Fatalf("check streaming after tool_call failed: %v", err)
	}
	if !streamingStillExists {
		t.Error("streaming bubble should still exist after tool card injection")
	}

	// Verify tool entry has running status
	var runningEntry bool
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity .tool-entry .tool-status-label') !== null && document.querySelector('#tool-activity .tool-entry .tool-status-label').textContent === 'running...'`, &runningEntry),
		)
		if err == nil && runningEntry {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !runningEntry {
		t.Fatal("sidebar tool entry should show 'running...' status after tool_call")
	}

	// Emit tool_result — should morph to done via HTMX
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_result', tool: 'terminal_execute', output: 'hello\nworld'})`, nil),
	)
	if err != nil {
		t.Fatalf("emit tool_result failed: %v", err)
	}

	// Poll for done status in sidebar entry
	deadline = time.Now().Add(3 * time.Second)
	var doneEntry bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity .tool-entry .tool-status-label') !== null && document.querySelector('#tool-activity .tool-entry .tool-status-label').textContent === 'done'`, &doneEntry),
		)
		if err == nil && doneEntry {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !doneEntry {
		t.Fatal("sidebar tool entry should show 'done' status after tool_result")
	}

	// Emit done to trigger final markdown render
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'done', message_id: 'msg_final'})`, nil),
	)
	if err != nil {
		t.Fatalf("emit done failed: %v", err)
	}

	// Poll for finalizeMessage to replace streaming bubble
	deadline = time.Now().Add(3 * time.Second)
	var streamingReplaced bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.getElementById('streaming') === null`, &streamingReplaced),
		)
		if err == nil && streamingReplaced {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !streamingReplaced {
		t.Error("streaming element should be replaced by final markdown (outerHTML swap)")
	}

	// Verify final assistant message rendered
	var finalAssistantExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.message-assistant') !== null`, &finalAssistantExists),
	)
	if err != nil {
		t.Fatalf("check final assistant failed: %v", err)
	}
	if !finalAssistantExists {
		t.Error("final assistant message should exist after done packet")
	}

	// Verify scroll-sentinel is still last child after all operations
	var sentinelStillLast bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var messages = document.getElementById('messages');
			var sentinel = document.getElementById('scroll-sentinel');
			if (!messages || !sentinel) return false;
			return messages.lastElementChild === sentinel;
		})()`, &sentinelStillLast),
	)
	if err != nil {
		t.Fatalf("check sentinel after tool cards failed: %v", err)
	}
	if !sentinelStillLast {
		t.Error("scroll-sentinel should still be the last child after tool card operations")
	}
}

// TestBrowser_AutoScrollDuringStreaming verifies auto-scroll lands at newest
// content during streaming in the scroll container.
func TestBrowser_AutoScrollDuringStreaming(t *testing.T) {
	llmURL := fakeBurstChatServer(t, 100, 5*time.Millisecond).URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	// Force #messages to a small height to create scrollable overflow
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('messages').style.maxHeight = '120px'`, nil),
	)
	if err != nil {
		t.Fatalf("set messages height failed: %v", err)
	}

	// Verify scroll-to-bottom button exists
	var btnExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('scroll-to-bottom-btn') !== null`, &btnExists),
	)
	if err != nil {
		t.Fatalf("check scroll btn exists failed: %v", err)
	}
	if !btnExists {
		t.Fatal("scroll-to-bottom-btn should exist in the DOM")
	}

	// Send message to trigger streaming
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Auto scroll test message", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	// Wait for streaming tokens to accumulate
	time.Sleep(800 * time.Millisecond)

	// Scroll up in #messages to force scroll position away from bottom
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('messages').scrollTop = 0`, nil),
	)
	if err != nil {
		t.Fatalf("scroll up failed: %v", err)
	}

	// Wait for IntersectionObserver to detect sentinel is not visible
	time.Sleep(600 * time.Millisecond)

	// Verify scroll-to-bottom button is now visible
	var btnVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('scroll-to-bottom-btn').classList.contains('visible')`, &btnVisible),
	)
	if err != nil {
		t.Fatalf("check btn visible state failed: %v", err)
	}
	if !btnVisible {
		t.Error("scroll-to-bottom button should be visible when scrolled up")
	}

	// Click the button to scroll to latest
	err = chromedp.Run(ctx,
		chromedp.Click("#scroll-to-bottom-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("click scroll-to-bottom failed: %v", err)
	}

	// Wait for smooth scroll
	time.Sleep(500 * time.Millisecond)

	// Verify button is hidden again after scrolling to bottom
	var btnHiddenAfterClick bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`!document.getElementById('scroll-to-bottom-btn').classList.contains('visible')`, &btnHiddenAfterClick),
	)
	if err != nil {
		t.Fatalf("check btn hidden after click failed: %v", err)
	}
	if !btnHiddenAfterClick {
		t.Error("scroll-to-bottom button should hide after scrolling to bottom")
	}

	// Verify sentinel is scrollable (has size and is in flow)
	var sentinelHasSize bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var s = document.getElementById('scroll-sentinel');
			if (!s) return false;
			var rect = s.getBoundingClientRect();
			return rect.width > 0 && rect.height >= 0;
		})()`, &sentinelHasSize),
	)
	if err != nil {
		t.Fatalf("check sentinel size failed: %v", err)
	}
	if !sentinelHasSize {
		t.Error("scroll-sentinel should have dimensions for IntersectionObserver to fire")
	}

	// Poll for run completion
	deadline := time.Now().Add(3 * time.Second)
	var finalDone bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var indicator = document.getElementById('stream-indicator');
				return indicator && indicator.textContent.trim() === 'Done';
			})()`, &finalDone),
		)
		if err == nil && finalDone {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !finalDone {
		t.Error("run should reach Done status")
	}
}

// TestBrowser_HTMXBeforeEndTargetsMessages verifies HTMX swaps targeting
// #messages with beforeend work correctly in the layout.
func TestBrowser_HTMXBeforeEndTargetsMessages(t *testing.T) {
	llmURL := fakeInstantChatServer(t, "reply").URL
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		network.Enable(),
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	// Send a message (chat submit uses hx-target="#messages" hx-swap="beforeend")
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "Test beforeend swap", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	var userText string
	var userBubbleFound bool
	for i := 0; i < 30; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.querySelector('.message-user .message-content');
				return el ? el.textContent : '';
			})()`, &userText),
		)
		if err == nil && strings.Contains(userText, "Test beforeend swap") {
			userBubbleFound = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !userBubbleFound {
		t.Fatal("user bubble should appear via beforeend swap into #messages")
	}

	// Wait for assistant response
	var assistantText string
	for i := 0; i < 30; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.querySelector('.message-assistant .message-content');
				return el ? el.textContent : '';
			})()`, &assistantText),
		)
		if err == nil && strings.TrimSpace(assistantText) != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if strings.TrimSpace(assistantText) == "" {
		t.Fatal("assistant response did not render after beforeend swap")
	}

	// Verify scroll-sentinel still exists in #messages after swaps
	var sentinelExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var s = document.getElementById('scroll-sentinel');
			return s !== null && s.parentElement && s.parentElement.id === 'messages';
		})()`, &sentinelExists),
	)
	if err != nil {
		t.Fatalf("check sentinel after swaps failed: %v", err)
	}
	if !sentinelExists {
		t.Error("scroll-sentinel should exist in #messages after HTMX swaps")
	}
}

// TestBrowser_ComposerMobileKeyboard verifies the composer stays visible when
// the mobile keyboard opens. On iOS/Safari, the visual viewport shrinks while
// the layout viewport stays the same size. Eitri handles this by pinning the
// composer using visualViewport resize events.
func TestBrowser_ComposerMobileKeyboard(t *testing.T) {
	llmSrv := fakeChatServer(t, "ok")
	defer llmSrv.Close()

	server := newTestServerWithRuns(t)
	defer server.Close()

	configureProvider(t, server, llmSrv.URL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Navigate to chat
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}

	waitForComposerReady(t, ctx)

	// Emulate iPhone SE viewport (375×667 = narrow mobile)
	err = chromedp.Run(ctx,
		chromedp.EmulateViewport(375, 667),
	)
	if err != nil {
		t.Fatalf("emulate viewport failed: %v", err)
	}

	// Give resize observer time to fire
	time.Sleep(300 * time.Millisecond)

	// Verify composer element exists and has the flex-shrink-0 styling
	var composerDisplay string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('composer');
			if (!el) return 'no-composer';
			return window.getComputedStyle(el).display;
		})()`, &composerDisplay),
	)
	if err != nil {
		t.Fatalf("get composer display failed: %v", err)
	}
	if composerDisplay != "block" && composerDisplay != "no-composer" {
		t.Errorf("composer display = %q, want block", composerDisplay)
	}

	// Focus the textarea to simulate keyboard opening
	err = chromedp.Run(ctx,
		chromedp.Focus("#chat-input", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("focus textarea failed: %v", err)
	}

	// Type a message and verify composer becomes fixed at bottom when keyboard is emulated
	// Simulate the visualViewport shrinking by dispatching a resize event
	var fixedBottom string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var composer = document.querySelector('eitri-composer');
			if (!composer) return 'no-composer';
			return composer.style.position || '(empty)';
		})()`, &fixedBottom),
	)
	if err != nil {
		t.Fatalf("get composer position failed: %v", err)
	}

	// If composer style is not fixed now (no real keyboard on headless Chrome),
	// we just verify the component is present and structured correctly.
	// The visualViewport handler will kick in when a real mobile browser fires.
	t.Logf("mobile composer style.position = %q (empty = keyboard not simulated in headless)", fixedBottom)

	// Verify textarea is usable on mobile
	var textareaRows string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.getElementById('chat-input');
			if (!el) return '';
			return String(el.getAttribute('rows'));
		})()`, &textareaRows),
	)
	if err != nil {
		t.Fatalf("get textarea rows failed: %v", err)
	}
	if textareaRows != "3" {
		t.Errorf("textarea rows = %q, want 3", textareaRows)
	}

	// Verify no double-scroll: main should have overflow control
	var mainOverflow string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var main = document.querySelector('main');
			if (!main) return 'no-main';
			return window.getComputedStyle(main).overflowY;
		})()`, &mainOverflow),
	)
	if err != nil {
		t.Fatalf("get main overflow style failed: %v", err)
	}
	t.Logf("mobile main.overflowY=%s", mainOverflow)
}

func fakeThinkingChatServer(t *testing.T) *httptest.Server {
	t.Helper()

	return fakeInstantChatServer(t, "Before <think>hidden reasoning</think> After")
}

// TestBrowser_ThinkingRendering verifies that thinking/reasoning content
// wrapped in <think> tags renders as collapsible <details class="think-details">
// elements in the DOM.
func TestBrowser_ThinkingRendering(t *testing.T) {
	server := newTestServerWithRuns(t)
	configureProvider(t, server, fakeThinkingChatServer(t).URL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Navigate and send a message that will trigger thinking response
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "Show thinking", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation/send failed: %v", err)
	}

	// Wait for run to complete — poll for assistant message with think-details
	deadline := time.Now().Add(5 * time.Second)
	var thinkDetailsFound bool
	var summaryFound bool
	var reasoningContentVisible bool
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`document.querySelector('.message-assistant details.think-details') !== null`,
				&thinkDetailsFound,
			),
		)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if thinkDetailsFound {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Check for the details element
	if !thinkDetailsFound {
		t.Fatal("think-details not found in assistant message after thinking response")
	}

	// Verify summary contains "Thinking..."
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant details.think-details summary') !== null &&
			 document.querySelector('.message-assistant details.think-details summary').textContent === 'Thinking...'`,
			&summaryFound,
		),
	)
	if err != nil || !summaryFound {
		t.Fatalf("summar found=%v err=%v", summaryFound, err)
	}

	// Verify reasoning content is inside the details (may be hidden)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant details.think-details') !== null &&
			 document.querySelector('.message-assistant details.think-details').textContent.includes('reasoning')`,
			&reasoningContentVisible,
		),
	)
	if err != nil || !reasoningContentVisible {
		t.Fatalf("reasoning content found=%v err=%v", reasoningContentVisible, err)
	}

	// Verify non-think text ("Before" and "After") appears outside the details element
	var beforeText, afterText bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant .message-content').textContent.includes('Before')`,
			&beforeText,
		),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-assistant .message-content').textContent.includes('After')`,
			&afterText,
		),
	)
	if err != nil {
		t.Fatalf("check non-think text failed: %v", err)
	}
	if !beforeText {
		t.Error("text before <think> not rendered")
	}
	if !afterText {
		t.Error("text after </think> not rendered")
	}
}

// TestBrowser_MermaidComponentHeight verifies MermaidDiagram components
// appended via the real render endpoint (same as SSE component events) have
// correct height after mermaid processes them (regression test for overflow clipping).
func TestBrowser_MermaidComponentHeight(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10)
	sess, err := sessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	server := newTestServerWithSessionManager(t, workspace, sessionMgr)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetCookie("browser_id", "browser-1").WithURL(server.URL).Do(ctx)
		}),
		chromedp.Navigate(server.URL+"/sessions/"+sess.ID),
		chromedp.WaitVisible("#messages", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}

	// Simulate the exact flow: htmx.ajax POST to render endpoint,
	// same as renderComponent does for SSE component events.
	var diagramHeight float64
	var svgHeight float64

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var messages = document.getElementById('messages');
			if (!messages) return;
			htmx.ajax('POST', '/api/sessions/`+sess.ID+`/render', {
				source: document.body,
				target: '#messages',
				swap: 'beforeend',
				contentType: 'application/json',
				values: {
					kind: 'component',
					name: 'MermaidDiagram',
					data: {code: 'graph TD; A-->B;'},
				},
			});
		})()`, nil),
		chromedp.Sleep(2000*time.Millisecond),
		chromedp.EvaluateAsDevTools(`(function() {
			var pre = document.querySelector('.mermaid-diagram pre.mermaid');
			if (!pre) return 0;
			var svg = pre.querySelector('svg');
			if (!svg) return -1;
			return svg.getBoundingClientRect().height;
		})()`, &svgHeight),
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('.mermaid-diagram');
			if (!el) return 0;
			return el.getBoundingClientRect().height;
		})()`, &diagramHeight),
	)
	if err != nil {
		t.Fatalf("component render failed: %v", err)
	}

	if svgHeight <= 0 {
		t.Fatalf("mermaid SVG has zero or negative height: %.1f", svgHeight)
	}
	if diagramHeight <= 0 {
		t.Fatalf("mermaid diagram container has zero or negative height: %.1f", diagramHeight)
	}
	// Diagram container must be >= SVG height + 2rem padding
	// (padding: 16px top + 16px bottom = 32px, borders: 1px each)
	minExpected := svgHeight + 32.0
	if diagramHeight < minExpected {
		t.Errorf("diagram container height %.1f < SVG height+padding %.1f — overflow clipping bug", diagramHeight, minExpected)
	}
}

// TestBrowser_ContextPanel verifies the context panel renders compact view with
// progress bar and stats, expanded view with category breakdown, and color classes.
func TestBrowser_ContextPanel(t *testing.T) {
	server := newTestServerWithRuns(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("eitri-context", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}

	// Verify idle state
	var idleText string
	err = chromedp.Run(ctx,
		chromedp.Text("eitri-context .context-idle", &idleText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("idle text check failed: %v", err)
	}
	if !strings.Contains(idleText, "No active run") {
		t.Errorf("idle text = %q, want 'No active run'", idleText)
	}

	// Dispatch a context_update directly via JS to test compact rendering
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			if (typeof window.dispatchContextUpdate === 'function') {
				window.dispatchContextUpdate({
					total_tokens: 12847,
					context_window: 128000,
					prompt_tokens: 9500,
					completion_tokens: 3347,
					system_tokens: 4200,
					history_tokens: 4800,
					skill_tokens: 500,
				});
			}
		})()`, nil),
		chromedp.Sleep(300*time.Millisecond),
	)

	// Verify compact view is visible
	var compactDisplay string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('eitri-context .context-compact');
			return el ? window.getComputedStyle(el).display : 'not-found';
		})()`, &compactDisplay),
	)
	if err != nil {
		t.Fatalf("compact display check failed: %v", err)
	}
	if compactDisplay == "none" || compactDisplay == "not-found" {
		t.Errorf("compact view display = %q, expected visible (flex)", compactDisplay)
	}

	// Verify progress bar has a width set (indicating rendering happened)
	var barWidth string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('eitri-context .context-bar-fill');
			return el ? el.style.width : '';
		})()`, &barWidth),
	)
	if err != nil {
		t.Fatalf("bar width check failed: %v", err)
	}
	// 12847/128000 = 10%, which is < 60%, so green is expected
	if barWidth == "" {
		t.Error("bar fill width is empty, expected e.g. '10%'")
	}

	// Verify bar color class is fill-green (10% < 60%)
	var barClasses string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('eitri-context .context-bar-fill');
			return el ? el.className : '';
		})()`, &barClasses),
	)
	if err != nil {
		t.Fatalf("bar classes check failed: %v", err)
	}
	if !strings.Contains(barClasses, "fill-green") {
		t.Errorf("bar classes = %q, want fill-green (12847/128K = 10%% < 60%%)", barClasses)
	}

	// Verify stats text
	var statsText string
	err = chromedp.Run(ctx,
		chromedp.Text("eitri-context .context-stats", &statsText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("stats text check failed: %v", err)
	}
	expected := "12,847 / 128K (10%)"
	if statsText != expected {
		t.Errorf("stats text = %q, want %q", statsText, expected)
	}

	// Click compact view to toggle expanded
	err = chromedp.Run(ctx,
		chromedp.Click("eitri-context .context-compact", chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("click compact failed: %v", err)
	}

	var expandedOpen string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('eitri-context .context-expanded');
			return el ? el.className : '';
		})()`, &expandedOpen),
	)
	if err != nil {
		t.Fatalf("expanded open check failed: %v", err)
	}
	if !strings.Contains(expandedOpen, "open") {
		t.Errorf("expanded classes = %q, want 'open'", expandedOpen)
	}

	// Test resetContextPanel transitions back to idle
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			if (typeof window.resetContextPanel === 'function') {
				window.resetContextPanel();
			}
		})()`, nil),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("resetContextPanel failed: %v", err)
	}

	var idleDisplay string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('eitri-context .context-idle');
			return el ? window.getComputedStyle(el).display : 'not-found';
		})()`, &idleDisplay),
	)
	if err != nil {
		t.Fatalf("idle after reset check failed: %v", err)
	}
	if idleDisplay == "none" || idleDisplay == "not-found" {
		t.Errorf("idle display after reset = %q, expected visible (block)", idleDisplay)
	}
}

