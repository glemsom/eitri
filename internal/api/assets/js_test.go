package assets

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

func TestJsFiles(t *testing.T) {
	files := []string{
		"eitri-session-id.js",
		"eitri-composer.js",
		"eitri-stream.js",
		"eitri-stream-common.js",
		"eitri-stream-toolcards.js",
		"eitri-stream-announcer.js",
		"eitri-stream-tokens.js",
		"eitri-stream-confirmation.js",
		"eitri-stream-scroll.js",
		"eitri-stream-render.js",
		"eitri-renderers.js",
		"eitri-mermaid.js",
		"eitri-lazy-load.js",
		"htmx.min.js",
		"prism-core.min.js",
		"prism-go.min.js",
		"katex.min.js",
		"katex-auto-render.min.js",
		"mermaid.min.js",
		"prism.min.css",
		"katex.min.css",
		"eitri-context.js",
		"eitri-persona-selector.js",
		"sw.js",
	}
	for _, name := range files {
		f, err := Files.Open(name)
		if err != nil {
			t.Errorf("failed to open %s: %v", name, err)
			continue
		}
		data, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Errorf("failed to read %s: %v", name, err)
			continue
		}
		t.Logf("%s: %d bytes", name, len(data))
	}

	// Verify composer JS has runStarted handler
	f, err := Files.Open("eitri-composer.js")
	if err != nil {
		t.Fatalf("failed to open eitri-composer.js: %v", err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-composer.js: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "eitri:runStarted") {
		t.Error("eitri-composer.js missing eitri:runStarted handler")
	}

	// Verify composer JS has draft persistence (issue #974)
	if !strings.Contains(content, "_draftKey") {
		t.Error("eitri-composer.js missing _draftKey for localStorage key")
	}
	if !strings.Contains(content, "_scheduleDraftSave") {
		t.Error("eitri-composer.js missing _scheduleDraftSave method for debounced writes")
	}
	if !strings.Contains(content, "_saveDraftNow") {
		t.Error("eitri-composer.js missing _saveDraftNow method")
	}
	if !strings.Contains(content, "_restoreDraft") {
		t.Error("eitri-composer.js missing _restoreDraft method")
	}
	if !strings.Contains(content, "_clearDraft") {
		t.Error("eitri-composer.js missing _clearDraft method")
	}
	if !strings.Contains(content, "localStorage.setItem") {
		t.Error("eitri-composer.js missing localStorage.setItem for draft persistence")
	}
	if !strings.Contains(content, "localStorage.getItem") {
		t.Error("eitri-composer.js missing localStorage.getItem for draft restoration")
	}
	if !strings.Contains(content, "localStorage.removeItem") {
		t.Error("eitri-composer.js missing localStorage.removeItem for draft clearing")
	}
	if !strings.Contains(content, "eitri:composer-draft:") {
		t.Error("eitri-composer.js missing per-session localStorage key prefix")
	}
	if !strings.Contains(content, "_draftDebounceMs") {
		t.Error("eitri-composer.js missing _draftDebounceMs for debounce configuration")
	}

	// Verify stream JS has reenableComposer
	f2, err := Files.Open("eitri-stream.js")
	if err != nil {
		t.Fatalf("failed to open eitri-stream.js: %v", err)
	}
	data2, err := io.ReadAll(f2)
	f2.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-stream.js: %v", err)
	}
	content2 := string(data2)

	// eitri-stream was split into per-concern modules (issue #1113): the
	// stream behaviour is spread across eitri-stream-common/toolcards/
	// announcer/tokens/confirmation/scroll/render.js, each a self-contained
	// island sharing the window.eitriStream runtime (loaded before the
	// orchestrator eitri-stream.js in base.templ). Assertions below that
	// describe cross-cutting stream behaviour run against the concatenated
	// modules so they stay meaningful regardless of which file owns the code.
	streamModuleNames := []string{
		"eitri-stream-common.js",
		"eitri-stream-toolcards.js",
		"eitri-stream-announcer.js",
		"eitri-stream-tokens.js",
		"eitri-stream-confirmation.js",
		"eitri-stream-scroll.js",
		"eitri-stream-render.js",
		"eitri-stream.js",
	}
	streamAll := ""
	for _, name := range streamModuleNames {
		modData, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		streamAll += string(modData)
	}
	if !strings.Contains(content2, "reenableComposer") {
		t.Error("eitri-stream.js missing reenableComposer function")
	}

	// Verify escapeHtml is defined only once (issue #974)
	escapeHtmlCount := strings.Count(streamAll, "function escapeHtml(")
	if escapeHtmlCount != 1 {
		t.Errorf("eitri-stream.js should define escapeHtml exactly once, found %d definitions", escapeHtmlCount)
	}

	// Verify stream JS has insertOptimisticBubble
	if !strings.Contains(streamAll, "insertOptimisticBubble") {
		t.Error("eitri-stream.js missing insertOptimisticBubble function")
	}

	// Verify stream JS has scrollToLatest
	if !strings.Contains(streamAll, "scrollToLatest") {
		t.Error("eitri-stream.js missing scrollToLatest function")
	}

	// Verify stream JS has scroll-to-bottom button logic (IntersectionObserver, sentinel, button toggle)
	if !strings.Contains(streamAll, "initScrollToBottomButton") {
		t.Error("eitri-stream.js missing initScrollToBottomButton function")
	}
	if !strings.Contains(streamAll, "scroll-to-bottom-btn") {
		t.Error("eitri-stream.js missing scroll-to-bottom-btn element reference")
	}
	if !strings.Contains(streamAll, "IntersectionObserver") {
		t.Error("eitri-stream.js missing IntersectionObserver for scroll detection")
	}

	// Verify stream JS has removeOptimisticBubbles
	if !strings.Contains(streamAll, "removeOptimisticBubbles") {
		t.Error("eitri-stream.js missing removeOptimisticBubbles function")
	}

	// Verify activity panel functions are removed
	if strings.Contains(content2, "autoOpenActivityPanel") {
		t.Error("eitri-stream.js should not contain autoOpenActivityPanel function")
	}
	if strings.Contains(content2, "updateActivitySummary") {
		t.Error("eitri-stream.js should not contain updateActivitySummary function")
	}

	if strings.Contains(content2, "activityElapsed") {
		t.Error("eitri-stream.js should not contain activityElapsed variable or function")
	}
	if strings.Contains(content2, "appendActivityEntry") {
		t.Error("eitri-stream.js should not contain appendActivityEntry function")
	}
	if strings.Contains(content2, "updateActivityCount") {
		t.Error("eitri-stream.js should not contain updateActivityCount function")
	}
	if strings.Contains(content2, "resetActivityPanel") {
		t.Error("eitri-stream.js should not contain resetActivityPanel function")
	}
	if strings.Contains(content2, "activityBriefForPacket") {
		t.Error("eitri-stream.js should not contain activityBriefForPacket function")
	}
	if strings.Contains(content2, "summarizeToolDetail") {
		t.Error("eitri-stream.js should not contain summarizeToolDetail function")
	}
	if strings.Contains(content2, "formatElapsed") {
		t.Error("eitri-stream.js should not contain formatElapsed function")
	}
	if strings.Contains(content2, "activityToolCount") {
		t.Error("eitri-stream.js should not contain activityToolCount variable")
	}
	if strings.Contains(content2, "activityToolSummary") {
		t.Error("eitri-stream.js should not contain activityToolSummary variable")
	}

	// Verify stream JS has context_update handler
	if !strings.Contains(content2, "context_update") {
		t.Error("eitri-stream.js missing context_update handler")
	}
	if !strings.Contains(content2, "dispatchContextUpdate") {
		t.Error("eitri-stream.js missing dispatchContextUpdate call")
	}
	if !strings.Contains(content2, "resetContextPanel") {
		t.Error("eitri-stream.js missing resetContextPanel call")
	}

	// Verify stream JS appends token-usage before scroll-sentinel
	if !strings.Contains(content2, "insertBefore") && strings.Contains(content2, "scroll-sentinel") {
		// Check that appendTokenUsage inserts before sentinel, not after
		if strings.Contains(content2, "messages.insertBefore(footer, sentinel)") {
			// Good: token-usage goes before sentinel
		} else if strings.Contains(content2, "messages.appendChild(footer)") && strings.Contains(content2, "// Insert before scroll-sentinel") {
			// Good: token-usage inserted before sentinel
		} else {
			t.Error("eitri-stream.js should insert token-usage before scroll-sentinel")
		}
	}

	f3, err := Files.Open("eitri-renderers.js")
	if err != nil {
		t.Fatalf("failed to open eitri-renderers.js: %v", err)
	}
	data3, err := io.ReadAll(f3)
	f3.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-renderers.js: %v", err)
	}
	content3 := string(data3)
	if !strings.Contains(content3, "initPrism") {
		t.Error("eitri-renderers.js missing Prism initialization")
	}
	if !strings.Contains(content3, "initKatex") {
		t.Error("eitri-renderers.js missing KaTeX initialization")
	}

	// Verify the lazy loader fetches the heavy libraries on demand and signals
	// the islands to render once they arrive (issue #968).
	fLazy, err := Files.Open("eitri-lazy-load.js")
	if err != nil {
		t.Fatalf("failed to open eitri-lazy-load.js: %v", err)
	}
	lazyData, err := io.ReadAll(fLazy)
	fLazy.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-lazy-load.js: %v", err)
	}
	contentLazy := string(lazyData)
	for _, want := range []string{
		"mermaid.min.js",
		"katex.min.js",
		"prism-core.min.js",
		"prism-go.min.js",
		"katex.min.css",
		"prism.min.css",
		"eitri:mermaid-loaded",
		"eitri:katex-loaded",
		"eitri:prism-loaded",
		"htmx:afterSwap",
	} {
		if !strings.Contains(contentLazy, want) {
			t.Errorf("eitri-lazy-load.js missing %q", want)
		}
	}
	if strings.Contains(contentLazy, "mermaid.initialize") {
		t.Error("eitri-lazy-load.js must only load libraries, not initialise them")
	}

	// Verify the lazy loader catches script-load failures, logs them once, and
	// notifies the renderer islands so content degrades visibly instead of
	// throwing unhandled promise rejections (issue #1078).
	for _, want := range []string{
		".catch(function (err) {",
		"eitri:mermaid-load-failed",
		"eitri:katex-load-failed",
		"eitri:prism-load-failed",
		"handleLoadFailure",
	} {
		if !strings.Contains(contentLazy, want) {
			t.Errorf("eitri-lazy-load.js missing %q", want)
		}
	}
	if !strings.Contains(content3, "eitri:katex-load-failed") {
		t.Error("eitri-renderers.js missing eitri:katex-load-failed handler for graceful degradation")
	}
	if !strings.Contains(content3, "eitri:prism-load-failed") {
		t.Error("eitri-renderers.js missing eitri:prism-load-failed handler for graceful degradation")
	}
	fMermaid, err := Files.Open("eitri-mermaid.js")
	if err != nil {
		t.Fatalf("failed to open eitri-mermaid.js: %v", err)
	}
	mermaidData, err := io.ReadAll(fMermaid)
	fMermaid.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-mermaid.js: %v", err)
	}
	contentMermaid := string(mermaidData)
	if !strings.Contains(contentMermaid, "eitri:mermaid-load-failed") {
		t.Error("eitri-mermaid.js missing eitri:mermaid-load-failed handler for graceful degradation")
	}

	// Verify CSS has scroll-to-bottom button with --composer-height variable
	f4, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("failed to open eitri.css: %v", err)
	}
	data4, err := io.ReadAll(f4)
	f4.Close()
	if err != nil {
		t.Fatalf("failed to read eitri.css: %v", err)
	}
	content4 := string(data4)

	// Verify the fallback styles for failed lazy-loads are present (issue #1078).
	if !strings.Contains(content4, "math-error") {
		t.Error("eitri.css missing .math-error fallback style for failed KaTeX loads")
	}
	if !strings.Contains(content4, "code-error") {
		t.Error("eitri.css missing .code-error fallback style for failed Prism loads")
	}

	// Verify CSS has .messages as scroll container with overflow-y: auto
	if !strings.Contains(content4, ".messages {") {
		t.Error("eitri.css missing .messages selector for scroll container")
	}
	// Check overflow-y: auto within messages block
	msgIdx := strings.Index(content4, ".messages {")
	if msgIdx >= 0 {
		// Scan forward from messages selector for overflow-y: auto
		block := content4[msgIdx:]
		closeIdx := strings.Index(block, "}")
		if closeIdx >= 0 {
			block = block[:closeIdx+1]
			if !strings.Contains(block, "overflow-y: auto") {
				t.Error(".messages CSS block missing overflow-y: auto (required for IntersectionObserver scroll container)")
			}
		}
	}
	if !strings.Contains(content4, "--composer-height") {
		t.Error("eitri.css missing --composer-height CSS variable for scroll-to-bottom positioning")
	}
	if !strings.Contains(content4, "calc(var(--composer-bottom, var(--composer-height") {
		t.Error("eitri.css missing calc(var(--composer-bottom, var(--composer-height) for scroll-to-bottom button bottom offset")
	}

	// Verify composer JS has composer height tracking on parent #chat-view
	if !strings.Contains(content, "_trackComposerHeight") {
		t.Error("eitri-composer.js missing _trackComposerHeight method")
	}
	if !strings.Contains(content, "ResizeObserver") {
		t.Error("eitri-composer.js missing ResizeObserver for composer height tracking")
	}
	if !strings.Contains(content, "parent.style.setProperty") {
		t.Error("eitri-composer.js should set --composer-height on parent element")
	}

	// Verify settings model refresh includes current unsaved form values.
	fSettings, err := Files.Open("eitri-settings.js")
	if err != nil {
		t.Fatalf("failed to open eitri-settings.js: %v", err)
	}
	dataSettings, err := io.ReadAll(fSettings)
	fSettings.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-settings.js: %v", err)
	}
	contentSettings := string(dataSettings)
	if !strings.Contains(contentSettings, "new FormData(form)") {
		t.Error("eitri-settings.js model refresh should read current settings form values")
	}
	if !strings.Contains(contentSettings, "URLSearchParams") {
		t.Error("eitri-settings.js model refresh should send form values as query params")
	}
	if strings.Contains(contentSettings, "fetch('/api/models')") {
		t.Error("eitri-settings.js model refresh must not fetch /api/models without form values")
	}

	// Verify context JS exports
	f5, err := Files.Open("eitri-context.js")
	if err != nil {
		t.Fatalf("failed to open eitri-context.js: %v", err)
	}
	data5, err := io.ReadAll(f5)
	f5.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-context.js: %v", err)
	}
	content5 := string(data5)

	if !strings.Contains(content5, "customElements.define") {
		t.Error("eitri-context.js missing customElements.define call")
	}
	if !strings.Contains(content5, "eitri-context") {
		t.Error("eitri-context.js missing eitri-context element name")
	}
	if !strings.Contains(content5, "context-update") {
		t.Error("eitri-context.js missing context-update event listener")
	}
	if !strings.Contains(content5, "resetToIdle") {
		t.Error("eitri-context.js missing resetToIdle method")
	}
	if !strings.Contains(content5, "dispatchContextUpdate") {
		t.Error("eitri-context.js missing dispatchContextUpdate helper")
	}
	if !strings.Contains(content5, "resetContextPanel") {
		t.Error("eitri-context.js missing resetContextPanel helper")
	}
	if !strings.Contains(content5, "_renderCompact") {
		t.Error("eitri-context.js missing _renderCompact method")
	}
	if !strings.Contains(content5, "_renderExpanded") {
		t.Error("eitri-context.js missing _renderExpanded method")
	}
	if !strings.Contains(content5, "fill-green") {
		t.Error("eitri-context.js missing fill-green class name")
	}
	if !strings.Contains(content5, "fill-yellow") {
		t.Error("eitri-context.js missing fill-yellow class name")
	}
	if !strings.Contains(content5, "fill-red") {
		t.Error("eitri-context.js missing fill-red class name")
	}
	if !strings.Contains(content5, "No active run") {
		t.Error("eitri-context.js missing idle state text")
	}
	if !strings.Contains(content5, "DEBOUNCE_MS") {
		t.Error("eitri-context.js missing DEBOUNCE_MS constant")
	}

	// Per-category progress bars
	if !strings.Contains(content5, "context-category-bar") {
		t.Error("eitri-context.js missing context-category-bar class for per-category mini bars")
	}
	if !strings.Contains(content5, "context-category-bar-fill") {
		t.Error("eitri-context.js missing context-category-bar-fill class for per-category mini bar fill")
	}
	if strings.Count(content5, "context-category-bar-fill") < 5 {
		t.Errorf("eitri-context.js has %d category-bar-fill elements, want at least 5 (one per row)", strings.Count(content5, "context-category-bar-fill"))
	}

	// Verify stream JS exports lightweightMarkdown function

	// Verify stream JS has cleanup helpers (clear/thinking/reset activity)
	if !strings.Contains(streamAll, "clearToolActivity") {
		t.Error("eitri-stream.js missing clearToolActivity function")
	}
	if !strings.Contains(streamAll, "clearThinkingPanel") {
		t.Error("eitri-stream.js missing clearThinkingPanel function")
	}
	if !strings.Contains(streamAll, "resetActivityTracking") {
		t.Error("eitri-stream.js missing resetActivityTracking function")
	}
	// Verify es.onerror preserves in-flight tool state instead of clearing it
	// before RECONNECTING: a transient EventSource error must not wipe tool
	// activity/elapsed data that resumes after reconnect (issue #1070).
	errReconnectIdx := strings.Index(content2, "state.status = S.STATES.RECONNECTING")
	if errReconnectIdx < 0 {
		t.Error("eitri-stream.js missing RECONNECTING state transition")
	} else {
		// Find es.onerror block — search backwards for it
		onerrorStart := strings.LastIndex(content2[:errReconnectIdx], "es.onerror = function")
		if onerrorStart < 0 {
			t.Error("eitri-stream.js missing es.onerror handler")
		} else {
			onerrorBlock := content2[onerrorStart:errReconnectIdx]
			if strings.Contains(onerrorBlock, "clearToolActivity()") {
				t.Error("es.onerror handler must NOT call clearToolActivity() — tool state survives reconnect (issue #1070)")
			}
			if strings.Contains(onerrorBlock, "clearThinkingPanel()") {
				t.Error("es.onerror handler must NOT call clearThinkingPanel() — thinking content survives reconnect (issue #1070)")
			}
			if strings.Contains(onerrorBlock, "resetActivityTracking()") {
				t.Error("es.onerror handler must NOT call resetActivityTracking() — elapsed tracking survives reconnect (issue #1070)")
			}
		}
	}
	// Verify a duplicate/replayed 'done' packet is ignored once the run is
	// already finalizing or finalized (guard on run status, issue #1070).
	if !strings.Contains(content2, "state.status === S.STATES.RENDERING || state.status === S.STATES.DONE") {
		t.Error("eitri-stream.js done handler missing RENDERING/DONE guard against duplicate/replayed done packets (issue #1070)")
	}
	// Verify tool card keys are replay-stable across reconnect replays.
	if !strings.Contains(streamAll, "toolKeysByIdentity") {
		t.Error("eitri-stream.js missing toolKeysByIdentity replay-stable tool card key map (issue #1070)")
	}
	if !strings.Contains(streamAll, "toolIdentityForPacket") {
		t.Error("eitri-stream.js missing toolIdentityForPacket helper (issue #1070)")
	}
	// Verify card timers die with their cards: FIFO eviction/full teardown go
	// through pruneToolCardState, and the interval self-stops when its card
	// leaves the DOM.
	if !strings.Contains(streamAll, "function pruneToolCardState(") {
		t.Error("eitri-stream.js missing pruneToolCardState helper (issue #1070)")
	}
	if !strings.Contains(streamAll, "!elapsedSpan || !elapsedSpan.isConnected") {
		t.Error("eitri-stream.js timer must self-stop when its card is removed from the DOM (issue #1070)")
	}
	if !strings.Contains(streamAll, "window.__activeToolCardTimerKeys") {
		t.Error("eitri-stream.js missing __activeToolCardTimerKeys test hook (issue #1070)")
	}
	if !strings.Contains(streamAll, "lightweightMarkdown") {
		t.Error("eitri-stream.js missing lightweightMarkdown function")
	}

	// Verify the screen-reader stream announcer (issue #1071): a hidden
	// role="status" live region that receives only *new* stream deltas at a
	// throttled cadence, so assistive tech announces the reply without
	// re-reading the full stream on every 80ms flush.
	if !strings.Contains(streamAll, "stream-announcer") {
		t.Error("eitri-stream.js missing stream-announcer live-region element")
	}
	if !strings.Contains(streamAll, "role") || !strings.Contains(streamAll, "'status'") {
		t.Error("eitri-stream.js stream announcer missing role=\"status\"")
	}
	if !strings.Contains(streamAll, "aria-live") || !strings.Contains(streamAll, "'polite'") {
		t.Error("eitri-stream.js stream announcer missing aria-live=\"polite\"")
	}
	if !strings.Contains(streamAll, "accumulateStreamAnnounce") {
		t.Error("eitri-stream.js missing accumulateStreamAnnounce delta bookkeeping")
	}
	if !strings.Contains(streamAll, "ANNOUNCE_INTERVAL_MS") {
		t.Error("eitri-stream.js missing ANNOUNCE_INTERVAL_MS throttle constant")
	}
	if !strings.Contains(streamAll, "flushStreamAnnounce") {
		t.Error("eitri-stream.js missing flushStreamAnnounce")
	}
}

