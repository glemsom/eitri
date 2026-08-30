package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// BenchmarkBusyRender_* is the regression guard for the lazy-CoT fix
// (issue #661): while a turn is busy, per-delta rendering must scale with the
// live turn's size, not with total history size. Before the fix the busy path
// re-rendered the whole committed history each delta (~quadratic); after it the
// committed prefix is cached and only the live tail re-renders. The 800-turn
// case must land near the live-turn floor, not ~4x the 200-turn cost.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkBusyRender -benchtime 30x
func BenchmarkBusyRender_PerDelta(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, turns := range []int{200, 800} {
		b.Run("turns_"+strconv.Itoa(turns), func(b *testing.B) {
			tx := benchBusyTx()
			benchBusyHistory(tx, turns)
			benchBusyLive(tx, 2000)

			// Prime the committed-prefix cache so the measured loop is the
			// per-delta live-tail cost (mirrors steady-step streaming).
			tx.renderPaneContent()
			tx.busyPrefixDirty = false

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tx.renderPaneContent()
			}
		})
	}
}

// BenchmarkBusyRender_LiveTailFloor is the post-fix per-delta floor with no
// committed history: the live-tail render alone. The 800-turn result should sit
// close to this, demonstrating independence from history size.
func BenchmarkBusyRender_LiveTailFloor(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := benchBusyTx()
	benchBusyLive(tx, 2000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tx.renderPaneContent()
	}
}

func benchBusyTx() *Transcript {
	tx := &Transcript{}
	tx.theme = themeFor(config.DefaultTheme)
	tx.configTheme = config.DefaultTheme
	tx.layout = transcriptLayout{dirty: true}
	tx.width = 120
	tx.height = 30
	tx.cotExpanded = true
	tx.histViewport = newHistoryViewport()
	tx.busy = true
	return tx
}

func benchBusyHistory(tx *Transcript, turns int) {
	for i := 0; i < turns; i++ {
		tx.messages = append(tx.messages, message{role: "you", content: "a moderately long user prompt describing a task"})
		tx.messages = append(tx.messages, message{
			role:    "eitri",
			content: "a reasonably sized assistant answer body for this turn",
			events:  synthAnswerLog("a reasonably sized assistant answer body for this turn"),
		})
	}
}

func benchBusyLive(tx *Transcript, cotLen int) {
	tx.messages = append(tx.messages, message{role: "you", content: "live prompt"})
	tx.messages = append(tx.messages, message{
		role:              "eitri",
		streaming:         true,
		thinkingRequested: true,
		reasoning:         strings.Repeat("chain of thought reasoning tokens and analysis  ", 4*(cotLen/50))[:cotLen],
		expansion:         ExpansionState{},
	})
	s := NewTurnSession(nil)
	s.flow.Observe(ReasoningStream, strings.Repeat("chain of thought reasoning tokens and analysis  ", 4*(cotLen/50))[:cotLen])
	tx.live = s
}
