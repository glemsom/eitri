package tui

import (
	"strconv"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPausedStreamRenderKeepsViewportCachedAcrossDeltas(t *testing.T) {
	tx := pausedStreamRenderTranscript(t, 200)
	builds := tx.layout.builds
	if builds == 0 {
		t.Fatal("wheel pause must sync the viewport and build the layout once")
	}
	before := tx.histViewport.View()

	tx.syncStreamSnapshots(len(tx.messages)-1, "new streamed token", "")
	got := tx.renderHistoryPane(5)

	if tx.layout.builds != builds {
		t.Fatalf("paused stream delta rebuilt full history: builds %d -> %d", builds, tx.layout.builds)
	}
	if got != before {
		t.Fatalf("paused viewport changed while new stream output arrived below it")
	}
}

// BenchmarkPausedStreamRender_PerDelta covers the mouse-wheel-while-streaming
// path: once the reader scrolls away, later deltas must not re-feed the full
// transcript into the viewport just to preserve the paused reading position.
func BenchmarkPausedStreamRender_PerDelta(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, turns := range []int{200, 800} {
		b.Run("turns_"+strconv.Itoa(turns), func(b *testing.B) {
			tx := pausedStreamRenderTranscript(b, turns)
			reserved := 5

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tx.renderHistoryPane(reserved)
			}
		})
	}
}

func pausedStreamRenderTranscript(tb testing.TB, turns int) *Transcript {
	tb.Helper()
	tx := benchBusyTx()
	benchBusyHistory(tx, turns)
	benchBusyLive(tx, 2000)
	reserved := 5

	_ = tx.renderHistoryPane(reserved) // follow fast path leaves viewport stale.
	tx.navigateMouse(wheelMsg(true).(tea.MouseWheelMsg))
	if tx.histFollow {
		tb.Fatal("wheel up must pause follow")
	}
	return tx
}