// TestSharedSessionIdHelper verifies the one shared session-ID parsing
// implementation used by all frontend islands (issue #1077): it must extract
// hex-only IDs (no regression) and IDs containing '-' or '_' from both the
// page URL (/sessions/{id}) and the chat form action (/api/sessions/{id}/chat),
// and return an empty string when no session ID is present.
func TestSharedSessionIdHelper(t *testing.T) {
	data, err := Files.ReadFile("eitri-session-id.js")
	if err != nil {
		t.Fatalf("failed to read eitri-session-id.js: %v", err)
	}

	// Verify the helper is a single global used by every island.
	helperSrc := string(data)
	if !strings.Contains(helperSrc, "window.eitriGetSessionId") {
		t.Fatal("eitri-session-id.js must expose window.eitriGetSessionId")
	}
	// The stream island resolves its session ID via eitri-stream-common.js
	// (getSessionIdFromUrl calls window.eitriGetSessionId); the orchestrator
	// eitri-stream.js delegates to that shared helper through the runtime.
	for _, island := range []string{"eitri-events.js", "eitri-stream-common.js", "eitri-context.js", "eitri-composer.js"} {
		src, err := Files.ReadFile(island)
		if err != nil {
			t.Fatalf("failed to read %s: %v", island, err)
		}
		if !strings.Contains(string(src), "window.eitriGetSessionId") {
			t.Errorf("%s does not use the shared window.eitriGetSessionId helper", island)
		}
		if strings.Contains(string(src), "pathname.match") {
			t.Errorf("%s still parses the session ID from the URL itself; it must use window.eitriGetSessionId", island)
		}
	}

	runtime := goja.New()
	if _, err := runtime.RunString(`globalThis.window = { location: { pathname: '/sessions/not-used' } };`); err != nil {
		t.Fatalf("failed to set up window stub: %v", err)
	}
	if _, err := runtime.RunString(helperSrc); err != nil {
		t.Fatalf("failed to run eitri-session-id.js: %v", err)
	}
	var fn func(url string) string
	if err := runtime.ExportTo(runtime.Get("window").ToObject(runtime).Get("eitriGetSessionId"), &fn); err != nil {
		t.Fatalf("failed to export eitriGetSessionId: %v", err)
	}

	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "hex page URL", url: "/sessions/abc123def456", want: "abc123def456"},
		{name: "dash in ID", url: "/sessions/abc-def_123", want: "abc-def_123"},
		{name: "underscore in ID", url: "/sessions/abc_def-123", want: "abc_def-123"},
		{name: "mixed alnum dash underscore", url: "/sessions/sess-7f3c_ab", want: "sess-7f3c_ab"},
		{name: "hex API chat action", url: "/api/sessions/abc123def456/chat", want: "abc123def456"},
		{name: "non-hex API chat action", url: "/api/sessions/abc-def_123/chat", want: "abc-def_123"},
		{name: "no session in root", url: "/", want: ""},
		{name: "no session in settings", url: "/settings", want: ""},
		{name: "sessions list page", url: "/sessions", want: ""},
		{name: "no url falls back to current pathname", url: "", want: "not-used"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fn(tc.url)
			if got != tc.want {
				t.Errorf("eitriGetSessionId(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestStreamIslandNoDebugLogging guards the streaming island (issue #1076)
// against debug console.log calls that noise up the console on every stream:
// a disconnect-trigger log and full packet dumps when rendering components.
// Normal streaming, rendering, and disconnect flows must stay silent. Error
// handling is allowed to keep using console.warn/console.error — those fire
// only on failure paths, never during normal operation.
func TestStreamIslandNoDebugLogging(t *testing.T) {
	f, err := Files.Open("eitri-stream.js")
	if err != nil {
		t.Fatalf("failed to open eitri-stream.js: %v", err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatalf("failed to read eitri-stream.js: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "console.log") {
		t.Error("eitri-stream.js contains console.log debug calls; remove them (issue #1076)")
	}
}

func TestLightweightMarkdown(t *testing.T) {
	f, err := Files.Open("eitri-stream-common.js")
	if err != nil {
		t.Fatalf("failed to open eitri-stream-common.js: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read eitri-stream-common.js: %v", err)
	}
	content := string(data)

	// Extract the lightweightMarkdown function body
	// Defined as: function lightweightMarkdown(text) { ... }
	startMatch := "function lightweightMarkdown(text) {"
	startIdx := strings.Index(content, startMatch)
	if startIdx < 0 {
		t.Fatal("lightweightMarkdown function not found in eitri-stream.js")
	}
	// Opening brace position
	braceIdx := startIdx + len(startMatch) - 1
	// Body starts after the {
	bodyStart := braceIdx + 1

	// Find matching closing brace — scan counting braces
	depth := 1
	bodyEnd := bodyStart
	for bodyEnd < len(content) {
		switch content[bodyEnd] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				bodyEnd++
				goto extractBody
			}
		}
		bodyEnd++
	}
extractBody:
	if depth != 0 {
		t.Fatal("could not find matching closing brace for lightweightMarkdown function")
	}

	// Build JS function — extracted body only
	fnSrc := "function lightweightMarkdown(text) {" + content[bodyStart:bodyEnd]

	runtime := goja.New()
	_, err = runtime.RunString(fnSrc)
	if err != nil {
		t.Fatalf("failed to parse lightweightMarkdown: %v", err)
	}

	var fn func(string) string
	err = runtime.ExportTo(runtime.Get("lightweightMarkdown"), &fn)
	if err != nil {
		t.Fatalf("failed to export lightweightMarkdown: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		wantHTML string
	}{
		{
			name:     "bold",
			input:    "**bold**",
			wantHTML: "<strong>bold</strong>",
		},
		{
			name:     "italic",
			input:    "*italic*",
			wantHTML: "<em>italic</em>",
		},
		{
			name:     "inline code",
			input:    "`code`",
			wantHTML: "<code>code</code>",
		},
		{
			name:     "https link",
			input:    "[text](https://example.com)",
			wantHTML: `<a href="https://example.com" target="_blank" rel="noopener">text</a>`,
		},
		{
			name:     "http link",
			input:    "[text](http://example.com)",
			wantHTML: `<a href="http://example.com" target="_blank" rel="noopener">text</a>`,
		},
		{
			name:     "mailto link",
			input:    "[me](mailto:u@h.com)",
			wantHTML: `<a href="mailto:u@h.com" target="_blank" rel="noopener">me</a>`,
		},
		{
			name:     "javascript: link — no <a>",
			input:    "[click](javascript:alert(1))",
			wantHTML: "[click](javascript:alert(1))",
		},
		{
			name:     "data: link — no <a>",
			input:    "[bad](data:text/html,<svg>)",
			wantHTML: "[bad](data:text/html,&lt;svg&gt;)",
		},
		{
			name:     "incomplete/unclosed bold",
			input:    "**unclosed",
			wantHTML: "**unclosed",
		},
		{
			name:     "paragraph breaks",
			input:    "para1\n\npara2",
			wantHTML: "</p><p>",
		},
		{
			name:     "mixed bold italic code",
			input:    "**bold** *italic* `code`",
			wantHTML: "<strong>bold</strong> <em>italic</em> <code>code</code>",
		},
		{
			name:     "plain text wrapped in <p>",
			input:    "hello world",
			wantHTML: "<p>hello world</p>",
		},
		{
			name:     "unordered list",
			input:    "- item1\n- item2",
			wantHTML: "<li>item1</li>",
		},
		{
			name:     "task list unchecked",
			input:    "- [ ] todo",
			wantHTML: `<li><label><input type="checkbox" disabled="" /> todo</label></li>`,
		},
		{
			name:     "task list checked",
			input:    "- [x] done",
			wantHTML: `<li><label><input type="checkbox" checked="" disabled="" /> done</label></li>`,
		},
		{
			name:     "task list with preceding paragraph",
			input:    "What tools do you have?\n\n- [ ] Check tool description\n- [ ] Check if there are any guidelines to using the tools",
			wantHTML: `<ul class="task-list">`,
		},
		{
			name:     "mixed list types",
			input:    "- item1\n- item2\n- item3",
			wantHTML: "<li>item1</li><li>item2</li><li>item3</li>",
		},
		{
			name:     "ordered list",
			input:    "1. first\n2. second",
			wantHTML: "<li>first</li><li>second</li>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fn(tc.input)
			if !strings.Contains(got, tc.wantHTML) {
				t.Errorf("lightweightMarkdown(%q)\n  got:  %q\n  want substring: %q", tc.input, got, tc.wantHTML)
			}
		})
	}
}

func TestServiceWorker(t *testing.T) {
	f, err := Files.Open("sw.js")
	if err != nil {
		t.Fatalf("failed to open sw.js: %v", err)
	}
	data, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		t.Fatalf("failed to read sw.js: %v", err)
	}
	content := string(data)

	tests := []struct {
		name    string
		want    string
		missing string
	}{
		{
			name: "install event precaches all static assets",
			want: `cache.addAll([`,
		},
		{
			name: "precaches root path",
			want: `"/"`,
		},
		{
			name: "precaches eitri.css",
			want: `"/static/eitri.css?v=__EITRI_VERSION__"`,
		},
		{
			name: "precaches JS files",
			want: `"/static/htmx.min.js?v=__EITRI_VERSION__"`,
		},
		{
			name: "precaches favicon",
			want: `"/static/favicon-32.png?v=__EITRI_VERSION__"`,
		},
		{
			name: "precaches manifest",
			want: `"/manifest.json"`,
		},
		{
			name: "activate event cleans old caches",
			want: `caches.keys`,
		},
		{
			name: "activate event deletes non-current caches",
			want: `caches.delete(key)`,
		},
		{
			name: "network-only for /api/ endpoints",
			want: `url.pathname.startsWith("/api/")`,
		},
		{
			name: "network-only for /stream endpoints",
			want: `url.pathname.startsWith("/stream")`,
		},
		{
			name: "cache-first for /static/ assets",
			want: `url.pathname.startsWith("/static/")`,
		},
		{
			name: "cache-first uses cache.match then fetch fallback",
			want: `cache.match(event.request)`,
		},
		{
			name: "precaches self-hosted Geist fonts",
			want: `"/static/fonts/Geist-latin.woff2?v=__EITRI_VERSION__"`,
		},
		{
			name: "precaches self-hosted JetBrains Mono fonts",
			want: `"/static/fonts/JetBrainsMono-latin.woff2?v=__EITRI_VERSION__"`,
		},
		{
			name:    "no Google Fonts CDN dependency",
			want:    `url.pathname.startsWith("/static/")`,
			missing: `fonts.googleapis.com`,
		},
		{
			name:    "no fonts.gstatic.com CDN dependency",
			want:    `url.pathname.startsWith("/static/")`,
			missing: `fonts.gstatic.com`,
		},
		{
			name: "navigation fallback to cached shell",
			want: `event.request.mode === "navigate"`,
		},
		{
			name: "navigation fetch with cache fallback",
			want: `caches.match("/")`,
		},
		{
			name: "skipWaiting on install",
			want: `self.skipWaiting()`,
		},
		{
			name: "clients.claim on activate",
			want: `self.clients.claim()`,
		},
		{
			name: "cache version constant",
			want: `CACHE`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(content, tc.want) {
				t.Errorf("sw.js should contain %q", tc.want)
			}
			if tc.missing != "" && strings.Contains(content, tc.missing) {
				t.Errorf("sw.js should not contain %q (wrong section)", tc.missing)
			}
		})
	}
}

// TestStreamJSVersionedAvatar verifies eitri-stream.js builds the streaming
// bubble avatar URL with the cache-bust version (issue #969).
func TestStreamJSVersionedAvatar(t *testing.T) {
	data, err := Files.ReadFile("eitri-stream-tokens.js")
	if err != nil {
		t.Fatalf("failed to read eitri-stream-tokens.js: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "data-asset-version") {
		t.Error("eitri-stream-tokens.js should read the page shell's data-asset-version for cache busting")
	}
	if !strings.Contains(content, "/static/face.webp?v=") {
		t.Error("eitri-stream-tokens.js should append the cache-bust version to /static/face.webp")
	}
}

// TestAssetVersionPlaceholder verifies that files served dynamically by the
// HTTP server (sw.js, manifest.json) embed the cache-bust placeholder so the
// server can substitute the current asset version at serve time (issue #969).
func TestAssetVersionPlaceholder(t *testing.T) {
	for _, name := range []string{"sw.js", "manifest.json"} {
		data, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		content := string(data)
		if !strings.Contains(content, "__EITRI_VERSION__") {
			t.Errorf("%s missing __EITRI_VERSION__ placeholder (server substitutes the asset version)", name)
		}
	}
}

// TestLazyLoadAssetVersioning verifies the on-demand heavy-library loader builds
// versioned URLs from the cache-bust version the page shell renders (issue #969).
func TestLazyLoadAssetVersioning(t *testing.T) {
	data, err := Files.ReadFile("eitri-lazy-load.js")
	if err != nil {
		t.Fatalf("failed to read eitri-lazy-load.js: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "assetUrl") {
		t.Error("eitri-lazy-load.js missing assetUrl helper for versioned URLs")
	}
	if !strings.Contains(content, "data-asset-version") {
		t.Error("eitri-lazy-load.js should read the page shell's data-asset-version for cache busting")
	}
	// Every heavy-library URL must go through assetUrl so released asset changes
	// are picked up despite immutable caching.
	for _, want := range []string{
		"assetUrl('/static/mermaid.min.js')",
		"assetUrl('/static/katex.min.css')",
		"assetUrl('/static/katex.min.js')",
		"assetUrl('/static/prism.min.css')",
		"assetUrl('/static/prism-core.min.js')",
		"assetUrl('/static/prism-go.min.js')",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("eitri-lazy-load.js should load %s via assetUrl", want)
		}
	}
}

// TestJsListenerHygiene verifies that frontend custom elements never leak
// document/body-level listeners across HTMX re-renders (issue #1069).
// Navigating the app re-creates the sidebar elements (context panel, persona
// selector, composer) on every swap; each must tear down its document-level
// listeners when disconnected so detached elements can be garbage-collected.
func TestJsListenerHygiene(t *testing.T) {
	read := func(name string) string {
		data, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		return string(data)
	}
	composer := read("eitri-composer.js")
	context := read("eitri-context.js")
	persona := read("eitri-persona-selector.js")

	// AC1: every custom element that registers document/body-level listeners
	// implements disconnectedCallback (or equivalent) that removes them.
	for _, want := range []string{
		"disconnectedCallback",
		"document.removeEventListener('keydown'",
		"visualViewport.removeEventListener",
	} {
		if !strings.Contains(composer, want) {
			t.Errorf("eitri-composer.js missing %q in its disconnect cleanup", want)
		}
	}
	for _, want := range []string{
		"disconnectedCallback",
		"document.body.removeEventListener",
	} {
		if !strings.Contains(context, want) {
			t.Errorf("eitri-context.js missing %q in its disconnect cleanup", want)
		}
	}
	// The context panel's body-level listener must be added via a stored
	// reference so disconnectedCallback can remove the exact handler.
	if !strings.Contains(context, "_onBodyAfterRequest") {
		t.Error("eitri-context.js should store its document.body htmx:afterRequest handler for removal")
	}

	// AC2: re-entry is guarded (e.g. _initialized flag) so moving or
	// re-rendering an element does not double-register handlers.
	for _, file := range []struct{ name, src string }{
		{"eitri-composer.js", composer},
		{"eitri-context.js", context},
	} {
		if !strings.Contains(file.src, "_initialized") {
			t.Errorf("%s missing _initialized re-entry guard", file.name)
		}
	}

	// AC3: the composer's requestAnimationFrame retry loop in connectedCallback
	// terminates if the expected form is never found (attempt cap or timeout).
	if !strings.Contains(composer, "MAX_COMPOSER_INIT_ATTEMPTS") {
		t.Error("eitri-composer.js missing MAX_COMPOSER_INIT_ATTEMPTS cap for the init retry loop")
	}
	if !strings.Contains(composer, "_initAttempts < MAX_COMPOSER_INIT_ATTEMPTS") {
		t.Error("eitri-composer.js should bound its connectedCallback rAF retry loop with the attempt cap")
	}

	// AC4 (equivalent, statically): the persona selector must register its
	// document-level click listener exactly once (delegated, at module scope),
	// never once per re-created element.
	if n := strings.Count(persona, "document.addEventListener('click'"); n != 1 {
		t.Errorf("eitri-persona-selector.js should register exactly one delegated document click listener, found %d", n)
	}
	if !strings.Contains(persona, "querySelectorAll('#persona-selector')") {
		t.Error("eitri-persona-selector.js delegated click listener should walk current #persona-selector elements")
	}
	// Per-element init must stay idempotent so re-rendering the same element
	// cannot stack handlers.
	if !strings.Contains(persona, "psInitialized") {
		t.Error("eitri-persona-selector.js missing psInitialized idempotency guard")
	}
}

// TestPersonaSelectorKeyboardContract verifies the persona selector dropdown
// implements the WAI-ARIA listbox keyboard contract (issue #1074): the
// trigger advertises the popup, arrow/Home/End keys navigate the options,
// Escape closes the dropdown and returns focus to the trigger, Tab closes the
// widget, and a persona activation hands focus back to the re-rendered
// trigger so keyboard users can keep operating the dropdown.
func TestPersonaSelectorKeyboardContract(t *testing.T) {
	data, err := Files.ReadFile("eitri-persona-selector.js")
	if err != nil {
		t.Fatalf("failed to read eitri-persona-selector.js: %v", err)
	}
	content := string(data)

	// AC1 — options can be navigated and Escape returns focus to the trigger.
	for _, want := range []string{
		"personaMoveFocus", // shared index math behind navigation
		"'ArrowDown'",
		"'ArrowUp'",
		"'Home'",
		"'End'",
		"e.key === 'Escape'", // Escape closes…
		"trigger.focus()",    // …and returns focus to the trigger
		"e.key === 'Tab'",    // Tab closes the widget
	} {
		if !strings.Contains(content, want) {
			t.Errorf("eitri-persona-selector.js missing %q in its keyboard contract", want)
		}
	}

	// AC2 — the trigger announces the popup and options expose selection.
	for _, want := range []string{
		"aria-haspopup",
		"aria-expanded",
		"aria-controls",
		"aria-selected",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("eitri-persona-selector.js missing %q in its ARIA wiring", want)
		}
	}

	// Focus hand-back to the re-created trigger after an activation swap.
	if !strings.Contains(content, "e.detail.target.id === 'persona-selector'") {
		t.Error("eitri-persona-selector.js should restore focus to the re-created trigger after a persona activation swap")
	}
}

// TestPersonaMoveFocus verifies the pure index math behind the persona
// listbox arrow-key navigation (issue #1074): ArrowDown/ArrowUp wrap at the
// ends of the list, Home/End jump to the extremes, and non-navigation keys
// return -1 so the caller leaves the key to its native behaviour.
func TestPersonaMoveFocus(t *testing.T) {
	data, err := Files.ReadFile("eitri-persona-selector.js")
	if err != nil {
		t.Fatalf("failed to read eitri-persona-selector.js: %v", err)
	}
	fnSrc := extractFunctionBody(t, string(data), "function personaMoveFocus(key, currentIndex, optionCount) {")

	runtime := goja.New()
	if _, err := runtime.RunString(fnSrc); err != nil {
		t.Fatalf("failed to parse personaMoveFocus: %v", err)
	}
	var fn func(string, int, int) int
	if err := runtime.ExportTo(runtime.Get("personaMoveFocus"), &fn); err != nil {
		t.Fatalf("failed to export personaMoveFocus: %v", err)
	}

	t.Run("arrow down moves forward and wraps", func(t *testing.T) {
		if got := fn("ArrowDown", 0, 2); got != 1 {
			t.Errorf("ArrowDown from 0/2 = %d, want 1", got)
		}
		if got := fn("ArrowDown", 1, 2); got != 0 {
			t.Errorf("ArrowDown from 1/2 should wrap to 0, got %d", got)
		}
	})
	t.Run("arrow up moves backward and wraps", func(t *testing.T) {
		if got := fn("ArrowUp", 1, 2); got != 0 {
			t.Errorf("ArrowUp from 1/2 = %d, want 0", got)
		}
		if got := fn("ArrowUp", 0, 2); got != 1 {
			t.Errorf("ArrowUp from 0/2 should wrap to 1, got %d", got)
		}
	})
	t.Run("home and end jump to the extremes", func(t *testing.T) {
		if got := fn("Home", 1, 2); got != 0 {
			t.Errorf("Home = %d, want 0", got)
		}
		if got := fn("End", 0, 2); got != 1 {
			t.Errorf("End = %d, want 1", got)
		}
	})
	t.Run("non-navigation keys leave the key alone", func(t *testing.T) {
		if got := fn("Enter", 0, 2); got != -1 {
			t.Errorf("Enter = %d, want -1", got)
		}
		if got := fn(" ", 0, 2); got != -1 {
			t.Errorf("Space = %d, want -1", got)
		}
		if got := fn("Escape", 0, 2); got != -1 {
			t.Errorf("Escape = %d, want -1", got)
		}
	})
	t.Run("empty list never navigates", func(t *testing.T) {
		if got := fn("ArrowDown", 0, 0); got != -1 {
			t.Errorf("ArrowDown on empty list = %d, want -1", got)
		}
	})
}

// TestComposerInitRetryLoopIsBounded simulates the composer's connectedCallback
// while its form is permanently missing and verifies the requestAnimationFrame
// retry loop terminates (issue #1069, acceptance criterion 3).
func TestComposerInitRetryLoopIsBounded(t *testing.T) {
	data, err := Files.ReadFile("eitri-composer.js")
	if err != nil {
		t.Fatalf("failed to read eitri-composer.js: %v", err)
	}

	runtime := goja.New()
	_, err = runtime.RunString(`
		// Minimal stubs: HTMLElement whose querySelector never finds the form,
		// an rAF that queues callbacks for manual draining, and a customElements
		// registry that captures the defined class.
		class FakeHTMLElement {
			constructor() {
				this.isConnected = true;
			}
			querySelector() { return null; }
		}
		var rafQueue = [];
		var rafScheduled = 0;
		globalThis.HTMLElement = FakeHTMLElement;
		globalThis.window = {
			requestAnimationFrame: function (cb) { rafScheduled++; rafQueue.push(cb); },
		};
		globalThis.document = { addEventListener: function () {} };
		globalThis.customElements = { define: function (name, cls) { globalThis.definedComposer = cls; } };
	` + "\n" + string(data))
	if err != nil {
		t.Fatalf("failed to run eitri-composer.js in goja: %v", err)
	}

	// Drain retries until the loop must give up.
	_, err = runtime.RunString(`
		var el = new definedComposer();
		var drained = 0;
		el.connectedCallback();
		while (rafQueue.length > 0 && drained < 500) {
			rafQueue.shift()();
			drained++;
		}
	`)
	if err != nil {
		t.Fatalf("failed to drain composer retry loop: %v", err)
	}
	var drained, scheduled int64
	if err := runtime.ExportTo(runtime.Get("drained"), &drained); err != nil {
		t.Fatalf("failed to export drained: %v", err)
	}
	if err := runtime.ExportTo(runtime.Get("rafScheduled"), &scheduled); err != nil {
		t.Fatalf("failed to export rafScheduled: %v", err)
	}
	if scheduled != 29 {
		t.Errorf("composer retry loop scheduled %d rAF frames, want 29 (MAX_COMPOSER_INIT_ATTEMPTS-1 retries after the initial attempt)", scheduled)
	}
	if drained != 29 {
		t.Errorf("composer retry loop drained %d rAF frames, want 29", drained)
	}

	// A second element torn down mid-retry must stop scheduling immediately.
	_, err = runtime.RunString(`
		rafQueue.length = 0;
		rafScheduled = 0;
		var el2 = new definedComposer();
		el2.connectedCallback();
		el2.isConnected = false;
		var drained2 = 0;
		while (rafQueue.length > 0 && drained2 < 50) {
			rafQueue.shift()();
			drained2++;
		}
	`)
	if err != nil {
		t.Fatalf("failed to drain torn-down composer retry loop: %v", err)
	}
	var scheduled2 int64
	if err := runtime.ExportTo(runtime.Get("rafScheduled"), &scheduled2); err != nil {
		t.Fatalf("failed to export rafScheduled after teardown: %v", err)
	}
	if scheduled2 != 1 {
		t.Errorf("torn-down composer should schedule exactly 1 rAF frame before bailing, got %d", scheduled2)
	}
}

// TestContextElementListenerLifecycle simulates repeated connect/disconnect
// cycles of the eitri-context custom element and verifies the document.body
// listener is added once and removed on disconnect — no growth across swaps
// (issue #1069, acceptance criteria 1 and 2).
func TestContextElementListenerLifecycle(t *testing.T) {
	data, err := Files.ReadFile("eitri-context.js")
	if err != nil {
		t.Fatalf("failed to read eitri-context.js: %v", err)
	}

	runtime := goja.New()
	_, err = runtime.RunString(`
		var bodyListenerCount = 0;
		var headerListenerCount = 0;
		class FakeEl {
			constructor() {
				this.style = {};
				this.classList = { toggle: function () {}, add: function () {}, remove: function () {} };
				this._listeners = {};
			}
			getAttribute() { return null; }
			setAttribute() {}
			addEventListener(type, fn) { this._listeners[type] = fn; }
			removeEventListener(type, fn) {}
			querySelector() { return new FakeEl(); }
			contains() { return true; }
		}
		// The sidebar header is outside the custom element; track its listener
		// separately so we can assert disconnect removes it too.
		class HeaderEl {
			addEventListener(type, fn) { if (type === 'click') headerListenerCount++; }
			removeEventListener(type, fn) { if (type === 'click') headerListenerCount--; }
		}
		globalThis.HTMLElement = FakeEl;
		globalThis.document = {
			querySelector: function () { return new HeaderEl(); },
			body: {
				addEventListener: function (type) { if (type === 'htmx:afterRequest') bodyListenerCount++; },
				removeEventListener: function (type) { if (type === 'htmx:afterRequest') bodyListenerCount--; },
			},
			addEventListener: function () {},
		};
		globalThis.window = {
			location: { pathname: '/sessions/abc123' },
			eitriGetSessionId: function () { return 'abc123'; },
			setTimeout: function () {},
			clearTimeout: function () {},
		};
		globalThis.customElements = { define: function (name, cls) { globalThis.definedContext = cls; } };
	` + "\n" + string(data))
	if err != nil {
		t.Fatalf("failed to run eitri-context.js in goja: %v", err)
	}

	_, err = runtime.RunString(`
		var el = new definedContext();
		el.connectedCallback();
		var afterFirstConnect = bodyListenerCount;
		el.connectedCallback();
		var afterSecondConnect = bodyListenerCount;
		el.disconnectedCallback();
		var afterDisconnect = bodyListenerCount;
		var headersAfterDisconnect = headerListenerCount;
	`)
	if err != nil {
		t.Fatalf("failed to exercise context element lifecycle: %v", err)
	}

	var afterFirstConnect, afterSecondConnect, afterDisconnect, headersAfterDisconnect int64
	for name, v := range map[string]*int64{
		"afterFirstConnect":      &afterFirstConnect,
		"afterSecondConnect":     &afterSecondConnect,
		"afterDisconnect":        &afterDisconnect,
		"headersAfterDisconnect": &headersAfterDisconnect,
	} {
		if err := runtime.ExportTo(runtime.Get(name), v); err != nil {
			t.Fatalf("failed to export %s: %v", name, err)
		}
	}
	if afterFirstConnect != 1 {
		t.Errorf("context element should add exactly 1 document.body listener on connect, got %d", afterFirstConnect)
	}
	if afterSecondConnect != 1 {
		t.Errorf("context element re-connect should not add another document.body listener, got %d", afterSecondConnect)
	}
	if afterDisconnect != 0 {
		t.Errorf("context element should remove its document.body listener on disconnect, got %d", afterDisconnect)
	}
	if headersAfterDisconnect != 0 {
		t.Errorf("context element should remove its sidebar-header listener on disconnect, got %d", headersAfterDisconnect)
	}
}

// extractFunctionBody pulls the body of a top-level function declaration out
// of a JS source file, mirroring the extraction used for lightweightMarkdown.
// The function must have no nested top-level braces outside its own body.
func extractFunctionBody(t *testing.T, content, startMatch string) string {
	t.Helper()
	startIdx := strings.Index(content, startMatch)
	if startIdx < 0 {
		t.Fatalf("function %q not found in JS source", startMatch)
	}
	braceIdx := startIdx + len(startMatch) - 1
	bodyStart := braceIdx + 1
	depth := 1
	bodyEnd := bodyStart
	for bodyEnd < len(content) {
		switch content[bodyEnd] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return startMatch + content[bodyStart:bodyEnd+1]
			}
		}
		bodyEnd++
	}
	t.Fatalf("could not find matching closing brace for %q", startMatch)
	return ""
}

