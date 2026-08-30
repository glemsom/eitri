package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// livePerDeltaTranscript builds a busy transcript whose live turn has streamed
// the given contiguous chain-of-thought deltas and nothing else: a live turn's
// flow coalesces every contiguous reasoning delta into one block per run (so
// token-size SSE deltas never paint a card per token), so this fixture is the
// minimal shape a coalescing test needs.
func livePerDeltaTranscript(deltas []string) *Transcript {
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		busy:            true,
		messages: []message{
			{role: "you", content: "p"},
			{role: "eitri", streaming: true, thinkingRequested: true,
				reasoning: strings.Join(deltas, " "), expansion: ExpansionState{}},
		},
	}
	events := make([]TimelineEvent, 0, len(deltas))
	for i, d := range deltas {
		events = append(events, TimelineEvent{Kind: EventReasoning, Seq: i, Delta: d})
	}
	wireLive(tx, events)
	return tx
}

// livePerDeltaWithToolTranscript builds a busy live turn whose contiguous
// reasoning deltas straddle a tool entry: two deltas stream before the tool
// start, one after the tool result — the shape AC1 names ("fragments on both
// sides of a tool entry") rendered as one flat flow, coalesced into a pre-tool
// run and a post-tool run.
func livePerDeltaWithToolTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}})
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             log,
		busy:            true,
		messages: []message{
			{role: "you", content: "p"},
			{role: "eitri", streaming: true, thinkingRequested: true,
				reasoning: "fragA fragB fragC", expansion: ExpansionState{}},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventReasoning, Seq: 0, Delta: "fragA"},
		{Kind: EventReasoning, Seq: 1, Delta: "fragB"},
		{Kind: EventToolStart, Seq: 2, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
		{Kind: EventToolResult, Seq: 3, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
		{Kind: EventReasoning, Seq: 4, Delta: "fragC"},
		{Kind: EventAnswer, Seq: 5, Delta: "final"},
	})
	return tx
}

// TestTranscript_collapsibleBlocksCoalescesContiguousDeltas locks the
// regression behind a card-per-token: a live turn's flow coalesces every
// contiguous reasoning delta into ONE focusable block per tool-delimited run
// (so token-size SSE deltas never enumerate a block per token).
func TestTranscript_collapsibleBlocksCoalescesContiguousDeltas(t *testing.T) {
	tx := livePerDeltaTranscript([]string{"alpha1", "beta2", "gamma3"})

	blocks := tx.collapsibleBlocks()
	if len(blocks) != 1 {
		t.Fatalf("collapsibleBlocks() = %d, want 1 coalesced fragment for the contiguous run:\n%+v", len(blocks), blocks)
	}
	b := blocks[0]
	if b.kind != blockReasoning || b.fragIdx != 0 || b.msgIdx != 1 {
		t.Fatalf("block = %+v, want reasoning fragment 0 of message 1", b)
	}
}

