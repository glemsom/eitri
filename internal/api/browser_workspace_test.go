package api_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// ————— Workspace indicator tests ————— —

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

// ————— Page layout and navigation tests ————— —

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
		chromedp.EvaluateAsDevTools("document.querySelector('.stream-status-text') !== null", &headerStreamIndicatorExists),
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
		t.Error(".stream-status-text in header not found")
	}
	if chatViewDisplay != "grid" {
		t.Errorf("#chat-view display = %q, want 'grid'", chatViewDisplay)
	}
	if chatViewGridRows == "" || chatViewGridRows == "none" {
		t.Errorf("#chat-view grid-template-rows = %q, expected explicit rows (auto 1fr auto)", chatViewGridRows)
	}
	if messagesOverflowY != "auto" {
		t.Errorf("#messages overflow-y = %q, want 'auto'", messagesOverflowY)
	}
	if messagesDisplay != "flex" {
		t.Errorf("#messages display = %q, want 'flex' (grid-area messages keeps its flex column layout)", messagesDisplay)
	}
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
	if dropdownDisplay != "none" {
		t.Errorf(".dropdown-content display = %q, want 'none'", dropdownDisplay)
	}
}

// TestBrowser_NavUsesHTMXBetweenFullPages verifies HTMX navigation between pages.
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

// ————— Setup banner / header tests ————— —

// TestBrowser_SetupBannerVisible verifies that the setup banner appears
// when no provider config exists, chat input is disabled, and the banner
// links to /settings.
func TestBrowser_SetupBannerVisible(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var bannerVisible bool
	var sendBtnDisabled bool
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
		chromedp.EvaluateAsDevTools("document.querySelector('#send-btn').disabled === true", &sendBtnDisabled),
		chromedp.OuterHTML("#setup-banner", &bannerHTML, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("setup banner test failed: %v", err)
	}

	if !bannerVisible {
		t.Error("#setup-banner should be visible when no config")
	}
	if !sendBtnDisabled {
		t.Error("#send-btn should be disabled when no config")
	}
	if !strings.Contains(bannerHTML, "/settings") {
		t.Error("setup banner should link to /settings")
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
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('.stream-status-text').textContent`, &streamText),
	)
	if err != nil {
		t.Fatalf("stream indicator test failed: %v", err)
	}
	if strings.TrimSpace(streamText) == "" {
		t.Error(".stream-status-text has no text content")
	}
}

// TestBrowser_FaceBreathingAnimation verifies the face image gets the breathe animation
// during streaming and tool-running states (issue #449).
func TestBrowser_FaceBreathingAnimation(t *testing.T) {
	server := newTestServer(t)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var animName string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible(".header-face", chromedp.ByQuery),
		// Set streaming status to trigger breathing animation
		chromedp.EvaluateAsDevTools(`(function() {
			var container = document.querySelector('.header-face-container');
			if (container) {
				container.setAttribute('data-stream-status', 'streaming');
				return 'set';
			}
			return 'not-found';
		})()`, nil),
		// Read computed animation-name from the face img
		chromedp.EvaluateAsDevTools(`(function() {
			var face = document.querySelector('.header-face');
			if (!face) return '';
			return window.getComputedStyle(face).animationName;
		})()`, &animName),
	)
	if err != nil {
		t.Fatalf("test failed: %v", err)
	}
	if !strings.Contains(animName, "breathe") {
		t.Errorf("face animation-name = %q, want it to contain 'breathe'", animName)
	}

	// Verify idle state has no breathing animation
	var idleAnimName string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var container = document.querySelector('.header-face-container');
			if (container) {
				container.setAttribute('data-stream-status', 'idle');
			}
		})()`, nil),
		chromedp.EvaluateAsDevTools(`(function() {
			var face = document.querySelector('.header-face');
			if (!face) return '';
			return window.getComputedStyle(face).animationName;
		})()`, &idleAnimName),
	)
	if err != nil {
		t.Fatalf("idle check failed: %v", err)
	}
	if strings.Contains(idleAnimName, "breathe") {
		t.Errorf("face animation-name = %q, should NOT contain 'breathe' when idle", idleAnimName)
	}
}

// ————— Health / canary / infrastructure tests ————— —

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