// TestLazyLoadScriptFailureIsCaughtAndLoggedOnce simulates a network failure
// while the lazy loader fetches mermaid.min.js and verifies the rejection is
// caught — no unhandled promise rejection, exactly one console.error, and a
// failure event the renderer islands can react to (issue #1078, acceptance
// criterion 1). The <script> onerror fires asynchronously (as in a real
// browser) so the .then/.catch handlers are attached before the rejection.
func TestLazyLoadScriptFailureIsCaughtAndLoggedOnce(t *testing.T) {
	data, err := Files.ReadFile("eitri-lazy-load.js")
	if err != nil {
		t.Fatalf("failed to read eitri-lazy-load.js: %v", err)
	}

	runtime := goja.New()

	// Track unhandled rejections the way a browser's unhandledrejection would.
	var rejectionOps []goja.PromiseRejectionOperation
	runtime.SetPromiseRejectionTracker(func(_ *goja.Promise, op goja.PromiseRejectionOperation) {
		rejectionOps = append(rejectionOps, op)
	})

	_, err = runtime.RunString(`
		var scriptEls = [];
		var errors = [];
		var dispatched = [];
		function FakeCustomEvent(type) { this.type = type; }
		globalThis.CustomEvent = FakeCustomEvent;
		globalThis.console = { error: function (msg) { errors.push(String(msg)); } };
		globalThis.document = {
			readyState: 'complete',
			body: { getAttribute: function () { return 'test-version'; } },
			createElement: function (tag) {
				if (tag === 'script') {
					var el = { src: '', async: false, onload: null, onerror: null };
					scriptEls.push(el);
					return el;
				}
				return { rel: '', href: '' };
			},
			head: {
				appendChild: function (el) {
					if (el && typeof el.onerror === 'function') {
						// Simulate the network failure asynchronously — a real
						// <script> onerror never fires during appendChild.
						Promise.resolve().then(function () { el.onerror(); });
					}
				}
			},
			querySelector: function (sel) {
				// Only mermaid content is present, so only mermaid is loaded.
				if (sel === 'pre.mermaid') return {};
				return null;
			},
			querySelectorAll: function () { return []; },
			addEventListener: function () {},
			dispatchEvent: function (ev) { dispatched.push(ev.type); },
		};
	` + "\n" + string(data))
	if err != nil {
		t.Fatalf("failed to run eitri-lazy-load.js in goja: %v", err)
	}

	if len(rejectionOps) != 0 {
		t.Errorf("script-load failure produced %d unhandled promise rejection report(s), want 0", len(rejectionOps))
	}

	var errors, dispatched []string
	if err := runtime.ExportTo(runtime.Get("errors"), &errors); err != nil {
		t.Fatalf("failed to export errors: %v", err)
	}
	if len(errors) != 1 {
		t.Fatalf("console.error called %d time(s), want exactly 1 (logged once)", len(errors))
	}
	if !strings.Contains(errors[0], "mermaid") {
		t.Errorf("console.error %q does not mention the failed library", errors[0])
	}
	if err := runtime.ExportTo(runtime.Get("dispatched"), &dispatched); err != nil {
		t.Fatalf("failed to export dispatched: %v", err)
	}
	if !slices.Contains(dispatched, "eitri:mermaid-load-failed") {
		t.Errorf("dispatched events %v missing eitri:mermaid-load-failed", dispatched)
	}
}

