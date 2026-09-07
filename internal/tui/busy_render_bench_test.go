package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// BenchmarkBusyRender_* is the regression guard for the lazy-CoT fix
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

// benchStreamingLiveReason returns a transcript whose one live turn carries a
// reasoning block of reasonKB KiB. Streaming reasoning auto-expands (thinkingExpandedForBlock)
// regardless of the collapsed-by-default config, so the expanded markdown pane is
// (re)rendered per frame — the path that previously grew super-linear with stream
// length and pinned a core on a long CoT passthrough. Per-frame cost must stay
// flat (bounded by liveStreamingMarkdownWindow) as reasonKB grows.
func benchStreamingLiveReason(reasonKB int) *Transcript {
	tx := benchBusyTx()
	tx.configTheme = config.DefaultTheme
	reason := strings.Repeat("paragraph of analysis and reasoning tokens repeated some words here ", 2000)[:reasonKB*1000]
	tx.messages = append(tx.messages, message{role: "you", content: "live prompt"})
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: true,
		reasoning: reason, content: "", expansion: ExpansionState{}})
	s := NewTurnSession(nil)
	s.flow.Observe(ReasoningStream, reason)
	tx.live = s
	tx.busy = true
	return tx
}

// BenchmarkStreamingAutoExpandReason is the regression guard for the live-streaming
// markdown window fix: rendering a streaming auto-expanded reasoning block must cost
// a constant amount no matter how long the single turn's CoT grows. Before the fix
// the 100KiB case cost ~linearly more than 30KiB (re-rendering + width-measuring the
// whole accumulated markdown every frame); after it both land near one another,
// bounded by liveStreamingMarkdownWindow.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkStreamingAutoExpandReason -benchtime 200x
func BenchmarkStreamingAutoExpandReason(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	for _, kb := range []int{30, 100} {
		b.Run("reason_"+strconv.Itoa(kb)+"kiB", func(b *testing.B) {
			tx := benchStreamingLiveReason(kb)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = tx.renderPaneContent()
			}
		})
	}
}

// BenchmarkRemapMarkdownColors is the regression guard for the fast-path rewrite
// of remapMarkdownColors: the streaming live tail re-renders the reasoning/answer
// block pane every delta, and the old regexp-based implementation (ReplaceAllStringFunc
// rebuilding every SGR via split+join) cost ~28ms for one 8KiB markdown block,
// pinning a core during long streaming reasoning. The manual scanner must stay in
// the single-digit-ms range on an 8KiB block.
//
// Run: go test ./internal/tui -run xxx -bench BenchmarkRemapMarkdownColors -benchtime 100x
func BenchmarkRemapMarkdownColors(b *testing.B) {
	b.Setenv("EITRI_ASCII_GLYPHS", "1")
	body := strings.Repeat("## heading paragraph with `code` and **bold** reasoning tokens\n", 400)
	s, err := RenderMarkdown(body, 100, "dark")
	if err != nil {
		b.Fatal(err)
	}
	th := themeFor("dark")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = remapMarkdownColors(s, th)
	}
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
