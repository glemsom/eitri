package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// livePerDeltaTranscript builds a busy transcript whose live turn has streamed
// the given per-delta reasoning fragments and nothing else: a live turn's flow
// flushes one reasoning fragment per delta (issue #657), so the fixture is the
// minimal many-fragment shape the issue #658 interaction sweep must cover.
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

// livePerDeltaWithToolTranscript builds a busy live turn whose per-delta
// reasoning fragments straddle a tool entry: two deltas stream before the tool
// start, one after the tool result — the shape AC1 names ("fragments on both
// sides of a tool entry") rendered as one flat flow.
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

// TestTranscript_collapsibleBlocksEnumeratesEveryPerDeltaFragment locks AC1 of
// issue #658: a live turn's flow flushes one reasoning fragment per delta, so
// the block focus must enumerate every one of those fragments in render order —
// never merge a per-delta burst into a single focusable block.
func TestTranscript_collapsibleBlocksEnumeratesEveryPerDeltaFragment(t *testing.T) {
	tx := livePerDeltaTranscript([]string{"alpha1", "beta2", "gamma3"})

	blocks := tx.collapsibleBlocks()
	if len(blocks) != 3 {
		t.Fatalf("collapsibleBlocks() = %d, want 3 per-delta fragments:\n%+v", len(blocks), blocks)
	}
	for k, want := range []int{0, 1, 2} {
		b := blocks[k]
		if b.kind != blockReasoning || b.fragIdx != want || b.msgIdx != 1 {
			t.Fatalf("block %d = %+v, want reasoning fragment %d of message 1", k, b, want)
		}
	}
}

// TestTranscript_perDeltaFocusCyclesFragmentsOnBothSidesOfTool locks AC1's
// render-order traversal: fragments streamed before AND after a tool entry all
// enumerate as focusable blocks in emission order (the tool sits visually
// between them, never as a focus boundary), and Tab cycles through every one,
// wrapping back to the first.
func TestTranscript_perDeltaFocusCyclesFragmentsOnBothSidesOfTool(t *testing.T) {
	tx := livePerDeltaWithToolTranscript() // fragA, fragB, tool read, fragC

	blocks := tx.collapsibleBlocks()
	if len(blocks) != 4 {
		t.Fatalf("collapsibleBlocks() = %d, want 4 (three per-delta fragments + one tool):\n%+v", len(blocks), blocks)
	}
	for k, wantText := range []string{"fragA", "fragB", "fragC"} {
		if b := blocks[k]; b.kind != blockReasoning || b.fragIdx != k {
			t.Fatalf("block %d = %+v, want reasoning fragment %d (%s)", k, b, k, wantText)
		}
	}
	if b := blocks[3]; b.kind != blockTool || b.toolIdx != 0 {
		t.Fatalf("block 3 = %+v, want the tool entry", b)
	}

	// Tab cycles forward in emission order: fragA, fragB, fragC, then the tool;
	// one more Tab wraps back to fragA.
	for want := 0; want <= 3; want++ {
		tx.focusNext()
		if got, ok := tx.focused(); !ok || got.fragIdx != want && !(want == 3 && got.kind == blockTool) {
			t.Fatalf("after Tab to block %d focused = %+v ok=%v, want block %d", want, got, ok, want)
		}
	}
	tx.focusNext()
	if got, ok := tx.focused(); !ok || got.kind != blockReasoning || got.fragIdx != 0 {
		t.Fatalf("wrap-around Tab focused = %+v ok=%v, want reasoning fragment 0", got, ok)
	}

	// The cursor resolves exactly the middle fragment on the seam used by the
	// renderer's focus marker.
	tx = livePerDeltaWithToolTranscript()
	tx.focusNext() // fragA
	tx.focusNext() // fragB
	if !tx.focusedBlockIs(blockReasoning, 1, 0, 1) {
		t.Fatalf("cursor must point at fragment 1, got focused=%+v", mustFocused(tx))
	}
	if tx.focusedBlockIs(blockReasoning, 1, 0, 2) {
		t.Fatalf("cursor must not point at fragment 2")
	}
}

func mustFocused(tx *Transcript) collapsibleBlock {
	blk, ok := tx.focused()
	if !ok {
		panic("no focused block")
	}
	return blk
}

