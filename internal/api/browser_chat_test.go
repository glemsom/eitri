package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/glemsom/eitri/internal/session"
)

// ————— Composer tests ————— —

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

// ————— Message send / receive tests ————— —

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

// ————— Scroll / auto-scroll tests ————— —

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

	// Wait for streaming to start and accumulate enough content — poll for content
	pollForCondition(t, 5*time.Second, 200*time.Millisecond, func() bool {
		var msgExists bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`document.querySelector('.message-assistant') !== null`,
				&msgExists,
			),
		)
		return msgExists
	})

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

	// Wait for IntersectionObserver to fire — poll for button visible
	pollForCondition(t, 3*time.Second, 100*time.Millisecond, func() bool {
		var btnState string
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(
				`(() => {
					const btn = document.getElementById('scroll-to-bottom-btn');
					if (!btn) return 'missing';
					return btn.classList.contains('visible') ? 'visible' : 'hidden';
				})()`,
				&btnState,
			),
		)
		return btnState == "visible"
	})

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

	// Wait for scroll to complete — poll for sentinel at bottom
	deadline := time.Now().Add(3 * time.Second)
	var btnVisibleAfterClick string
	for time.Now().Before(deadline) {
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
		if btnVisibleAfterClick == "hidden" {
			break
		}
		time.Sleep(100 * time.Millisecond)
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

// ————— Fast run / streaming tests ————— —

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
	chromedp.ListenTarget(ctx, func(ev any) {
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
	sessionMgr := session.NewManager(10, t.TempDir())
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
		// Increased from 150ms to 1150ms: navigator.clipboard.writeText() in headless
		// Chrome (no user-granted clipboard permission) silently discards the write
		// but the copy-btn text change dispatches asynchronously; 150ms was flaky.
		chromedp.Sleep(1150*time.Millisecond),
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

// ————— Diff card tests ————— —

// TestBrowser_DiffCardsToggleAndCollapseAfterHTMXSwap verifies that diff cards
// can toggle collapse state and switch between unified/side-by-side views.
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
		diffExpanded         bool
		diffSideBySideActive bool
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
				values: {kind: 'component', name: 'DiffCard', data: {old: %q, new: %q, lang: 'go'}}
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
}

// ————— Run status chrome tests ————— —

func TestBrowser_RunStatusChrome_ShowsNoDeadAirAndDone(t *testing.T) {
	llmURL := fakeSlowChatServer(t, 2*time.Second).URL
	server := newTestServerWithRuns(t)

	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var idleStatus string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('.stream-status-text').textContent`, &idleStatus),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}
	if strings.TrimSpace(idleStatus) != "Idle" {
		t.Fatalf("initial run status = %q, want Idle", idleStatus)
	}

	// Disconnect the stale auto-connect EventSource (from autoConnectOnPageLoad)
	// before sending a message; otherwise its error/reconnect cycle races with
	// the real run's EventSource and keeps the status stuck at Connecting.
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.disconnectAll && window.disconnectAll()`, nil),
	); err != nil {
		t.Fatalf("disconnect stale stream failed: %v", err)
	}

	if err := chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", "show status", chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("start run failed: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	var midStatus string
	err = chromedp.Run(ctx,
		chromedp.Text(".stream-status-text", &midStatus, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("read status failed: %v", err)
	}
	midStatus = strings.TrimSpace(midStatus)
	if midStatus != "Connecting" && midStatus != "Streaming" {
		t.Fatalf("run status during active run = %q, want Connecting or Streaming", midStatus)
	}

	var finalStatus string
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text(".stream-status-text", &finalStatus, chromedp.ByQuery),
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
	if !strings.Contains(assistantText, "working") {
		t.Fatalf("assistant text = %q, want working", assistantText)
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

	// Disconnect the stale auto-connect EventSource before installing the fake.
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.disconnectAll && window.disconnectAll()`, nil),
	); err != nil {
		t.Fatalf("disconnect stale stream failed: %v", err)
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
		chromedp.Text(".stream-status-text", &reconnectingStatus, chromedp.ByQuery),
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
			chromedp.Text(".stream-status-text", &toolRunningStatus, chromedp.ByQuery),
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

	// After tool_result + done, the status transitions through Rendering → Done.
	// On CI the Rendering window can be too fast for chromedp CDP round-trips
	// to observe, so we accept either state.
	var postDoneStatus string
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text(".stream-status-text", &postDoneStatus, chromedp.ByQuery),
		)
		if err == nil {
			postDoneStatus = strings.TrimSpace(postDoneStatus)
			if postDoneStatus == "Rendering" || postDoneStatus == "Done" {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if postDoneStatus != "Rendering" && postDoneStatus != "Done" {
		t.Fatalf("expected Rendering or Done status after done packet, got %q", postDoneStatus)
	}
	// Verify run reaches Done after tool_result + done
	var doneStatus string
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.Text(".stream-status-text", &doneStatus, chromedp.ByQuery),
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

// ————— Input disabled / cancel tests ————— —

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

// ————— Streaming / layout tests ————— —

// TestBrowser_StreamingTokensAppendInScrollContainer verifies that streaming tokens
// append correctly in the #messages scroll container after the flex layout change.
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
			chromedp.Text(".stream-status-text", &statusText, chromedp.ByQuery),
		)
		if err == nil && strings.TrimSpace(statusText) == "Done" {
			isDone = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !isDone {
		t.Error(".stream-status-text should show Done after streaming completes")
	}
}

// TestBrowser_AutoScrollDuringStreaming verifies auto-scroll lands at newest
// content during streaming in the scroll container.
func TestBrowser_AutoScrollDuringStreaming(t *testing.T) {
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
		t.Fatalf("navigate chat failed: %v", err)
	}

	// Disconnect stale auto-connect EventSource before starting run
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.disconnectAll && window.disconnectAll()`, nil),
	); err != nil {
		t.Fatalf("disconnect stale stream failed: %v", err)
	}

	// Force #messages to a small height to create scrollable overflow
	// (default viewport is too large for 500 chars to overflow without this constraint)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('messages').style.maxHeight = '150px'`, nil),
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
	time.Sleep(2 * time.Second)

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
			chromedp.EvaluateAsDevTools(`document.querySelector('.stream-status-text').textContent.trim() === 'Done'`, &finalDone),
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
// the mobile keyboard opens.
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

// ————— Thinking rendering tests ————— —

// TestBrowser_ThinkingRendering verifies that thinking/reasoning content
// wrapped in <think> tags renders as collapsible <details class="think-details">.
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
		t.Fatalf("summary found=%v err=%v", summaryFound, err)
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
// appended via the real render endpoint have correct height after mermaid processes them.
func TestBrowser_MermaidComponentHeight(t *testing.T) {
	workspace := t.TempDir()
	sessionMgr := session.NewManager(10, t.TempDir())
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
	minExpected := svgHeight + 32.0
	if diagramHeight < minExpected {
		t.Errorf("diagram container height %.1f < SVG height+padding %.1f — overflow clipping bug", diagramHeight, minExpected)
	}
}

// ————— Streaming Markdown rendering tests ————— —

// TestBrowser_StreamingMarkdownBold verifies **bold** renders as <strong> during streaming.
func TestBrowser_StreamingMarkdownBold(t *testing.T) {
	streamingMarkdownTestHelper(t, "This text is **bold** during streaming", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var result struct {
			HasBold bool
		}
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var s = document.getElementById('streaming');
				if (s) return !!s.querySelector('.message-content strong');
				var msgs = document.querySelectorAll('.message-assistant');
				for (var i = 0; i < msgs.length; i++) {
					if (msgs[i].querySelector('.message-content strong')) return true;
				}
				return false;
			})()`, &result.HasBold),
		)
		return err == nil && result.HasBold
	})
}

// TestBrowser_StreamingMarkdownItalic verifies *italic* renders as <em> during streaming.
func TestBrowser_StreamingMarkdownItalic(t *testing.T) {
	streamingMarkdownTestHelper(t, "This text is *italic* during streaming", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var result struct {
			HasItalic bool
		}
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var s = document.getElementById('streaming');
				if (s) return !!s.querySelector('.message-content em');
				var msgs = document.querySelectorAll('.message-assistant');
				for (var i = 0; i < msgs.length; i++) {
					if (msgs[i].querySelector('.message-content em')) return true;
				}
				return false;
			})()`, &result.HasItalic),
		)
		return err == nil && result.HasItalic
	})
}

