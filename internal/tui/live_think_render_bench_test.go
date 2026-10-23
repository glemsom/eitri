package tui

import (
	"strings"
	"testing"
)

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

// BenchmarkLiveThinkingRender measures the per-delta cost of the production live
// reasoning render (renderPaneBodyFresh -> renderLiveThoughtBody) over a fixed
// ~9KiB window, the largest a live thinking block reaches before
// liveStreamingText caps the tail at liveStreamingMarkdownWindow. The fixture is
// built outside the timed loop so the measured cost is the render alone.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkLiveThinkingRender -benchtime 100x
func BenchmarkLiveThinkingRender(b *testing.B) {
	const width = 120
	blob := buildLiveThinkBlob(9 << 10)
	th := themeFor("dark")
	theme := "dark"
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// A rotating suffix byte changes the windowed text each frame, so the
		// cache always misses and the hard-wrap is what runs.
		_ = renderPaneBodyFresh(blob+string(rune('a'+i%26)), width-2, theme, mdPaneStreamingThinking, th)
	}
}