// TestMermaidLoadFailureDegradesContent verifies that when the lazy loader
// reports a failed mermaid.min.js fetch, every untouched diagram is marked
// with the error class and gets a visible "could not be loaded" message with
// its raw source preserved (issue #1078, acceptance criterion 2).
func TestMermaidLoadFailureDegradesContent(t *testing.T) {
	data, err := Files.ReadFile("eitri-mermaid.js")
	if err != nil {
		t.Fatalf("failed to read eitri-mermaid.js: %v", err)
	}

	runtime := goja.New()
	_, err = runtime.RunString(`
		var markedEls = [];
		var listeners = {};
		var inserted = [];
		globalThis.window = { matchMedia: function () { return { matches: true }; } };
		globalThis.setTimeout = function () {};
		globalThis.document = {
			readyState: 'loading',
			querySelectorAll: function () { return markedEls; },
			addEventListener: function (type, fn) { listeners[type] = fn; },
			dispatchEvent: function () {},
		};
	` + "\n" + string(data))
	if err != nil {
		t.Fatalf("failed to run eitri-mermaid.js in goja: %v", err)
	}

	_, err = runtime.RunString(`
		var pre = {
			_classes: [],
			_attrs: {},
			classList: { add: function (c) { pre._classes.push(c); } },
			setAttribute: function (k, v) { pre._attrs[k] = v; },
			insertAdjacentHTML: function (pos, html) { inserted.push(pos + ':' + html); },
		};
		markedEls.push(pre);
		listeners['eitri:mermaid-load-failed']();
		var outClasses = pre._classes;
		var outAttrs = pre._attrs;
	`)
	if err != nil {
		t.Fatalf("failed to fire eitri:mermaid-load-failed: %v", err)
	}

	var classes, insertedStrs []string
	var attrs map[string]interface{}
	if err := runtime.ExportTo(runtime.Get("outClasses"), &classes); err != nil {
		t.Fatalf("failed to export pre classes: %v", err)
	}
	if err := runtime.ExportTo(runtime.Get("outAttrs"), &attrs); err != nil {
		t.Fatalf("failed to export pre attrs: %v", err)
	}
	if err := runtime.ExportTo(runtime.Get("inserted"), &insertedStrs); err != nil {
		t.Fatalf("failed to export inserted: %v", err)
	}
	if !slices.Contains(classes, "mermaid-error") {
		t.Errorf("diagram classes %v missing mermaid-error", classes)
	}
	if attrs["data-mermaid-processed"] != "true" {
		t.Errorf("data-mermaid-processed = %v, want true", attrs["data-mermaid-processed"])
	}
	if len(insertedStrs) != 1 || !strings.Contains(insertedStrs[0], "Diagram renderer could not be loaded") {
		t.Errorf("diagram fallback message not inserted: %v", insertedStrs)
	}
}

