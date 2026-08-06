//go:build e2e

package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// installFakeEventSourceAndConnect swaps window.EventSource for a controllable
// fake, wires it to the session's stream via eitri:connectRunStream, and
// returns. The fake is reachable as window.__fakeEventSource and supports
// emitOpen / emitMessage(packet) / emitError.
func installFakeEventSourceAndConnect(t *testing.T, ctx context.Context, sessionID string) {
	t.Helper()
	err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			class FakeEventSource {
				constructor(url) { this.url = url; window.__fakeEventSource = this; }
				close() { this.closed = true; }
				emitOpen() { if (this.onopen) this.onopen({}); }
				emitMessage(packet) { if (this.onmessage) this.onmessage({ data: JSON.stringify(packet) }); }
				emitError() { if (this.onerror) this.onerror(new Event('error')); }
			}
			window.EventSource = FakeEventSource;
			document.dispatchEvent(new CustomEvent('eitri:connectRunStream', { detail: { value: '`+sessionID+`' } }));
			return !!window.__fakeEventSource;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install fake EventSource failed: %v", err)
	}
}

func pollRunStatus(t *testing.T, ctx context.Context, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var status string
		err := chromedp.Run(ctx,
			chromedp.Text(".stream-status-text", &status, chromedp.ByQuery),
		)
		if err == nil && strings.TrimSpace(status) == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run status never reached %q within deadline", want)
}

