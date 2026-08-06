//go:build e2e

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// listenerCounts is the set of document/body-level listener counts that must
// stay stable while the page keeps re-creating sidebar elements.
type listenerCounts struct {
	docClick         int // persona selector: one delegated document click listener (was one per re-created element)
	docKeydown       int // composer: one document keydown while connected (released on disconnect)
	bodyAfterRequest int // context panel: one document.body htmx:afterRequest (released on disconnect)
}

// readListenerCounts inspects document/body listeners through the DevTools
// command-line API (getEventListeners) — the browser equivalent of inspecting
// listeners in the Elements panel.
func readListenerCounts(t *testing.T, ctx context.Context) listenerCounts {
	t.Helper()
	var raw struct {
		DocClick         int `json:"docClick"`
		DocKeydown       int `json:"docKeydown"`
		BodyAfterRequest int `json:"bodyAfterRequest"`
	}
	err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			function count(target, type) {
				var all = getEventListeners(target);
				return (all[type] || []).length;
			}
			return {
				docClick: count(document, 'click'),
				docKeydown: count(document, 'keydown'),
				bodyAfterRequest: count(document.body, 'htmx:afterRequest'),
			};
		})()`, &raw),
	)
	if err != nil {
		t.Fatalf("read listener counts failed: %v", err)
	}
	return listenerCounts{
		docClick:         raw.DocClick,
		docKeydown:       raw.DocKeydown,
		bodyAfterRequest: raw.BodyAfterRequest,
	}
}

// TestBrowser_ListenerCountStableAcrossSwaps verifies that re-creating the
// sidebar custom elements in-place does not grow document/body listener
// counts (issue #1069, acceptance criterion 4). Activating a persona from the
// header selector performs an htmx POST whose response swaps
// `#persona-selector` via outerHTML, so each activation creates a fresh
// persona-selector element without a page load — the exact leak scenario the
// issue describes. Before the fix every re-created selector registered its own
// permanent document click listener; now there is exactly one delegated
// listener, and the composer/context-panel document listeners stay balanced by
// their disconnectedCallback teardown.
func TestBrowser_ListenerCountStableAcrossSwaps(t *testing.T) {
	llmURL := testLLMURL(t)
	server := newTestServerWithRuns(t)
	configureProvider(t, server, llmURL)

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.WaitVisible("eitri-context", chromedp.ByQuery),
		chromedp.WaitVisible("#persona-selector", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate to chat failed: %v", err)
	}
	waitForComposerReady(t, ctx)

	before := readListenerCounts(t, ctx)

	// Activate the (only) persona several times; each activation htmx-swaps
	// #persona-selector for a fresh element.
	for i := 0; i < 3; i++ {
		err = chromedp.Run(ctx,
			chromedp.Click("#persona-selector [data-ps-target=trigger]", chromedp.ByQuery),
			chromedp.Sleep(100*time.Millisecond),
			chromedp.Click("#persona-selector .persona-option", chromedp.ByQuery),
			chromedp.WaitVisible("#persona-selector .persona-trigger-label", chromedp.ByQuery),
		)
		if err != nil {
			t.Fatalf("activate persona (round %d) failed: %v", i+1, err)
		}
		// Let the outerHTML swap and the afterSwap re-init settle.
		time.Sleep(300 * time.Millisecond)
	}

	after := readListenerCounts(t, ctx)

	if after.docClick != before.docClick {
		t.Errorf("document click listener count grew from %d to %d across persona-swap re-renders — persona selector is leaking listeners", before.docClick, after.docClick)
	}
	if after.docKeydown != before.docKeydown {
		t.Errorf("document keydown listener count changed from %d to %d — composer listeners are not balanced on disconnect", before.docKeydown, after.docKeydown)
	}
	if after.bodyAfterRequest != before.bodyAfterRequest {
		t.Errorf("document.body htmx:afterRequest listener count changed from %d to %d — context panel is leaking listeners", before.bodyAfterRequest, after.bodyAfterRequest)
	}
}
