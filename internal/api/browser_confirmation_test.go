package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// ————— Confirmation dialog tests ————— —

func TestBrowser_ConfirmationDenyShowsUndoToast(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var sessionID string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	// Get session ID from URL
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`location.pathname.split('/').pop()`, &sessionID),
	)
	if err != nil || sessionID == "" {
		t.Fatalf("get session ID failed: %v", err)
	}

	// Install fake EventSource and connect to trigger needs_confirmation
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

	// Emit open then needs_confirmation event
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/passwd', message: 'This path requires confirmation'}})`, nil),
	); err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	// Verify confirmation overlay appeared
	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("confirmation overlay not visible after needs_confirmation event")
	}

	// Verify Deny button exists
	var denyVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirm-deny') !== null`, &denyVisible),
	)
	if err != nil || !denyVisible {
		t.Fatalf("confirm-deny button not found")
	}

	// Click Deny — should trigger undo toast
	if err := chromedp.Run(ctx,
		chromedp.Click("#confirm-deny", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click Deny failed: %v", err)
	}

	// Allow a brief moment for the undo toast to render
	time.Sleep(200 * time.Millisecond)

	// Verify undo toast is visible, not the original modal buttons
	var undoToastVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var toast = document.querySelector('.undo-toast');
			return toast !== null && toast.offsetParent !== null;
		})()`, &undoToastVisible),
	)
	if err != nil || !undoToastVisible {
		t.Fatalf("undo toast not visible after Deny click")
	}

	// Verify progress bar exists inside undo toast
	var progressBarExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.undo-toast .undo-toast-bar') !== null`, &progressBarExists),
	)
	if err != nil || !progressBarExists {
		t.Fatalf("undo-toast-bar not found inside undo toast")
	}

	// Verify Undo button exists
	var undoBtnExists bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.undo-toast .undo-toast-btn') !== null`, &undoBtnExists),
	)
	if err != nil || !undoBtnExists {
		t.Fatalf("undo-toast-btn not found inside undo toast")
	}

	// Click Undo button
	if err := chromedp.Run(ctx,
		chromedp.Click(".undo-toast-btn", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click Undo failed: %v", err)
	}

	// Allow brief moment for modal to close
	time.Sleep(200 * time.Millisecond)

	// Verify modal/overlay is removed
	var overlayGone bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') === null`, &overlayGone),
	)
	if err != nil || !overlayGone {
		t.Fatalf("confirmation overlay still visible after Undo click")
	}
}

func TestBrowser_ConfirmationDenyTimeoutClosesModal(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	var sessionID string
	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate chat failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`location.pathname.split('/').pop()`, &sessionID),
	)
	if err != nil || sessionID == "" {
		t.Fatalf("get session ID failed: %v", err)
	}

	// Install fake EventSource
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

	// Emit needs_confirmation
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/shadow', message: 'This path requires confirmation'}})`, nil),
	); err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	// Verify overlay appeared
	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("confirmation overlay not visible")
	}

	// Click Deny
	if err := chromedp.Run(ctx,
		chromedp.Click("#confirm-deny", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("click Deny failed: %v", err)
	}

	// Verify undo toast appears
	time.Sleep(200 * time.Millisecond)
	var toastVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.undo-toast') !== null`, &toastVisible),
	)
	if err != nil || !toastVisible {
		t.Fatalf("undo toast not visible after Deny")
	}

	// Wait for the 5-second timeout
	var overlayGone bool
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') === null`, &overlayGone),
		)
		if err == nil && overlayGone {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !overlayGone {
		t.Fatalf("confirmation overlay still visible after 5-second timeout")
	}
}

func TestBrowser_ConfirmationAutofocusDenyButton(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
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

	// Emit open + needs_confirmation
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/shadow', message: 'This path requires confirmation'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	// Wait for overlay
	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("confirmation overlay not visible")
	}

	// Verify Deny button is focused (autofocus)
	var activeElementID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.activeElement ? document.activeElement.id : ''`, &activeElementID),
	)
	if err != nil {
		t.Fatalf("check activeElement failed: %v", err)
	}
	if activeElementID != "confirm-deny" {
		t.Errorf("autofocus: expected confirm-deny to be focused, got %q", activeElementID)
	}
}

func TestBrowser_ConfirmationFocusTrapTab(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
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

	// Emit open + needs_confirmation
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/passwd', message: 'Test'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("overlay not visible")
	}

	// Deny should be focused initially; simulate Tab to move to Allow
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirm-deny').focus()`, nil),
	)
	if err != nil {
		t.Fatalf("focus deny failed: %v", err)
	}

	// Dispatch Tab on overlay
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch Tab failed: %v", err)
	}

	var activeElementID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.activeElement ? document.activeElement.id : ''`, &activeElementID),
	)
	if err != nil {
		t.Fatalf("check activeElement failed: %v", err)
	}
	if activeElementID != "confirm-allow" {
		t.Errorf("focus trap Tab: expected confirm-allow after Tab from deny, got %q", activeElementID)
	}

	// Tab again from Allow — should wrap to Deny
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch second Tab failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.activeElement ? document.activeElement.id : ''`, &activeElementID),
	)
	if err != nil {
		t.Fatalf("check activeElement failed: %v", err)
	}
	if activeElementID != "confirm-deny" {
		t.Errorf("focus trap Tab wrap: expected confirm-deny after Tab from allow, got %q", activeElementID)
	}
}

