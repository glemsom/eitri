package api_test

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// ————— Tool card / skills tests ————— —

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

	// Verify output content is stored inside <details> element
	var hasResult bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#tool-activity details.tool-entry-wrapper[open] .tool-result') !== null`,
			&hasResult,
		),
	)
	if err != nil {
		t.Fatalf("query tool result failed: %v", err)
	}

	// Verify no tool card DOM appears in #messages
	var toolCardsInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#messages .tool-entry, #messages [data-tool-key]') !== null`,
			&toolCardsInMessages,
		),
	)
	if err != nil {
		t.Fatalf("query tool cards in messages failed: %v", err)
	}
	if toolCardsInMessages {
		t.Error("tool entries should not appear in #messages after sidebar migration")
	}
}

// TestBrowser_ToolCardsInsertBeforeSentinel verifies a sidebar entry appears
// for first tool even when no streaming bubble exists yet (tools-run-first).
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

	// Emit a token to simulate first text after tools
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'token', content: 'Thinking about it...'})`, nil),
	)
	if err != nil {
		t.Fatalf("emit token failed: %v", err)
	}

	// Poll for streaming to appear before scroll-sentinel in messages
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

	// Verify no tool entries remain in #messages
	var toolEntriesInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#messages [data-tool-key]') !== null`,
			&toolEntriesInMessages,
		),
	)
	if err != nil {
		t.Fatalf("query tool entries in messages failed: %v", err)
	}
	if toolEntriesInMessages {
		t.Error("tool entries should not appear in #messages after sidebar migration")
	}
}

// TestBrowser_ToolCardMorphInPlace verifies sequential tool results update
// existing sidebar entries in-place rather than appending new DOM nodes.
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

	// Emit three sequential tool_calls with tool_results
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

// TestBrowser_ToolCardsInScrollContainer verifies sidebar tool entries appear
// within scrollable sidebar panel after a token (streaming bubble) is created.
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

	// Emit a tool_call — should create tool entry in sidebar
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`window.__fakeEventSource.emitMessage({type: 'tool_call', tool: 'terminal_execute', args: {command: 'echo hello'}})`, nil),
	)
	if err != nil {
		t.Fatalf("emit tool_call failed: %v", err)
	}

	// Wait for tool entry to appear
	time.Sleep(500 * time.Millisecond)

	// Verify tool entry exists in sidebar tool-activity-list
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

	// Verify streaming bubble still exists (tool entry sidebar, not messages)
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

	// Verify no tool entries in #messages
	var toolEntriesInMessages bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(
			`document.querySelector('#messages [data-tool-key]') !== null`,
			&toolEntriesInMessages,
		),
	)
	if err != nil {
		t.Fatalf("query tool entries in messages failed: %v", err)
	}
	if toolEntriesInMessages {
		t.Error("tool entries should not appear in #messages after sidebar migration")
	}
}
