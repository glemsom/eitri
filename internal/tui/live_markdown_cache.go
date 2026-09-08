package tui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// liveMarkdownMinRenderInterval bounds how often the live streaming block's
// expensive glamour+goldmark render may run. Streaming reasoning/answer grows
// the accumulated block every DrainReady batch, so the cache key changes each
// batch and every frame would otherwise re-parse the whole window (a real
// 8KiB block costs ~7ms in goldmark+glamour+remap). During a fast stream a
// batch arrives every few ms; without throttling one core is pinned. Rendering
// the live tail (a trailing window; the user watches the moving tail, not the
// stable head) at most every interval bounds CPU while the stream still looks
// live: deltas coalesce, nothing loses bytes — the next allowed render just
// spans more of them.
const liveMarkdownMinRenderInterval = 100 * time.Millisecond

// liveMarkdownCache caches the glamour markdown render AND its final pane-wrapped
// body for a streaming block, keyed on the rendered windowed text plus an explicit
// pane identifier. Reasoning/answer streams grow with every delta, and a frame
// re-renders the live tail; caching only the glamour output still left the pane
// border+padding re-wrap (a full width+width-measure pass over the window)
// running on every frame even when the window did not change, which dominates
// CPU during a burst because frames outnumber deltas (DrainReady batches,
// spinner/face ticks between batches). Caching the finished pane body makes
// unchanged-window frames a single slot hit.
type liveMarkdownCache struct {
	key       liveMarkdownCacheKey
	out       string
	valid     bool
	hits      int
	misses    int
	lastFresh time.Time
	// clock, when non-nil, overrides time.Now so tests exercise the throttle
	// deterministically without wall-clock sleeps.
	clock func() time.Time
}

// liveMarkdownCacheKey identifies one cache slot: the fully windowed text that
// was rendered, the wrap width, the theme, and the pane variant id. paneID
// disambiguates the pane-adjacent style variants so a changed (e.g. live ->
// committed) border is not served stale body bytes. Pane identity is passed
// explicitly from the call site rather than derived from the style value: the
// lipgloss Style's only string form forces a full render, too expensive.
type liveMarkdownCacheKey struct {
	text   string
	width  int
	theme  string
	paneID int
}

// liveMarkdownPaneIDs enumerate the distinct pane bodies the cache may hold,
// mirroring the theme's pane variants used by the reasoning/answer flow.
type liveMarkdownPaneID int

const (
	mdPaneThinking liveMarkdownPaneID = iota
	mdPaneStreamingThinking
	mdPaneAgent
	mdPaneStreaming
	mdPaneError
	mdPaneStreamingError
	mdPaneStopped
)

// paneStyleFor maps the pane identifier onto the theme's actual style. Callers
// choose a pane id matching the message state (streaming vs committed, error vs
// normal) so the key never serves stale body bytes across a state change.
func (th Theme) paneStyleFor(id liveMarkdownPaneID) lipgloss.Style {
	switch id {
	case mdPaneThinking:
		return th.thinkingPaneStyle
	case mdPaneStreamingThinking:
		return th.streamingThinkingPaneStyle
	case mdPaneAgent:
		return th.agentPaneStyle
	case mdPaneStreaming:
		return th.streamingPaneStyle
	case mdPaneError:
		return th.errorPaneStyle
	case mdPaneStreamingError:
		return th.streamingErrorPaneStyle
	case mdPaneStopped:
		return th.stoppedPaneStyle
	}
	return th.thinkingPaneStyle
}

// renderPaneBody returns the pane-wrapped markdown body for the given windowed
// text, using cache when the installed key matches (same text+strip+theme+pane).
// When no cache is provided (committed/legacy paths) it renders directly, so the
// cached and fresh output cannot drift.
//
// throttle controls the re-render throttle: when true, this is a live streaming
// window (a long reasoning/answer block whose trailing text re-parses each
// frame) and the expensive glamour re-render is bound to
// liveMarkdownMinRenderInterval. It is passed explicitly from the caller rather
// than derived from the text length because the caller trims the window to the
// first newline (see liveStreamingText), so a throttled window's length lands
// under liveStreamingMarkdownWindow and a length-based gate would never fire.
// Small non-throttled blocks still render per frame, so routine short reasoning
// and model tests see each delta immediately.
//
// now returns the cache's clock source: c.clock when injected for tests, else
// time.Now.
func (c *liveMarkdownCache) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

// renderPaneBody returns the pane-wrapped markdown body for the given windowed
// text, using cache when the installed key matches (same text+strip+theme+pane).
// When no cache is provided (committed/legacy paths) it renders directly, so the
// cached and fresh output cannot drift.
//
// A changed window is the only path that re-parses markdown. That render is
// throttled to liveMarkdownMinRenderInterval: if the window changed but the
// last fresh render is recent, the previous body is returned (its hits counter
// still advances) and the whole change batch is absorbed into the next allowed
// render. This bounds goldmark+glamour cost during a fast stream while keeping
// the visible tail live — the window slides, never drops bytes.
func (c *liveMarkdownCache) renderPaneBody(text string, width int, theme string, paneID liveMarkdownPaneID, th Theme, throttle bool) string {
	if c == nil {
		return renderPaneBodyFresh(text, width, theme, paneID, th)
	}
	key := liveMarkdownCacheKey{text: text, width: width, theme: theme, paneID: int(paneID)}
	if c.valid && c.key == key {
		c.hits++
		return c.out
	}
	// Throttle: hold the previous render when the expensive re-render would run
	// too soon after the last one. Only throttled streaming windows are held:
	// small blocks render so cheaply that live per-frame updates are worth it,
	// and model tests and routine short reasoning depend on seeing each delta
	// immediately. Once a block crosses the window, rendering it from scratch
	// each frame is what pins a core, so the stale body (the prior window) is
	// served between intervals. The stream briefly lags at most
	// liveMarkdownMinRenderInterval behind; it is never dropped, and coalescing
	// turns a burst of deltas into one render.
	if c.valid && liveMarkdownMinRenderInterval > 0 && throttle &&
		c.now().Sub(c.lastFresh) < liveMarkdownMinRenderInterval {
		c.hits++ // a cheap slot hit: the bytes drawn are slightly stale, but no re-parse
		return c.out
	}
	body := renderPaneBodyFresh(text, width, theme, paneID, th)
	c.key = key
	c.out = body
	c.valid = true
	c.lastFresh = c.now()
	c.misses++
	return body
}

// renderPaneBodyFresh renders the windowed markdown through glamour then wraps
// it in the chosen pane. The markdown is always re-rendered here because the
// windowed text changed (cache key miss); an unchanged re-glam cannot happen
// through the cache front door.
func renderPaneBodyFresh(text string, width int, theme string, paneID liveMarkdownPaneID, th Theme) string {
	// Streaming reasoning/answer panes render their body with the cheap ANSI
	// word-wrap instead of the full glamour+goldmark pipeline: a live block
	// re-renders its tail every delta, so per-delta cost must drop by an order
	// of magnitude (scratch issue 02). Committed, error, and stopped panes keep
	// the full glamour render so committed output does not diverge.
	if paneID == mdPaneStreamingThinking || paneID == mdPaneStreaming {
		pane := th.paneStyleFor(paneID)
		return pane.Render(renderCheapLiveBody(text, width))
	}
	md, _ := RenderMarkdown(text, width, theme)
	pane := th.paneStyleFor(paneID)
	return pane.Render(trimBody(md))
}

// trimBody trims trailing newlines the way the pane callers did, so the cached
// pane body matches a fresh render byte-for-byte.
func trimBody(md string) string {
	return strings.TrimRight(md, "\n")
}