func TestBrowser_ConfirmationFocusTrapShiftTab(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
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

	// Emit open + needs_confirmation
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/passwd', message: 'Test'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("overlay not visible")
	}

	// Start with Allow focused, then Shift+Tab should wrap to Deny
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirm-allow').focus()`, nil),
	)
	if err != nil {
		t.Fatalf("focus allow failed: %v", err)
	}

	// Dispatch Shift+Tab on overlay
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch Shift+Tab failed: %v", err)
	}

	var activeElementID string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.activeElement ? document.activeElement.id : ''`, &activeElementID),
	)
	if err != nil {
		t.Fatalf("check activeElement failed: %v", err)
	}
	if activeElementID != "confirm-deny" {
		t.Errorf("focus trap Shift+Tab: expected confirm-deny after Shift+Tab from allow, got %q", activeElementID)
	}

	// Shift+Tab again from Deny — should wrap to Allow
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', shiftKey: true, bubbles: true, cancelable: true }))`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch second Shift+Tab failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.activeElement ? document.activeElement.id : ''`, &activeElementID),
	)
	if err != nil {
		t.Fatalf("check activeElement failed: %v", err)
	}
	if activeElementID != "confirm-allow" {
		t.Errorf("focus trap Shift+Tab wrap: expected confirm-allow after Shift+Tab from deny, got %q", activeElementID)
	}
}

func TestBrowser_ConfirmationEscapeTriggersDeny(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
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

	// Emit open + needs_confirmation
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/shadow', message: 'Test'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	// Wait for overlay
	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("overlay not visible")
	}

	err = chromedp.Run(ctx,
		chromedp.Sleep(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("sleep failed: %v", err)
	}

	// Dispatch Escape on overlay — should trigger Deny (undo toast appears)
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch Escape failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.Sleep(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("sleep failed: %v", err)
	}

	// Should show undo toast (Deny behavior)
	var toastVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.undo-toast') !== null`, &toastVisible),
	)
	if err != nil || !toastVisible {
		t.Errorf("Escape should trigger Deny and show undo toast")
	}
}

func TestBrowser_ConfirmationAriaLive(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
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

	// Emit open + needs_confirmation
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/passwd', message: 'Test'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("overlay not visible")
	}

	// Verify aria-live on overlay
	var ariaLive string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').getAttribute('aria-live')`, &ariaLive),
	)
	if err != nil {
		t.Fatalf("get aria-live failed: %v", err)
	}
	if ariaLive != "polite" {
		t.Errorf("expected aria-live='polite', got %q", ariaLive)
	}

	// Verify aria-live content has the title text
	var liveContent string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay').textContent`, &liveContent),
	)
	if err != nil {
		t.Fatalf("get textContent failed: %v", err)
	}
	if !strings.Contains(liveContent, "Path requires confirmation") {
		t.Errorf("aria-live area should contain title text, got %q", liveContent)
	}
}

func TestBrowser_ConfirmationKeydownRemovedOnClose(t *testing.T) {
	chrome := findChrome()
	if chrome == "" {
		t.Skip("Chrome not found, skipping browser test")
	}

	server := newTestServerWithRuns(t)
	defer server.Close()

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
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

	// Emit open + needs_confirmation
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'needs_confirmation', data: {path: '/etc/shadow', message: 'Test'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit needs_confirmation failed: %v", err)
	}

	var overlayVisible bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') !== null`, &overlayVisible),
	)
	if err != nil || !overlayVisible {
		t.Fatalf("overlay not visible")
	}

	// Close modal by clicking Allow
	err = chromedp.Run(ctx,
		chromedp.Click("#confirm-allow", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("click Allow failed: %v", err)
	}

	err = chromedp.Run(ctx,
		chromedp.Sleep(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("sleep failed: %v", err)
	}

	// Wait for overlay to disappear
	var overlayGone bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.getElementById('confirmation-overlay') === null`, &overlayGone),
	)
	if err != nil || !overlayGone {
		t.Fatalf("overlay not removed after Allow")
	}

	// After close, dispatch Escape on document — should NOT trigger anything
	var toastBefore bool
	_ = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.undo-toast') !== null`, &toastBefore),
	)

	// Dispatch Escape on document
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))`, nil),
	)
	if err != nil {
		t.Fatalf("dispatch Escape on document failed: %v", err)
	}

	var toastAfter bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.undo-toast') !== null`, &toastAfter),
	)
	if err != nil {
		t.Fatalf("check toast after failed: %v", err)
	}
	if toastAfter && !toastBefore {
		t.Errorf("keydown listener not removed: Escape on document triggered action after modal close")
	}
}