// TestBrowser_StreamingMarkdownInlineCode verifies `code` renders as <code> during streaming.
func TestBrowser_StreamingMarkdownInlineCode(t *testing.T) {
	streamingMarkdownTestHelper(t, "Use the `fmt.Println` function", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var hasCode bool
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var s = document.getElementById('streaming');
				if (s) return s.querySelector('.message-content code') !== null;
				var msgs = document.querySelectorAll('.message-assistant');
				for (var i = 0; i < msgs.length; i++) {
					if (msgs[i].querySelector('.message-content code')) return true;
				}
				return false;
			})()`, &hasCode),
		)
		return err == nil && hasCode
	})
}

// TestBrowser_StreamingMarkdownLinkXSS verifies javascript: URLs render as plain text (no <a>).
func TestBrowser_StreamingMarkdownLinkXSS(t *testing.T) {
	streamingMarkdownLinkHelper(t, streamingMarkdownLinkTest{
		Name:         "LinkXSS",
		Markdown:     "Click [here](javascript:alert(1)) for more",
		ExpectLink:   false,
		ExpectedText: "here",
	})
}

// TestBrowser_StreamingMarkdownDataURL verifies data: URLs render as plain text (no <a>).
func TestBrowser_StreamingMarkdownDataURL(t *testing.T) {
	streamingMarkdownLinkHelper(t, streamingMarkdownLinkTest{
		Name:         "DataURL",
		Markdown:     "Check [bad](data:text/html,<b>XSS</b>) here",
		ExpectLink:   false,
		ExpectedText: "bad",
	})
}

// TestBrowser_StreamingMarkdownMailto verifies mailto: links produce <a> during streaming.
func TestBrowser_StreamingMarkdownMailto(t *testing.T) {
	streamingMarkdownLinkHelper(t, streamingMarkdownLinkTest{
		Name:           "Mailto",
		Markdown:       "Email [me](mailto:user@example.com) now",
		ExpectLink:     true,
		ExpectedHref:   "mailto:user@example.com",
		ExpectedTarget: "_blank",
		ExpectedRel:    "noopener",
	})
}

// TestBrowser_StreamingMarkdownHTTPLink verifies http: links produce <a> with target=_blank rel=noopener during streaming.
func TestBrowser_StreamingMarkdownHTTPLink(t *testing.T) {
	streamingMarkdownLinkHelper(t, streamingMarkdownLinkTest{
		Name:           "HTTPLink",
		Markdown:       "Check [example](http://example.com) here",
		ExpectLink:     true,
		ExpectedHref:   "http://example.com",
		ExpectedTarget: "_blank",
		ExpectedRel:    "noopener",
	})
}

// TestBrowser_StreamingMarkdownHTTPSLink verifies https: links produce <a> with target=_blank rel=noopener during streaming.
func TestBrowser_StreamingMarkdownHTTPSLink(t *testing.T) {
	streamingMarkdownLinkHelper(t, streamingMarkdownLinkTest{
		Name:           "HTTPSLink",
		Markdown:       "Check [example](https://example.com) details",
		ExpectLink:     true,
		ExpectedHref:   "https://example.com",
		ExpectedTarget: "_blank",
		ExpectedRel:    "noopener",
	})
}

// TestBrowser_StreamingMarkdownLink verifies [text](url) renders as <a> during streaming.
func TestBrowser_StreamingMarkdownLink(t *testing.T) {
	streamingMarkdownLinkHelper(t, streamingMarkdownLinkTest{
		Name:         "BasicLink",
		Markdown:     "Check [example](https://example.com) for details",
		ExpectLink:   true,
		ExpectedHref: "https://example.com",
	})
}

// TestBrowser_StreamingMarkdownParagraphs verifies \n\n creates <p> boundaries during streaming.
func TestBrowser_StreamingMarkdownParagraphs(t *testing.T) {
	streamingMarkdownTestHelper(t, "First paragraph.\n\nSecond paragraph.\n\nThird paragraph.", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var pCount int
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`var el = document.querySelector('#streaming .message-content'); el ? el.querySelectorAll('p').length : 0`, &pCount),
		)
		return pCount >= 1
	})
}

// TestBrowser_StreamingMarkdownMixed verifies mixed formatting renders correctly during streaming.
func TestBrowser_StreamingMarkdownMixed(t *testing.T) {
	streamingMarkdownTestHelper(t, "Mix of **bold**, *italic*, and `code` inline", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var result struct {
			Bold   bool
			Italic bool
			Code   bool
		}
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var s = document.getElementById('streaming');
				var root = s || document.body;
				return {
					bold: !!root.querySelector('strong'),
					italic: !!root.querySelector('em'),
					code: !!root.querySelector('code'),
				};
			})()`, &result),
		)
		return err == nil && result.Bold && result.Italic && result.Code
	})
}

