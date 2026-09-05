package tui

import (
	"strings"
	"testing"
)

// TestTranscript_liveTokenDeltasCoalesceToOneCard is the regression lock for the
// per-token-card bug: a real reasoning provider streams reasoning_content as
// many tiny SSE deltas (often one token per event), so each delta used to paint
// its own "🤔 N tok" header — the user saw a fresh card per token. Contiguous
// deltas must instead coalesce into ONE card.
func TestTranscript_liveTokenDeltasCoalesceToOneCard(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	// ~17 tokens of reasoning streamed as 17 single-token deltas.
	deltas := strings.Fields("Let me check the environment first before editing the file carefully")
	tx := livePerDeltaTranscript(deltas)

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	// The bodies must all be visible (live auto-expand) so the user watches CoT
	// progress, but they must live under ONE reasoning header.
	for _, d := range deltas {
		if !strings.Contains(plain, d) {
			t.Errorf("live reasoning body missing token %q, got:\n%s", d, plain)
		}
	}
	if n := strings.Count(plain, "tok"); n != 1 {
		t.Errorf("live token-streamed reasoning must render ONE '🤔 N tok' header, got %d headers:\n%s", n, plain)
	}
}