// TestTranscript_perDeltaFocusCyclesFragmentsOnBothSidesOfTool locks AC1's
// render-order traversal under coalescing: reasoning coalesces per tool-delimited
// run, so fragments streamed before AND after a tool entry enumerate as separate
// focusable blocks in emission order (the tool sits visually between them), and
// Tab cycles through every one, wrapping back to the first.
func TestTranscript_perDeltaFocusCyclesFragmentsOnBothSidesOfTool(t *testing.T) {
	tx := livePerDeltaWithToolTranscript() // fragA+fragB, tool read, fragC

	blocks := tx.collapsibleBlocks()
	// fragA+fragB coalesce into one pre-tool run, fragC is the post-tool run,
	// plus the tool entry.
	if len(blocks) != 3 {
		t.Fatalf("collapsibleBlocks() = %d, want 3 (pre-tool run + post-tool run + one tool):\n%+v", len(blocks), blocks)
	}
	if b := blocks[0]; b.kind != blockReasoning || b.fragIdx != 0 {
		t.Fatalf("block 0 = %+v, want pre-tool reasoning run 0", b)
	}
	if b := blocks[1]; b.kind != blockReasoning || b.fragIdx != 1 {
		t.Fatalf("block 1 = %+v, want post-tool reasoning run 1", b)
	}
	if b := blocks[2]; b.kind != blockTool || b.toolIdx != 0 {
		t.Fatalf("block 2 = %+v, want the tool entry", b)
	}

	// Tab cycles forward in emission order: pre-tool run, post-tool run, then the tool.
	for _, want := range []collapsibleBlock{
		{kind: blockReasoning, msgIdx: 1, fragIdx: 0},
		{kind: blockReasoning, msgIdx: 1, fragIdx: 1},
		{kind: blockTool, toolIdx: 0},
	} {
		tx.focusNext()
		if got, ok := tx.focused(); !ok || got != want {
			t.Fatalf("after Tab focused = %+v ok=%v, want %+v", got, ok, want)
		}
	}
	// One more Tab wraps back to the first pre-tool fragment.
	tx.focusNext()
	if got, ok := tx.focused(); !ok || got != (collapsibleBlock{kind: blockReasoning, msgIdx: 1, fragIdx: 0}) {
		t.Fatalf("wrap-around Tab focused = %+v ok=%v, want reasoning run 0", got, ok)
	}

	// The cursor resolves exactly the pre-tool run on the seam used by the
	// renderer's focus marker.
	tx = livePerDeltaWithToolTranscript()
	tx.focusNext() // pre-tool run
	tx.focusNext() // post-tool run
	if !tx.focusedBlockIs(blockReasoning, 1, 0, 1) {
		t.Fatalf("cursor must point at post-tool run 1, got focused=%+v", mustFocused(tx))
	}
	if tx.focusedBlockIs(blockReasoning, 1, 0, 2) {
		t.Fatalf("cursor must not point at a per-delta fragment index 2")
	}
}

func mustFocused(tx *Transcript) collapsibleBlock {
	blk, ok := tx.focused()
	if !ok {
		panic("no focused block")
	}
	return blk
}

// TestTranscript_enterTogglesSingleInterleavedRunIndependently keeps AC2 of
// issue #658 scoped to tool-delimited runs (the coalescing granularity): Enter on
// a focused interleaved reasoning run collapses exactly that run's body and
// leaves its streamed siblings expanded; Enter again re-expands only it.
func TestTranscript_enterTogglesSingleInterleavedRunIndependently(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := livePerDeltaWithToolTranscript() // pre-tool run (fragA fragB), tool, post-tool run (fragC)

	tx.focusNext() // pre-tool run (fragIdx 0)
	tx.focusNext() // post-tool run (fragIdx 1)
	if blk, ok := tx.focused(); !ok || blk.fragIdx != 1 {
		t.Fatalf("focused = %+v ok=%v, want reasoning run 1", blk, ok)
	}
	tx.toggleFocused()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())
	if strings.Contains(plain, "fragC") {
		t.Errorf("toggling run 1 must collapse only its body, got:\n%s", plain)
	}
	for _, frag := range []string{"fragA", "fragB"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("collapsing run 1 must leave %q (pre-tool run) expanded, got:\n%s", frag, plain)
		}
	}
	if n := strings.Count(plain, "tok"); n != 2 {
		t.Errorf("collapsed run 1 must keep its hint among the two run headers, got %d hints:\n%s", n, plain)
	}

	tx.toggleFocused()
	var hist2 strings.Builder
	tx.renderHistory(&hist2, nil, nil)
	plain2 := ansiStrip(hist2.String())
	if !strings.Contains(plain2, "fragC") {
		t.Errorf("toggling run 1 again must re-expand it, got:\n%s", plain2)
	}
	for _, frag := range []string{"fragA", "fragB"} {
		if !strings.Contains(plain2, frag) {
			t.Errorf("pre-tool run %q must stay expanded, got:\n%s", frag, plain2)
		}
	}
}

// TestModel_liveReasoningPinsWholeCoalescedBlockThroughBurst keeps AC2's
// persistence leg at the coalescing granularity: collapsing the single live
// reasoning block pins it, and since the burst coalesces into that same block,
// the user's collapse survives the continuing stream (it stays collapsed rather
// than per-token cards re-appearing).
func TestModel_liveReasoningPinsWholeCoalescedBlockThroughBurst(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyReasoningDelta(t, m, "alpha1")
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyTab})   // focus the reasoning block
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // collapse it

	// The burst keeps streaming: coalesced deltas grow the same collapsed block,
	// never per-token cards re-appearing.
	m = applyReasoningDelta(t, m, "beta2")
	m = applyReasoningDelta(t, m, "gamma3")
	plain := ansiStrip(view(m))
	if strings.Contains(plain, "alpha1") {
		t.Errorf("reasoning block must stay force-collapsed through the burst, got:\n%s", plain)
	}
	if strings.Contains(plain, "beta2") || strings.Contains(plain, "gamma3") {
		t.Errorf("coalesced burst must not re-open the pinned collapsed block, got:\n%s", plain)
	}
}