// TestTranscript_livePerDeltaEnterTogglesAnySingleFragmentIndependently locks
// AC2 of issue #658: Enter on a focused per-delta fragment collapses exactly
// that fragment's body and leaves its streamed siblings expanded; Enter again
// re-expands only it.
func TestTranscript_livePerDeltaEnterTogglesAnySingleFragmentIndependently(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := livePerDeltaTranscript([]string{"alpha1", "beta2", "gamma3"})

	tx.focusNext() // alpha1
	tx.focusNext() // beta2
	if blk, ok := tx.focused(); !ok || blk.fragIdx != 1 {
		t.Fatalf("focused = %+v ok=%v, want reasoning fragment 1", blk, ok)
	}
	tx.toggleFocused()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())
	if strings.Contains(plain, "beta2") {
		t.Errorf("toggling fragment 1 must collapse only its body, got:\n%s", plain)
	}
	for _, frag := range []string{"alpha1", "gamma3"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("collapsing fragment 1 must leave %q expanded, got:\n%s", frag, plain)
		}
	}
	if n := strings.Count(plain, "tok"); n != 3 {
		t.Errorf("collapsed fragment 1 must keep its hint among the three headers, got %d hints:\n%s", n, plain)
	}

	tx.toggleFocused()
	var hist2 strings.Builder
	tx.renderHistory(&hist2, nil, nil)
	plain2 := ansiStrip(hist2.String())
	if !strings.Contains(plain2, "beta2") {
		t.Errorf("toggling fragment 1 again must re-expand it, got:\n%s", plain2)
	}
	for _, frag := range []string{"alpha1", "gamma3"} {
		if !strings.Contains(plain2, frag) {
			t.Errorf("fragment %q must stay expanded, got:\n%s", frag, plain2)
		}
	}
}

// TestModel_livePerDeltaFragmentPinsStayIndependentThroughBurst locks AC2's
// persistence leg: collapsing one fragment of a live per-delta burst pins that
// exact fragment while the burst keeps streaming — later deltas render expanded
// on their own, so a user's per-fragment choice survives the continuing stream.
func TestModel_livePerDeltaFragmentPinsStayIndependentThroughBurst(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyReasoningDelta(t, m, "alpha1")
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyTab})   // focus fragment 0
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}) // collapse it

	// The burst keeps streaming: new per-delta fragments arrive after the
	// pinned one and must render expanded, independent of its collapse.
	m = applyReasoningDelta(t, m, "beta2")
	m = applyReasoningDelta(t, m, "gamma3")
	plain := ansiStrip(view(m))
	if strings.Contains(plain, "alpha1") {
		t.Errorf("fragment 0 must stay force-collapsed through the burst, got:\n%s", plain)
	}
	for _, frag := range []string{"beta2", "gamma3"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("fragment %q must render expanded after the burst, got:\n%s", frag, plain)
		}
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

	for i := 0; i < 40; i++ {
		m = applyReasoningDelta(t, m, fmt.Sprintf("burst%02d ", i))
		followRendered(m)
		if !m.tx.histFollow {
			t.Fatalf("delta %d must keep follow engaged", i)
		}
		if !m.tx.histViewport.AtBottom() {
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

// TestModel_collapseAllAndExpandAllCoverEveryPerDeltaFragment locks AC3 of
// issue #658: the global modes cover every fragment of a live per-delta turn —
// collapse-all (E) hides every fragment body, Enter on a focused fragment
// re-expands just it against the mode, and expand-all (ctrl+e) shows them all
// again.
func TestModel_collapseAllAndExpandAllCoverEveryPerDeltaFragment(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "alpha1")
	m = applyReasoningDelta(t, m, "beta2")
	m = applyReasoningDelta(t, m, "gamma3")

	if n := strings.Count(ansiStrip(view(m)), "tok"); n != 3 {
		t.Fatalf("precondition: three per-delta fragments expanded, got %d headers:\n%s", n, view(m))
	}

	// E: collapse-all hides every fragment body, one hint per fragment remains.
	m = keypress(t, m, "E")
	plain := ansiStrip(view(m))
	for _, frag := range []string{"alpha1", "beta2", "gamma3"} {
		if strings.Contains(plain, frag) {
			t.Errorf("collapse-all must hide fragment body %q, got:\n%s", frag, plain)
		}
	}
	if n := strings.Count(plain, "tok"); n != 3 {
		t.Errorf("collapse-all must keep one hint per fragment, got %d:\n%s", n, plain)
	}

	// Tab to the middle fragment; Enter re-expands only it against the mode.
	m = keypress(t, m, "tab")
	m = keypress(t, m, "tab")
	m = keypress(t, m, "enter")
	plain = ansiStrip(view(m))
	if !strings.Contains(plain, "beta2") {
		t.Errorf("Enter on the focused fragment must re-expand just it in collapse-all, got:\n%s", plain)
	}
	if strings.Contains(plain, "alpha1") || strings.Contains(plain, "gamma3") {
		t.Errorf("re-expanding one fragment must leave the others collapsed, got:\n%s", plain)
	}

	// ctrl+e: expand-all covers every fragment again.
	m = keypress(t, m, "ctrl+e")
	plain = ansiStrip(view(m))
	for _, frag := range []string{"alpha1", "beta2", "gamma3"} {
		if !strings.Contains(plain, frag) {
			t.Errorf("expand-all must show fragment body %q again, got:\n%s", frag, plain)
		}
	}
}
