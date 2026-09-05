package tui

import (
	"strconv"
	"testing"
)

// BenchmarkPausedStreamViewWithFace covers the complete visible surface while
// a reader has paused follow during a live stream, including the right rail.
func BenchmarkPausedStreamViewWithFace(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	b.Setenv("EITRI_KITTY_IMAGES", "1")
	for _, turns := range []int{200, 800} {
		b.Run("turns_"+strconv.Itoa(turns), func(b *testing.B) {
			tx := pausedStreamRenderTranscript(b, turns)
			tx.rail = NewRail("provider", "model", "medium", true, "session", "/tmp/session")
			tx.railWidth = defaultRailWidth
			bandHeight := 5
			pane := tx.renderHistoryPane(bandHeight)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tx.viewWithRail(pane, bandHeight)
			}
		})
	}
}