func pollToolCardTimerCount(t *testing.T, ctx context.Context, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		err := chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`window.__activeToolCardTimerKeys().length`, &count),
		)
		if err == nil && count == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var count int
	_ = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__activeToolCardTimerKeys().length`, &count),
	)
	t.Fatalf("active tool card timer count = %d, want %d", count, want)
}

// TestBrowser_RunFinalizesOnceOnDuplicateDone verifies a second 'done' packet
// within the retention window (duplicate/replay) is ignored: the run
// finalizes (markdown render POST + RENDERING→DONE) exactly once per run,
// guarded on run status (issue #1070).
func TestBrowser_RunFinalizesOnceOnDuplicateDone(t *testing.T) {
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

	// Disconnect the stale auto-connect EventSource before installing the fake.
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.disconnectAll && window.disconnectAll()`, nil),
	); err != nil {
		t.Fatalf("disconnect stale stream failed: %v", err)
	}

	// Count markdown render POSTs (finalizeMessage) — a duplicate 'done' must
	// not trigger a second one.
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			window.__markdownRenderCalls = 0;
			var origAjax = window.htmx.ajax;
			window.htmx.ajax = function(verb, path, opts) {
				if (verb === 'POST' && path && path.indexOf('/render') !== -1 &&
				    opts && opts.values && opts.values.kind === 'markdown') {
					window.__markdownRenderCalls++;
				}
				return origAjax.apply(this, arguments);
			};
			return true;
		})()`, nil),
	)
	if err != nil {
		t.Fatalf("install markdown render counter failed: %v", err)
	}

	installFakeEventSourceAndConnect(t, ctx, sessionID)

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'done', message_id: 'msg_final'})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'done', message_id: 'msg_final'})`, nil),
	); err != nil {
		t.Fatalf("emit duplicate done packets failed: %v", err)
	}

	// The second done is emitted while the first is still finalizing
	// (RENDERING) or already finalized (DONE) — both must be ignored.
	pollRunStatus(t, ctx, "Done")

	var renderCalls int
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__markdownRenderCalls`, &renderCalls),
	)
	if err != nil {
		t.Fatalf("read markdown render count failed: %v", err)
	}
	if renderCalls != 1 {
		t.Fatalf("markdown render calls = %d, want 1 (duplicate/replayed done must not re-finalize)", renderCalls)
	}
}

// TestBrowser_ToolCardTimerStopsWhenCardRemoved verifies a tool card's live
// elapsed interval is stopped when its card is removed from the DOM — not only
// on tool_result or FIFO eviction (issue #1070).
func TestBrowser_ToolCardTimerStopsWhenCardRemoved(t *testing.T) {
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

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.disconnectAll && window.disconnectAll()`, nil),
	); err != nil {
		t.Fatalf("disconnect stale stream failed: %v", err)
	}

	installFakeEventSourceAndConnect(t, ctx, sessionID)

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: 'echo hello'}})`, nil),
	); err != nil {
		t.Fatalf("emit tool_call failed: %v", err)
	}

	// Card appears with a live elapsed timer.
	pollToolCardTimerCount(t, ctx, 1)

	// Wait for the first interval tick so the span actually carries live text.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var first string
		if err := chromedp.Run(ctx,
			chromedp.Text(`#tool-activity [data-tool-elapsed]`, &first, chromedp.ByQuery),
		); err == nil && strings.HasPrefix(first, "\u2191") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Sanity: the live timer actually updates the elapsed span.
	var elapsedBefore, elapsedAfter string
	if err := chromedp.Run(ctx,
		chromedp.Text(`#tool-activity [data-tool-elapsed]`, &elapsedBefore, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read elapsed before failed: %v", err)
	}
	// Deliberate pacing (live-timer assertion): let the ticking timer elapse so
	// the test can verify the elapsed span updates over wall-clock time.
	time.Sleep(300 * time.Millisecond)
	if err := chromedp.Run(ctx,
		chromedp.Text(`#tool-activity [data-tool-elapsed]`, &elapsedAfter, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("read elapsed after failed: %v", err)
	}
	if !strings.HasPrefix(elapsedBefore, "\u2191") || !strings.HasPrefix(elapsedAfter, "\u2191") {
		t.Fatalf("elapsed span should show a live ↑ timer, before=%q after=%q", elapsedBefore, elapsedAfter)
	}

	// Remove the card from the DOM by hand (no tool_result, no FIFO eviction).
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var card = document.querySelector('#tool-activity .tool-entry-wrapper');
			if (!card) return false;
			card.remove();
			return true;
		})()`, nil),
	); err != nil {
		t.Fatalf("remove tool card failed: %v", err)
	}

	// The interval must stop itself now that its card is gone.
	pollToolCardTimerCount(t, ctx, 0)
}

// TestBrowser_ToolStateSurvivesReconnect verifies a transient EventSource
// error during an active run does NOT clear tool activity/elapsed data: the
// tool card (same key), its live timer, and its elapsed span all survive the
// reconnect, and the replayed history resumes into the same card instead of
// creating a duplicate (issue #1070).
func TestBrowser_ToolStateSurvivesReconnect(t *testing.T) {
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

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.disconnectAll && window.disconnectAll()`, nil),
	); err != nil {
		t.Fatalf("disconnect stale stream failed: %v", err)
	}

	installFakeEventSourceAndConnect(t, ctx, sessionID)

	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'token', content: 'hello'})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', turn: 1, args: {command: 'echo hello'}})`, nil),
	); err != nil {
		t.Fatalf("emit token/tool_call failed: %v", err)
	}

	// Card appears with a live timer; record its replay-stable key.
	var cardKey string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err = chromedp.Run(ctx,
			chromedp.EvaluateAsDevTools(`(function() {
				var card = document.querySelector('#tool-activity .tool-entry-wrapper');
				if (!card) return '';
				return card.getAttribute('data-tool-key');
			})()`, &cardKey),
		)
		if err == nil && cardKey != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if cardKey == "" {
		t.Fatalf("tool card did not appear: err=%v", err)
	}
	pollToolCardTimerCount(t, ctx, 1)

	// Transient error: status → Reconnecting, but the card/timer must survive.
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitError()`, nil),
	); err != nil {
		t.Fatalf("emit error failed: %v", err)
	}
	pollRunStatus(t, ctx, "Reconnecting")

	var cardStillThere bool
	var survivingKey string
	var timerStillActive int
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('#tool-activity .tool-entry-wrapper') !== null`, &cardStillThere),
		chromedp.EvaluateAsDevTools(`(function() {
			var card = document.querySelector('#tool-activity .tool-entry-wrapper');
			return card ? card.getAttribute('data-tool-key') : '';
		})()`, &survivingKey),
		chromedp.EvaluateAsDevTools(`window.__activeToolCardTimerKeys().length`, &timerStillActive),
	)
	if err != nil {
		t.Fatalf("read post-error tool state failed: %v", err)
	}
	if !cardStillThere {
		t.Fatal("tool card must survive a transient EventSource error")
	}
	if survivingKey != cardKey {
		t.Fatalf("tool card key after error = %q, want %q (same card)", survivingKey, cardKey)
	}
	if timerStillActive != 1 {
		t.Fatalf("tool card timer after error = %d active, want 1 (elapsed tracking must survive)", timerStillActive)
	}

	// Reconnect: server replays the retention window. The replayed tool_call
	// must resolve to the SAME card (no duplicate) and the tool_result resumes
	// it, then the run finalizes normally.
	if err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitOpen()`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', turn: 1, args: {command: 'echo hello'}})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'token', content: 'hello'})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_result', tool: 'terminal_execute', turn: 1, output: 'hello\n'})`, nil),
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'done', message_id: 'msg_final'})`, nil),
	); err != nil {
		t.Fatalf("emit reconnect replay failed: %v", err)
	}

	pollRunStatus(t, ctx, "Done")

	// Exactly one card, same key, finished with a recorded elapsed.
	var cardCount int
	var finalKey string
	var statusLabel string
	var elapsedText string
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelectorAll('#tool-activity .tool-entry-wrapper').length`, &cardCount),
		chromedp.EvaluateAsDevTools(`(function() {
			var card = document.querySelector('#tool-activity .tool-entry-wrapper');
			return card ? card.getAttribute('data-tool-key') : '';
		})()`, &finalKey),
		chromedp.EvaluateAsDevTools(`(function() {
			var label = document.querySelector('#tool-activity .tool-status-label');
			return label ? label.textContent : '';
		})()`, &statusLabel),
		chromedp.EvaluateAsDevTools(`(function() {
			var el = document.querySelector('#tool-activity [data-tool-elapsed]');
			return el ? el.textContent : '';
		})()`, &elapsedText),
	)
	if err != nil {
		t.Fatalf("read final tool card state failed: %v", err)
	}
	if cardCount != 1 {
		t.Fatalf("tool card count after reconnect replay = %d, want 1 (no duplicate cards)", cardCount)
	}
	if finalKey != cardKey {
		t.Fatalf("final tool card key = %q, want original %q", finalKey, cardKey)
	}
	if statusLabel != "done" {
		t.Fatalf("tool card status after replay = %q, want done", statusLabel)
	}
	if !strings.HasPrefix(elapsedText, "\u2191") {
		t.Fatalf("tool card elapsed after replay = %q, want a recorded ↑ elapsed", elapsedText)
	}
}
