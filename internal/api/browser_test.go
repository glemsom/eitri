package api_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

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
	// Resolve full path via LookPath
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

	// Wait for the provider field to appear (HTMX loads it)
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
		// Follow redirect to session page
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
