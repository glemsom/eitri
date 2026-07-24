package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// ————— Session tests ————— —

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

	const secondMessage = "second renames tab"
	const secondExpectedTitle = "second renames tab"

	err = chromedp.Run(ctx,
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(function() {
			var input = document.querySelector('#chat-input');
			if (!input) return false;
			input.value = '';
			input.dispatchEvent(new Event('input', { bubbles: true }));
			return true;
		})()`, nil),
		chromedp.SendKeys("#chat-input", secondMessage, chromedp.ByQuery),
		chromedp.Click("#send-btn", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("send second message failed: %v", err)
	}

	for i := 0; i < 20; i++ {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`Array.from(document.querySelectorAll('#session-tabs .session-title')).map(el => el.textContent.trim())`, &titles),
		)
		if err != nil {
			t.Fatalf("read session titles after second send failed: %v", err)
		}
		if len(titles) == 2 && titles[0] == secondExpectedTitle && titles[1] == "Session 2" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(titles) != 2 || titles[0] != secondExpectedTitle || titles[1] != "Session 2" {
		t.Fatalf("session titles after second send = %v, want [%s Session 2]", titles, secondExpectedTitle)
	}
}

// ————— Context panel tests ————— —

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
	// Click Context header to reveal idle message (hidden by default)
	err = chromedp.Run(ctx,
		chromedp.Click("#context-panel .sidebar-header", chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("click header failed: %v", err)
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

	// Click header again to hide idle
	err = chromedp.Run(ctx,
		chromedp.Click("#context-panel .sidebar-header", chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)

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

	// Verify per-category mini bars exist and have correct widths
	var barCount int
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var bars = document.querySelectorAll('eitri-context .context-category-bar-fill');
			return bars.length;
		})()`, &barCount),
	)
	if err != nil {
		t.Fatalf("category bar count check failed: %v", err)
	}
	if barCount != 5 {
		t.Errorf("category bar-fill count = %d, want 5 (one per category)", barCount)
	}

	// Verify each bar has fill-green class (all < 60%)
	var barClassesList string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var bars = document.querySelectorAll('eitri-context .context-category-bar-fill');
			var result = [];
			bars.forEach(function(b) { result.push(b.className); });
			return result.join('|');
		})()`, &barClassesList),
	)
	if err != nil {
		t.Fatalf("category bar classes check failed: %v", err)
	}
	barParts := strings.Split(barClassesList, "|")
	for i, cls := range barParts {
		if !strings.Contains(cls, "fill-green") {
			t.Errorf("category bar %d class = %q, want fill-green (low pct)", i, cls)
		}
	}

	// Verify total bar is separate (compact) and category bars are in expanded
	var categoryBarsVisible string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var bars = document.querySelectorAll('eitri-context .context-expanded.open .context-category-bar-fill');
			if (bars.length === 0) return 'not-found';
			return 'visible';
		})()`, &categoryBarsVisible),
	)
	if err != nil {
		t.Fatalf("expanded category bars check failed: %v", err)
	}
	if categoryBarsVisible != "visible" {
		t.Errorf("category bars visible = %q, want 'visible' in expanded.open", categoryBarsVisible)
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
	if idleDisplay != "none" && idleDisplay != "not-found" {
		t.Errorf("idle display after reset = %q, expected hidden (none)", idleDisplay)
	}
}

// TestBrowser_ContextPanel_SessionSwitch verifies that context panel data
// survives session switches via full page navigation.
// Regression test for issue #363 (persist across session switches).
func TestBrowser_ContextPanel_SessionSwitch(t *testing.T) {
	h := newManagedTestServerWithRuns(t)
	server := h.server

	// Use the server's session manager to create two sessions
	sess1, err := h.sessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}
	sess2, err := h.sessionMgr.Create("browser-1")
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	// Step 1: Navigate to session 1
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			return network.SetCookie("browser_id", "browser-1").WithURL(server.URL).Do(ctx)
		}),
		chromedp.Navigate(server.URL+"/sessions/"+sess1.ID),
		chromedp.WaitVisible("eitri-context", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate to sess1 failed: %v", err)
	}
	// Step 2: Click Context header to reveal idle message
	err = chromedp.Run(ctx,
		chromedp.Click("#context-panel .sidebar-header", chromedp.ByQuery),
		chromedp.Sleep(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("click header failed: %v", err)
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
		t.Fatalf("expected idle state, got %q", idleText)
	}

	// Re-hide idle before dispatch
	err = chromedp.Run(ctx,
		chromedp.Click("#context-panel .sidebar-header", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("click header to hide failed: %v", err)
	}

	// Step 3: Dispatch a context_update via JS (simulating what SSE does)
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
	if err != nil {
		t.Fatalf("dispatch context_update failed: %v", err)
	}

	// Step 4: Verify compact view is visible with correct data
	var statsText string
	err = chromedp.Run(ctx,
		chromedp.WaitVisible("eitri-context .context-compact", chromedp.ByQuery),
		chromedp.Text("eitri-context .context-stats", &statsText, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("stats text check after dispatch failed: %v", err)
	}
	if !strings.Contains(statsText, "12,847 / 128K") {
		t.Fatalf("expected stats containing '12,847 / 128K', got %q", statsText)
	}

	// Step 5: Navigate to session 2 (full page load)
	err = chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/sessions/"+sess2.ID),
		chromedp.WaitVisible("eitri-context", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate to sess2 failed: %v", err)
	}

	// Step 6: Navigate BACK to session 1 (full page load) and verify re-hydration
	err = chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/sessions/"+sess1.ID),
		chromedp.WaitVisible("eitri-context", chromedp.ByQuery),
		// Wait for debounced render (100ms) + safety margin
		chromedp.Sleep(500*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("navigate back to sess1 failed: %v", err)
	}

	// Verify context data was re-hydrated from sessionStorage (not "No active run")
	var statsTextAfterSwitch string
	err = chromedp.Run(ctx,
		chromedp.WaitVisible("eitri-context .context-compact", chromedp.ByQuery),
		chromedp.Text("eitri-context .context-stats", &statsTextAfterSwitch, chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("stats text check after switch-back failed: %v", err)
	}
	if !strings.Contains(statsTextAfterSwitch, "12,847 / 128K") {
		t.Fatalf("after switch-back stats = %q, want '12,847 / 128K' — re-hydration failed", statsTextAfterSwitch)
	}
}