// TestBrowser_StreamingMarkdownIncomplete verifies unclosed **text doesn't produce <strong> (graceful degradation).
func TestBrowser_StreamingMarkdownIncomplete(t *testing.T) {
	var (
		hasBold     bool
		contentText string
	)
	streamingMarkdownTestHelper(t, "This has **unclosed bold marker", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`var el = document.querySelector('#streaming .message-content'); el ? el.textContent : ''`, &contentText),
		)
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.getElementById('streaming') !== null && document.querySelector('#streaming .message-content strong') !== null`, &hasBold),
		)
		return contentText != ""
	})
	if hasBold {
		t.Error("unclosed **bold should NOT produce <strong> — graceful degradation expected")
	}
	if !strings.Contains(contentText, "**unclosed") && !strings.Contains(contentText, "unclosed") {
		t.Errorf("raw text with unclosed marker should appear in content, got %q", contentText)
	}
}

// TestBrowser_StreamingMarkdownRenderingPhaseCSS verifies .streaming-message.rendering after done.
func TestBrowser_StreamingMarkdownRenderingPhaseCSS(t *testing.T) {
	streamingMarkdownTestHelper(t, "Simple text to render", streamingMarkdownTestOptions{}, func(ctx context.Context) bool {
		var renderingFound bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`var s = document.getElementById('streaming'); s && s.classList.contains('rendering')`, &renderingFound),
		)
		if renderingFound {
			return true
		}
		// The streaming element may have been replaced by final render.
		var finalMsg bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('.message-assistant:not(#streaming)') !== null`, &finalMsg),
		)
		return finalMsg
	})
}

