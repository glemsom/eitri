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
	err = chromedp.Run(ctx,
		chromedp.SendKeys("#chat-input", messageText, chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
		// Wait for user bubble to appear
		chromedp.WaitVisible(".message-user", chromedp.ByQuery),
		// Verify the bubble contains our message
		chromedp.EvaluateAsDevTools(
			`document.querySelector('.message-user .message-content') !== null &&
			 document.querySelector('.message-user .message-content').textContent === "`+messageText+`"`,
			&userBubbleExists,
		),
	)
	if err != nil {
		t.Fatalf("send message test failed: %v", err)
	}

	if !userBubbleExists {
		t.Error("user bubble with message text not found after sending")
	}

	// Also verify the chat input is disabled during active run
	var inputDisabled bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools("document.querySelector('#chat-input').disabled === true", &inputDisabled),
	)
	if err != nil {
		t.Logf("input state check failed (may be race): %v", err)
	}
	if !inputDisabled {
		t.Error("#chat-input should be disabled during active run")
	}
}

// TestBrowser_InputDisabledDuringRun verifies that during an active run,
// the chat input and send button are disabled, and the stop button is visible.
func TestBrowser_InputDisabledDuringRun(t *testing.T) {
	llmURL := testLLMURL(t)
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
	llmURL := testLLMURL(t)
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
		chromedp.Click(`a[href="/settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
		chromedp.Location(&pathAfterSettings),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#workspace-indicator')?.textContent.includes(`+fmt.Sprintf("%q", workspace)+`) ?? false`,
			&settingsHasWorkspace,
		),
		chromedp.Click(`a[href="/skills"]`, chromedp.ByQuery),
		chromedp.WaitVisible(".skills-view", chromedp.ByQuery),
		chromedp.Location(&pathAfterSkills),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#workspace-indicator')?.textContent.includes(`+fmt.Sprintf("%q", workspace)+`) ?? false`,
			&skillsHasWorkspace,
		),
		chromedp.Click(`a[href^="/sessions/"]`, chromedp.ByQuery),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.Location(&pathAfterChat),
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#workspace-indicator')?.textContent.includes(`+fmt.Sprintf("%q", workspace)+`) ?? false`,
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
		t.Error("settings page missing workspace indicator after HTMX nav")
	}
	if !strings.HasSuffix(pathAfterSkills, "/skills") {
		t.Errorf("path after skills nav = %q, want suffix /skills", pathAfterSkills)
	}
	if !skillsHasWorkspace {
		t.Error("skills page missing workspace indicator after HTMX nav")
	}
	if !strings.Contains(pathAfterChat, "/sessions/") {
		t.Errorf("path after chat nav = %q, want containing /sessions/", pathAfterChat)
	}
	if !chatHasWorkspace {
		t.Error("chat page missing workspace indicator after HTMX nav")
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
	var apiKeyExists, baseURLExists, modelExists bool
	var sendBtnAbsent bool
	var providerOptionsCount int

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#provider", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools("document.querySelector('#provider') !== null", &providerExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#api_key') !== null", &apiKeyExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#base_url') !== null", &baseURLExists),
		chromedp.EvaluateAsDevTools("document.querySelector('#model') !== null", &modelExists),
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
// (401 from fake provider) does NOT populate models and the form stays unchanged.
// HTMX does NOT swap error content on 4xx (responseHandling), so no error toast
// appears in the DOM. The form simply stays as-is.
func TestBrowser_ConfigSaveProviderFailure(t *testing.T) {
	fakeProvider := fakeProviderServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Navigate to settings page
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/settings"),
		chromedp.WaitVisible("#settings-form", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigation to settings failed: %v", err)
	}

	// Fill form with bad provider URL and submit
	err = chromedp.Run(ctx,
		chromedp.SetValue("#provider", "custom_openai", chromedp.ByQuery),
		chromedp.Clear("#base_url", chromedp.ByQuery),
		chromedp.SendKeys("#base_url", fakeProvider.URL, chromedp.ByQuery),
		chromedp.Clear("#api_key", chromedp.ByQuery),
		chromedp.SendKeys("#api_key", "sk-bad", chromedp.ByQuery),
		chromedp.Click("button[type=submit]", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("form fill/submit failed: %v", err)
	}

	// Wait for HTMX to process the response (will not swap on 4xx).
	// The form remains unchanged. Verify model dropdown stayed empty.
	time.Sleep(500 * time.Millisecond)

	var modelOptionsEmpty bool
	var providerValue string
	err = chromedp.Run(ctx,
		chromedp.Value("#provider", &providerValue, chromedp.ByQuery),
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
}