// TestRenderersLoadFailureDegradesContent verifies the KaTeX/Prism failure
// handlers mark the affected content so the missing formatting is visible
// rather than silently skipped: raw LaTeX stays in place with a .math-error
// marker and raw code blocks get a .code-error marker on their <pre> (issue
// #1078, acceptance criterion 2).
func TestRenderersLoadFailureDegradesContent(t *testing.T) {
	data, err := Files.ReadFile("eitri-renderers.js")
	if err != nil {
		t.Fatalf("failed to read eitri-renderers.js: %v", err)
	}

	runtime := goja.New()
	_, err = runtime.RunString(`
		var katexEls = [];
		var codeEls = [];
		var listeners = {};
		globalThis.document = {
			readyState: 'loading',
			querySelectorAll: function (sel) {
				if (sel === '.math-inline, .math-block') return katexEls;
				if (sel === 'pre code') return codeEls;
				return [];
			},
			addEventListener: function (type, fn) { listeners[type] = fn; },
		};
	` + "\n" + string(data))
	if err != nil {
		t.Fatalf("failed to run eitri-renderers.js in goja: %v", err)
	}

	_, err = runtime.RunString(`
		var mathEl = {
			_classes: [],
			dataset: {},
			classList: { add: function (c) { mathEl._classes.push(c); } },
		};
		katexEls.push(mathEl);

		var preEl = { _classes: [], classList: { add: function (c) { preEl._classes.push(c); } } };
		var codeEl = {
			_classes: [],
			dataset: {},
			classList: { add: function (c) { codeEl._classes.push(c); } },
			closest: function (sel) { return sel === 'pre' ? preEl : null; },
		};
		codeEls.push(codeEl);

		listeners['eitri:katex-load-failed']();
		listeners['eitri:prism-load-failed']();
		var outMathClasses = mathEl._classes;
		var outMathDataset = mathEl.dataset;
		var outPreClasses = preEl._classes;
		var outCodeDataset = codeEl.dataset;
	`)
	if err != nil {
		t.Fatalf("failed to fire load-failed events: %v", err)
	}

	var mathClasses, preClasses []string
	var mathDataset, codeDataset map[string]interface{}
	if err := runtime.ExportTo(runtime.Get("outMathClasses"), &mathClasses); err != nil {
		t.Fatalf("failed to export math classes: %v", err)
	}
	if err := runtime.ExportTo(runtime.Get("outMathDataset"), &mathDataset); err != nil {
		t.Fatalf("failed to export math dataset: %v", err)
	}
	if err := runtime.ExportTo(runtime.Get("outPreClasses"), &preClasses); err != nil {
		t.Fatalf("failed to export pre classes: %v", err)
	}
	if err := runtime.ExportTo(runtime.Get("outCodeDataset"), &codeDataset); err != nil {
		t.Fatalf("failed to export code dataset: %v", err)
	}
	if !slices.Contains(mathClasses, "math-error") {
		t.Errorf("math classes %v missing math-error", mathClasses)
	}
	if mathDataset["katexProcessed"] != "true" {
		t.Errorf("katexProcessed = %v, want true", mathDataset["katexProcessed"])
	}
	if !slices.Contains(preClasses, "code-error") {
		t.Errorf("pre classes %v missing code-error", preClasses)
	}
	if codeDataset["prismProcessed"] != "true" {
		t.Errorf("prismProcessed = %v, want true", codeDataset["prismProcessed"])
	}
}