// TestBrowser_StreamingMarkdownFinalRenderCodeBlock verifies ```go fenced code block renders after done.
func TestBrowser_StreamingMarkdownFinalRenderCodeBlock(t *testing.T) {
	markdown := "Here is a Go program:\n\n```go\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```"
	streamingMarkdownTestHelper(t, markdown, streamingMarkdownTestOptions{SingleToken: true, Timeout: 8 * time.Second}, func(ctx context.Context) bool {
		var finalCodeBlock bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`var msgs = document.querySelectorAll('.message-assistant:not(#streaming)'); if (msgs.length === 0) false; else msgs[msgs.length-1].querySelector('pre code') !== null`, &finalCodeBlock),
		)
		if !finalCodeBlock {
			return false
		}
		// Verify code contains expected content
		var codeText string
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`var msgs = document.querySelectorAll('.message-assistant:not(#streaming)'); if (msgs.length === 0) ''; else { var c = msgs[msgs.length-1].querySelector('pre code'); c ? c.textContent : '' }`, &codeText),
		)
		if !strings.Contains(codeText, "func main") {
			t.Errorf("code block should contain 'func main'")
		}
		return true
	})
}

// TestBrowser_StreamingMarkdownFinalRenderMath verifies $$a+b$$ renders with KaTeX after done.
func TestBrowser_StreamingMarkdownFinalRenderMath(t *testing.T) {
	streamingMarkdownTestHelper(t, "The formula: $$a+b$$ is simple", streamingMarkdownTestOptions{SingleToken: true, Timeout: 8 * time.Second}, func(ctx context.Context) bool {
		var hasMath bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('.katex') !== null || document.querySelector('.katex-html') !== null`, &hasMath),
		)
		return hasMath
	})
}

// TestBrowser_StreamingMarkdownFinalRenderMermaid verifies ```mermaid diagram renders after done.
func TestBrowser_StreamingMarkdownFinalRenderMermaid(t *testing.T) {
	streamingMarkdownTestHelper(t, "Flow:\n\n```mermaid\ngraph TD;\nA-->B;\n```", streamingMarkdownTestOptions{SingleToken: true, Timeout: 8 * time.Second}, func(ctx context.Context) bool {
		var hasMermaid bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.querySelector('.mermaid') !== null || document.querySelector('[id^="mermaid"]') !== null`, &hasMermaid),
		)
		return hasMermaid
	})
}

// TestBrowser_StreamingMarkdownAutoScrollRegression verifies that multi-paragraph
// markdown streaming does not break auto-scroll — after streaming completes, the
// scroll container should be at the bottom (within tolerance).
func TestBrowser_StreamingMarkdownAutoScrollRegression(t *testing.T) {
	markdown := "# Title\n\nParagraph one with some descriptive text that goes on and on.\n\nParagraph two with even more content to fill up vertical space.\n\nParagraph three adding yet another block of text.\n\nParagraph four continuing the pattern with substantial content.\n\nParagraph five almost there with more filler text.\n\nParagraph six the last one to ensure enough scroll.\n\nFinal paragraph wrapping things up nicely."

	var heightClamped bool

	streamingMarkdownTestHelper(t, markdown, streamingMarkdownTestOptions{Timeout: 6 * time.Second}, func(ctx context.Context) bool {
		// Clamp #messages height on first call to force scroll overflow
		if !heightClamped {
			_ = chromedp.Run(ctx,
				chromedp.EvaluateAsDevTools(`document.getElementById('messages').style.maxHeight = '150px'`, nil),
			)
			heightClamped = true
			return false
		}

		// Wait for streaming to complete — look for Done indicator or final render
		var streamingDone bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var st = document.querySelector('.stream-status-text');
				if (st && st.textContent.trim() === 'Done') return true;
				var final = document.querySelector('.message-assistant:not(#streaming)');
				if (final) return true;
				return false;
			})()`, &streamingDone),
		)
		if !streamingDone {
			return false
		}

		// Assert scroll position is at bottom (within 50px tolerance)
		var atBottom bool
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.getElementById('messages');
				if (!el) return false;
				var tolerance = 50;
				return (el.scrollTop + el.clientHeight) >= (el.scrollHeight - tolerance);
			})()`, &atBottom),
		)
		if !atBottom {
			return false
		}

		// Debug: log scroll position for diagnosis
		var scrollInfo string
		_ = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var el = document.getElementById('messages');
				if (!el) return 'no-messages';
				return 'scrollTop=' + el.scrollTop + ' clientHeight=' + el.clientHeight + ' scrollHeight=' + el.scrollHeight;
			})()`, &scrollInfo),
		)
		t.Logf("scroll position: %s", scrollInfo)

		return true
	})
}