// TestModel_followStaysEngagedThroughPerDeltaBurst locks AC4 of issue #658: a
// burst of per-delta reasoning fragments — many small blocks arriving in quick
// succession — never drops follow: the viewport stays pinned to the newest
// output while the burst streams.
func TestModel_followStaysEngagedThroughPerDeltaBurst(t *testing.T) {
	m := newStreamingModel()
	m = resizeTo(t, m, 60, 7)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	for i := range 40 {
		m = applyReasoningDelta(t, m, fmt.Sprintf("burst%02d ", i))
		followRendered(m)
		if !m.tx.histFollow {
			t.Fatalf("delta %d must keep follow engaged", i)
		}
		if !atBottom(m) {
			t.Fatalf("delta %d let the viewport slip off the newest content (offset %d)", i, scrollOffset(m))
		}
	}
	got, _, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("test needs a positive viewport height, got %d", vh)
	}
	if row := newestNonBlank(got); row != "⠋ Reasoning\n" {
		t.Errorf("following a per-delta burst must hold the newest active tail at the bottom, got last row %q\n%s", row, got)
	}
}

// TestTranscript_gatedLiveTurnEnumeratesNoReasoningBlocks locks AC1's
// thinking-gate leg: a live turn that never asked for reasoning renders no
// reasoning fragments, so the block focus must not enumerate phantom blocks for
// them — only the turn's tool entries stay focusable.
func TestTranscript_gatedLiveTurnEnumeratesNoReasoningBlocks(t *testing.T) {
	tx := liveReasoningInterleaveTranscript() // 2 reasoning fragments + 2 tools
	tx.messages[1].thinkingRequested = false

	blocks := tx.collapsibleBlocks()
	if len(blocks) != 2 {
		t.Fatalf("gated live turn collapsibleBlocks() = %d, want 2 (tools only), got:\n%+v", len(blocks), blocks)
	}
	for _, b := range blocks {
		if b.kind != blockTool {
			t.Fatalf("gated live turn must leave only tool blocks focusable, got %+v", b)
		}
	}
}

// TestModel_collapseAllAndExpandAllCoverCoalescedReasoning locks AC3 of
// issue #658 at the coalescing granularity: the global modes cover the single
// live reasoning block — collapse-all (E) hides its body, Enter on the focused
// block re-expands it against the mode, and expand-all (ctrl+e) shows it again.
func TestModel_collapseAllAndExpandAllCoverCoalescedReasoning(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "alpha1 beta2 gamma3")

	if n := strings.Count(ansiStrip(view(m)), "tok"); n != 1 {
		t.Fatalf("precondition: one coalesced reasoning block expanded, got %d headers:\n%s", n, view(m))
	}

	// E: collapse-all hides the reasoning body, one hint remains.
	m = keypress(t, m, "E")
	plain := ansiStrip(view(m))
	if strings.Contains(plain, "alpha1 beta2 gamma3") {
		t.Errorf("collapse-all must hide the reasoning body, got:\n%s", plain)
	}
	if n := strings.Count(plain, "tok"); n != 1 {
		t.Errorf("collapse-all must keep the one reasoning hint, got %d:\n%s", n, plain)
	}

	// Tab to the reasoning block; Enter re-expands it against the mode.
	m = keypress(t, m, "tab")
	m = keypress(t, m, "enter")
	plain = ansiStrip(view(m))
	if !strings.Contains(plain, "alpha1 beta2 gamma3") {
		t.Errorf("Enter on the focused reasoning block must re-expand it in collapse-all, got:\n%s", plain)
	}

	// ctrl+e: expand-all shows the reasoning again.
	m = keypress(t, m, "ctrl+e")
	plain = ansiStrip(view(m))
	if !strings.Contains(plain, "alpha1 beta2 gamma3") {
		t.Errorf("expand-all must show the reasoning body again, got:\n%s", plain)
	}
}
