package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

func TestBusyLiveTailReusesUnchangedRenderedMarkdown(t *testing.T) {
	tx := benchBusyTx()
	tx.cotExpanded = true
	benchBusyLive(tx, 2000)
	// Deterministic clock advancing 200ms per read: the changed-window renders
	// below must always land past liveMarkdownMinRenderInterval so the throttle
	// never suppresses them.
	var tnow time.Time
	tx.liveMarkdownCache.clock = func() time.Time {
		tnow = tnow.Add(200 * time.Millisecond)
		return tnow
	}

	first := tx.renderPaneContent()
	second := tx.renderPaneContent()
	if first != second {
		t.Fatalf("unchanged live turn render changed between frames")
	}
	if tx.liveMarkdownCache.misses != 1 {
		t.Fatalf("first live render should render markdown once, got %d misses", tx.liveMarkdownCache.misses)
	}
	if tx.liveMarkdownCache.hits == 0 {
		t.Fatalf("second live render should reuse rendered markdown")
	}

	tx.flow.Observe(ReasoningStream, "new token")
	tx.messages[len(tx.messages)-1].reasoning += "new token"
	third := tx.renderPaneContent()
	if third == second || !strings.Contains(plain(third), "new token") {
		t.Fatalf("changed live reasoning did not render new content")
	}
	if tx.liveMarkdownCache.misses != 2 {
		t.Fatalf("changed live reasoning should render markdown once more, got %d misses", tx.liveMarkdownCache.misses)
	}
}

func TestStreamingWindowedReasoningRendersTail(t *testing.T) {
	// A reason blob whose head is unique so a windowed render only shows the tail.
	head := strings.Repeat("HEADMARKER-unique ", 40)
	tail := strings.Repeat("tail words repeated here ", 40)
	tx := benchBusyTx()
	tx.configTheme = config.DefaultTheme
	reason := head + strings.Repeat(tail, 3000) // well past liveStreamingMarkdownWindow
	tx.messages = append(tx.messages, message{role: "you", content: "live prompt"})
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: true,
		reasoning: reason, content: "", expansion: ExpansionState{}})
	tx.flow.Observe(ReasoningStream, reason)
	tx.BeginBusy()

	nl := func(s string) int { return strings.Count(s, "\n") }
	// Advance a fast clock each read so the throttle never holds a stale body:
	// this test is about the render *window* staying bounded, not coalescing
	// (covered by TestLiveMarkdownThrottleBoundsFastStream).
	var tnow time.Time
	tx.liveMarkdownCache.clock = func() time.Time {
		tnow = tnow.Add(200 * time.Millisecond)
		return tnow
	}
	prev := tx.renderPaneContent()
	if strings.Contains(plain(prev), "HEADMARKER") {
		t.Fatalf("streaming windowed render must not include the reasoning head, got a window too wide")
	}
	if !strings.Contains(plain(prev), "tail words") {
		t.Fatalf("streaming windowed render must include the reasoning tail")
	}
	// Growing the streamed reasoning shifts the window but must keep the frame
	// bounded: appending deltas stays within the window, not the full blob.
	for i := 0; i < 50; i++ {
		tx.flow.Observe(ReasoningStream, " more token ")
		tx.messages[len(tx.messages)-1].reasoning += " more token "
		nxt := tx.renderPaneContent()
		if nl(nxt) > nl(prev)+200 {
			t.Fatalf("streaming window growth blew past the window budget (newlines %d -> %d)", nl(prev), nl(nxt))
		}
	}
}
func TestBusyLiveTailCacheDoesNotChangeRenderedOutput(t *testing.T) {
	cachedTx := benchBusyTx()
	cachedTx.cotExpanded = true
	benchBusyLive(cachedTx, 2000)
	cached := cachedTx.renderPaneContent()

	uncachedTx := benchBusyTx()
	uncachedTx.cotExpanded = true
	benchBusyLive(uncachedTx, 2000)
	uncached := uncachedTx.renderLiveTail()

	if cached != uncached {
		t.Fatalf("cached live-tail render changed output")
	}
}

// TestPaneVariantReRender: the pane id is part of the cache key, so a
// streaming -> committed variant change re-renders once instead of reusing the
// other pane's body.
func TestPaneVariantReRender(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	tx := benchBusyTx()
	tx.configTheme = config.DefaultTheme
	c := &tx.liveMarkdownCache
	// Each new render lands one interval later, so the throttle never suppresses
	// the pane-variant re-render.
	var tnow time.Time
	c.clock = func() time.Time {
		tnow = tnow.Add(200 * time.Millisecond)
		return tnow
	}
	text := strings.Repeat("md inline `code` and **bold** here. ", 40)

	s1 := c.renderPaneBody(text, 118, config.DefaultTheme, mdPaneStreamingThinking, th, false, "")
	mAfterStream := c.misses
	// Stable frames under the same pane must be cache hits.
	s2 := c.renderPaneBody(text, 118, config.DefaultTheme, mdPaneStreamingThinking, th, false, "")
	if c.misses != mAfterStream {
		t.Fatalf("same-pane stable frame re-rendered markdown")
	}
	if s1 != s2 {
		t.Fatalf("same-pane unchanged body drifted")
	}

	// A different pane variant re-renders exactly once; a stale body must not
	// leak under the new pane's border.
	s3 := c.renderPaneBody(text, 118, config.DefaultTheme, mdPaneThinking, th, false, "")
	if c.misses != mAfterStream+1 {
		t.Fatalf("pane-variant change should render exactly once more, got %d (after %d)", c.misses, mAfterStream)
	}
	if s3 == s1 {
		t.Fatalf("pane-variant change must not reuse the other pane's body")
	}
}