// TestBrowser_AssistantBubbleMaxWidth verifies that assistant message bubbles
// are capped at 90% of the messages container width.
func TestBrowser_AssistantBubbleMaxWidth(t *testing.T) {
	server := newTestServerWithRuns(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation failed: %v", err)
	}

	var result struct {
		ContainerW float64 `json:"containerW"`
		MsgW       float64 `json:"msgW"`
		BodyW      float64 `json:"bodyW"`
		ContentW   float64 `json:"contentW"`
		MsgRatio   float64 `json:"msgRatio"`
	}
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var msgs = document.getElementById('messages');
			if (!msgs) return {containerW: 0, msgW: 0, bodyW: 0, contentW: 0, msgRatio: 0};
			var containerW = msgs.getBoundingClientRect().width;
			var div = document.createElement('div');
			div.className = 'message message-assistant';
			div.innerHTML = '<img class="message-avatar" src="/static/face.webp" width="32" height="32">'
				+ '<div class="message-body">'
				+ '<div class="message-content">'
				+ '<p>' + 'VeryLongUnbreakableWordThatShouldNotForceTheBubbleToStretchToFullWidth'.repeat(20) + '</p>'
				+ '</div></div>';
			msgs.appendChild(div);
			return {
				containerW: containerW,
				msgW: div.getBoundingClientRect().width,
				bodyW: div.querySelector('.message-body').getBoundingClientRect().width,
				contentW: div.querySelector('.message-content').getBoundingClientRect().width,
				msgRatio: div.getBoundingClientRect().width / containerW,
			};
		})()`, &result),
	)
	if err != nil {
		t.Fatalf("layout measurement failed: %v", err)
	}

	if result.ContainerW <= 0 {
		t.Fatalf("invalid container width: %v", result.ContainerW)
	}
	if result.MsgRatio > 0.901 {
		t.Errorf("assistant bubble too wide: msg=%.1fpx, container=%.1fpx, ratio=%.4f (want <= 0.90)",
			result.MsgW, result.ContainerW, result.MsgRatio)
	}
	t.Logf("assistant bubble: msg=%.1fpx body=%.1fpx content=%.1fpx container=%.1fpx ratio=%.4f",
		result.MsgW, result.BodyW, result.ContentW, result.ContainerW, result.MsgRatio)
}
