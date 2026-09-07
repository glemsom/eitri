package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

func TestBusyLiveTailReusesUnchangedRenderedMarkdown(t *testing.T) {
	tx := benchBusyTx()
	tx.cotExpanded = true
	benchBusyLive(tx, 2000)

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

	tx.live.flow.Observe(ReasoningStream, "new token")
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
	s := NewTurnSession(nil)
	s.flow.Observe(ReasoningStream, reason)
	tx.live = s
	tx.busy = true

	nl := func(s string) int { return strings.Count(s, "\n") }
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
		tx.live.flow.Observe(ReasoningStream, " more token ")
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