// TestLiveMarkdownThrottleBoundsFastStream is the regression guard for the
// live-stream CPU fix: distinct large windows arriving inside
// liveMarkdownMinRenderInterval are served from the cached body and re-render only
// once the interval elapses. Only windowed=true streaming windows are held; small
// live blocks still update every delta.
func TestLiveMarkdownThrottleNeverServesAnotherBlock(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	c := &liveMarkdownCache{}
	now := time.Time{}
	c.clock = func() time.Time { return now }

	first := c.renderPaneBody("first streaming block", 118, "dark", mdPaneStreamingThinking, th, true, "first")
	now = now.Add(time.Millisecond)
	second := c.renderPaneBody("second streaming block", 118, "dark", mdPaneStreamingThinking, th, true, "second")

	if first == second || !strings.Contains(plain(second), "second streaming block") {
		t.Fatalf("throttled key miss served another block's output: %q", plain(second))
	}
	if c.misses != 2 {
		t.Fatalf("different streaming blocks must both render, got %d misses", c.misses)
	}
}

func TestLiveMarkdownThrottleBoundsFastStream(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	c := &liveMarkdownCache{}
	var now time.Time
	step := 10 * time.Millisecond
	c.clock = func() time.Time { now = now.Add(step); return now }

	// Large enough to cross the streaming window bound.
	base := strings.Repeat("streaming reasoning token mix of prose and markdown \n", 900)
	for i := 0; i < 5; i++ {
		c.renderPaneBody(base+string(rune('a'+i)), 118, "dark", mdPaneStreamingThinking, th, true, "reasoning:0:0")
	}
	if len(base) < liveStreamingMarkdownWindow {
		t.Fatalf("test window too small: got %d, want >= %d", len(base), liveStreamingMarkdownWindow)
	}
	// Five distinct windows inside 50ms, well under the 100ms interval: only the
	// first re-renders.
	if c.misses != 1 {
		t.Fatalf("fast burst inside the render interval re-rendered %d times, want 1", c.misses)
	}
	if c.hits < 4 {
		t.Fatalf("fast burst should be served from cache, got %d hits", c.hits)
	}

	// Let the interval elapse: the next distinct window re-renders once.
	now = now.Add(liveMarkdownMinRenderInterval)
	c.renderPaneBody(base+"z", 118, "dark", mdPaneStreamingThinking, th, true, "reasoning:0:0")
	if c.misses != 2 {
		t.Fatalf("after the interval a changed window should re-render, got %d misses", c.misses)
	}
}

// TestLiveLargeStreamingReasoningThrottlesThroughRenderPath drives the real
// reasoningBlock -> liveStreamingText -> renderPaneBody path with a blob already
// past liveStreamingMarkdownWindow. Before the fix the throttle gate read the
// post-trim window length — which liveStreamingText trims below the bound — so
// every delta re-glamed the full window; the throttle flag must instead be derived
// from the windowed decision, holding the body to roughly one re-render per
// interval.
func TestLiveLargeStreamingReasoningThrottlesThroughRenderPath(t *testing.T) {
	tx := benchBusyTx()
	// Fast clock: many deltas arrive inside liveMarkdownMinRenderInterval, so the
	// throttle must coalesce them (serve the stale body) rather than re-render.
	var now time.Time
	step := 5 * time.Millisecond
	tx.liveMarkdownCache.clock = func() time.Time { now = now.Add(step); return now }

	big := strings.Repeat("streaming reasoning prose with markdown `code` and newline\n", 1200) // ~70KB, past window
	tx.messages = append(tx.messages, message{role: "you", content: "live prompt"})
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: true,
		reasoning: big, content: "", expansion: ExpansionState{}})
	tx.flow.Observe(ReasoningStream, big)
	tx.BeginBusy()

	tx.renderPaneContent()
	missesAfterPrime := tx.liveMarkdownCache.misses
	if missesAfterPrime == 0 {
		t.Fatalf("large streaming reasoning should have rendered at least once")
	}

	// Each delta changes the window, but the throttle coalesces inside the
	// interval, so re-renders stay flat.
	for i := 0; i < 5; i++ {
		tx.flow.Observe(ReasoningStream, " more token ")
		tx.messages[len(tx.messages)-1].reasoning += " more token "
		tx.renderPaneContent()
	}
	if tx.liveMarkdownCache.misses != missesAfterPrime {
		t.Fatalf("fast large-stream deltas re-rendered (misses %d -> %d); throttle should have held the body",
			missesAfterPrime, tx.liveMarkdownCache.misses)
	}
}
