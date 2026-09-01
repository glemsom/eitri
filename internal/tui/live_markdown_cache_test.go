package tui

import (
	"strings"
	"testing"
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
