package tui

import (
	"strings"
	"testing"
)

// buildLiveThinkBlob returns a realistic streaming long chain-of-thought body:
// mixed prose sentences, headings, inline and fenced code, bullet and numbered
// lists, and punctuation, sized to about wantBytes. The shape mirrors what a
// real model actually streams during a long CoT turn (the input the current
// glamour+goldmark live render pays full price to parse every delta).
func buildLiveThinkBlob(wantBytes int) string {
	prose := []string{
		"Let me reconsider the request carefully before I answer, because the phrasing is ambiguous and I want to be sure I solve the real problem and not a strawman of it.",
		"First I should check whether the module boundary already gives me a seam, and whether a cheaper approach would keep the committed output byte-identical.",
		"The interface here is a natural cut: callers hand me a pane id and windowed text, and I hand back wrapped bytes, so swapping the body renderer stays local.",
		"I also need to weigh the allocation profile, since a fast stream coalesces deltas and each coalesced render should stay well under a millisecond.",
		"Hmm, wait -- that assumption only holds while the block stays under the window bound, so I should double check the trailing-window logic trims on a newline boundary.",
	}
	var b strings.Builder
	// Interleave structured markdown with the prose so the blob exercises every
	// glamour code path a real CoT does, not a single homogeneous paragraph.
	paras := []string{
		"## Step 1: frame the problem",
		"- the requirement is to bound per-frame cost",
		"- the constraint is committed-output parity",
		"1. render cheaply while live",
		"2. fall back to glamour once committed",
		"```go\nfunc wrap(s string, w int) []string {\n\t// hard wrap without breaking the stream\n}\n```",
		"Inline identifiers like `liveStreamingText` and `RenderMarkdown` plus `renderPaneBodyFresh` keep appearing in the tail.",
	}
	for b.Len() < wantBytes {
		paras = append(paras, prose[b.Len()%len(prose)])
		b.WriteString(paras[len(paras)-1])
		b.WriteString("\n\n")
	}
	return b.String()
}

// BenchmarkLiveThinkingRender is the glamour-baseline regression benchmark
// (scratch issue 01). It measures the per-delta cost of rendering a realistic
// streaming long chain-of-thought thinking body through the production live
// markdown path -- the glamour+goldmark body wrap behind
// liveMarkdownCache.renderPaneBody / renderPaneBodyFresh that currently runs
// on every streaming delta before the cheap-renderer work (scratch issue 02)
// replaces the live body with an ANSI word-wrap.
//
// The fixture is a realistic ~9KiB mixed-prose/code reasoning window (the
// largest a live thinking block reaches before liveStreamingText caps the tail
// at liveStreamingMarkdownWindow), built once outside the timed loop so the
// measured cost is the render alone, not fixture construction. Each iteration
// renders the production path (renderPaneBodyFresh -> RenderMarkdown) on that
// fixed window plus one rotating suffix byte, forcing a genuine glamour
// re-render (cache-key miss) per frame just as a real streaming delta does.
// This number is today's baseline; issue 02's cheap renderer must beat it by
// an order of magnitude.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkLiveThinkingRender -benchtime 100x
func BenchmarkLiveThinkingRender(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	const width = 120
	// One realistic ~9KiB windowed reasoning body, reused across iterations.
	blob := buildLiveThinkBlob(9 << 10)
	th := themeFor("dark")
	theme := "dark"
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A rotating suffix byte changes the windowed text each frame, so the
		// cache always misses and the glamour+goldmark re-parse is what runs --
		// the exact per-delta cost issue 02 must remove.
		_ = renderPaneBodyFresh(blob+string(rune('a'+i%26)), width-2, theme, mdPaneStreamingThinking, th)
	}
}
