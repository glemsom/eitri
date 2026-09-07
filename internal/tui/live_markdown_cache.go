package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

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
	key    liveMarkdownCacheKey
	out    string
	valid  bool
	hits   int
	misses int
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
func (c *liveMarkdownCache) renderPaneBody(text string, width int, theme string, paneID liveMarkdownPaneID, th Theme) string {
	if c == nil {
		return renderPaneBodyFresh(text, width, theme, paneID, th)
	}
	key := liveMarkdownCacheKey{text: text, width: width, theme: theme, paneID: int(paneID)}
	if c.valid && c.key == key {
		c.hits++
		return c.out
	}
	body := renderPaneBodyFresh(text, width, theme, paneID, th)
	c.key = key
	c.out = body
	c.valid = true
	c.misses++
	return body
}

// renderPaneBodyFresh renders the windowed markdown through glamour then wraps
// it in the chosen pane. The markdown is always re-rendered here because the
// windowed text changed (cache key miss); an unchanged re-glam cannot happen
// through the cache front door.
func renderPaneBodyFresh(text string, width int, theme string, paneID liveMarkdownPaneID, th Theme) string {
	md, _ := RenderMarkdown(text, width, theme)
	pane := th.paneStyleFor(paneID)
	return pane.Render(trimBody(md))
}

// trimBody trims trailing newlines the way the pane callers did, so the cached
// pane body matches a fresh render byte-for-byte.
func trimBody(md string) string {
	return strings.TrimRight(md, "\n")
}