// TestAccumulateStreamAnnounce verifies the pure delta bookkeeping behind the
// screen-reader stream announcer: each call returns the existing pending text
// plus only the *new* unannounced bytes of the stream buffer — never the whole
// accumulated reply — so screen readers announce the reply in delta chunks at
// the throttled cadence instead of re-reading the full stream every 80ms flush
// (issue #1071).
func TestAccumulateStreamAnnounce(t *testing.T) {
	f, err := Files.Open("eitri-stream-announcer.js")
	if err != nil {
		t.Fatalf("failed to open eitri-stream-announcer.js: %v", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read eitri-stream-announcer.js: %v", err)
	}
	content := string(data)

	fnSrc := extractFunctionBody(t, content, "function accumulateStreamAnnounce(streamBuf, lastAnnouncedLen, pending) {")

	runtime := goja.New()
	if _, err := runtime.RunString(fnSrc); err != nil {
		t.Fatalf("failed to parse accumulateStreamAnnounce: %v", err)
	}

	// goja cannot map a JS object return into a Go struct through ExportTo, so
	// export as map[string]interface{} (the same extraction machinery that
	// TestLightweightMarkdown uses for string returns).
	var fn func(string, int, string) map[string]interface{}
	if err := runtime.ExportTo(runtime.Get("accumulateStreamAnnounce"), &fn); err != nil {
		t.Fatalf("failed to export accumulateStreamAnnounce: %v", err)
	}

	pendingOf := func(m map[string]interface{}) string {
		if v, ok := m["pending"].(string); ok {
			return v
		}
		return ""
	}
	lenOf := func(m map[string]interface{}) int {
		if v, ok := m["lastAnnouncedLen"].(int64); ok {
			return int(v)
		}
		return 0
	}

	t.Run("first call returns full delta and advances offset", func(t *testing.T) {
		got := fn("hello world", 0, "")
		if pendingOf(got) != "hello world" {
			t.Errorf("pending = %q, want %q", pendingOf(got), "hello world")
		}
		if lenOf(got) != 11 {
			t.Errorf("lastAnnouncedLen = %d, want 11", lenOf(got))
		}
	})

	t.Run("no new text produces no duplicate announcement", func(t *testing.T) {
		got := fn("hello world", 11, "hello world")
		if pendingOf(got) != "hello world" {
			t.Errorf("pending must be unchanged when buffer did not grow, got %q", pendingOf(got))
		}
		if lenOf(got) != 11 {
			t.Errorf("lastAnnouncedLen must stay 11, got %d", lenOf(got))
		}
	})

	t.Run("new text appends only the delta", func(t *testing.T) {
		got := fn("hello world, this is more", 11, "hello world")
		if pendingOf(got) != "hello world, this is more" {
			t.Errorf("pending = %q, want %q", pendingOf(got), "hello world, this is more")
		}
		if lenOf(got) != 25 {
			t.Errorf("lastAnnouncedLen = %d, want 25", lenOf(got))
		}
	})

	t.Run("empty buffer is a no-op", func(t *testing.T) {
		got := fn("", 0, "")
		if pendingOf(got) != "" {
			t.Errorf("pending = %q, want empty", pendingOf(got))
		}
		if lenOf(got) != 0 {
			t.Errorf("lastAnnouncedLen = %d, want 0", lenOf(got))
		}
	})
}

// TestStreamModulesLoadTogether verifies the eitri-stream module files (issue
// #1113) boot in dependency order under a minimal DOM stub without throwing —
// a smoke test for cross-module references on the shared window.eitriStream
// runtime (the primary regression risk of splitting the single island).
func TestStreamModulesLoadTogether(t *testing.T) {
	runtime := goja.New()
	_, err := runtime.RunString(`
		globalThis.window = {};
		// Minimal stubs used only at module-load time (event registration and
		// the shared session-id helper used by the runtime bootstrap).
		globalThis.document = {
			readyState: 'loading',
			addEventListener: function () {},
			createElement: function () { return { appendChild: function () {} }; },
			body: { appendChild: function () {} },
			querySelector: function () { return null; },
			querySelectorAll: function () { return []; },
			getElementById: function () { return null; },
		};
		globalThis.window.eitriGetSessionId = function () { return 'sess123'; };
	`)
	if err != nil {
		t.Fatalf("failed to set up DOM stub: %v", err)
	}

	for _, name := range []string{
		"eitri-stream-common.js",
		"eitri-stream-toolcards.js",
		"eitri-stream-announcer.js",
		"eitri-stream-tokens.js",
		"eitri-stream-confirmation.js",
		"eitri-stream-scroll.js",
		"eitri-stream-render.js",
		"eitri-stream.js",
	} {
		data, err := Files.ReadFile(name)
		if err != nil {
			t.Fatalf("failed to read %s: %v", name, err)
		}
		if _, err := runtime.RunString(string(data)); err != nil {
			t.Fatalf("failed to load %s: %v", name, err)
		}
	}

	// The runtime must exist and expose the functions the modules share.
	rt := runtime.Get("window").ToObject(runtime).Get("eitriStream")
	if rt == nil || rt.ToObject(runtime) == nil {
		t.Fatal("window.eitriStream runtime not created after loading stream modules")
	}
	ru := rt.ToObject(runtime)
	for _, want := range []string{
		"STATES", "streams", "createStreamState", "lightweightMarkdown",
		"injectToolCardSlot", "renderToolCard", "appendToken",
		"flushStreamBuffer", "flushStreamAnnounce", "showStreamingBubble",
		"finalizeMessage", "autoScroll", "insertOptimisticBubble",
	} {
		if v := ru.Get(want); v == nil || (goja.IsUndefined(v)) {
			t.Errorf("window.eitriStream.%s missing after module load", want)
		}
	}
	// The scroll module must have wired the orchestrator hooks.
	if v := ru.Get("onTokenActivity"); v == nil || goja.IsUndefined(v) {
		t.Error("window.eitriStream.onTokenActivity hook not registered by eitri-stream-scroll.js")
	}
}
