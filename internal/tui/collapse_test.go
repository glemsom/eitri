package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// largeCotFlowTranscript builds a completed turn whose event flow carries a
// large chain-of-thought before a tool call and the answer — the file that
// used to push tool calls out of view and that the collapse-by-default change
// (issue #432) locks down.
func largeCotFlowTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}})
	return &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             log,
		messages: []message{
			{role: "you", content: "run it"},
			{
				role:              "eitri",
				content:           "Done.",
				thinkingRequested: true,
				events: []TimelineEvent{
					{Kind: EventReasoning, Seq: 0, Delta: strings.Repeat("deep reasoning step. ", 400)},
					{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
					{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
					{Kind: EventAnswer, Seq: 3, Delta: "Done."},
				},
			},
		},
	}
}

// TestTranscript_largeCoTCollapsesToHintWhileToolsStayVisible locks the
// original complaint (a big CoT pushed tools out of view): the collapsed
// reasoning body must reduce to the 🤔 N tok hint and the tool call must still
// render inside the same flow, in arrival order.
func TestTranscript_largeCoTCollapsesToHintWhileToolsStayVisible(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := largeCotFlowTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	if strings.Contains(plain, "deep reasoning step") {
		t.Errorf("large CoT body must collapse to the hint by default, got:\n%s", plain)
	}
	if !strings.Contains(plain, "tok") {
		t.Errorf("collapsed CoT must keep the 🤔 N tok hint, got:\n%s", plain)
	}
	if !strings.Contains(plain, "$ bash") {
		t.Errorf("tool call must stay visible under a collapsed CoT, got:\n%s", plain)
	}
	if !strings.Contains(plain, "Done.") {
		t.Errorf("answer must still render after the collapsed CoT, got:\n%s", plain)
	}
}

// TestTranscript_coTExpandedByDefaultShowsBody flips the Settings toggle:
// CoT collapsed by default OFF means the reasoning body renders on its own.
func TestTranscript_coTExpandedByDefaultShowsBody(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := largeCotFlowTranscript()
	tx.cotExpanded = true // the "CoT collapsed by default" setting is off

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	if !strings.Contains(plain, "deep reasoning step") {
		t.Errorf("CoT expanded by default must render the reasoning body, got:\n%s", plain)
	}
}

// TestTranscript_toolResultsExpandedByDefaultShowsResult flips the Settings
// toggle: tool results collapsed by default OFF means the result body renders.
func TestTranscript_toolResultsExpandedByDefaultShowsResult(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := largeCotFlowTranscript()
	tx.toolResultsExpanded = true // the "tool results collapsed by default" setting is off

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	if !strings.Contains(plain, "a.go") {
		t.Errorf("tool results expanded by default must render the result body, got:\n%s", plain)
	}
	if !strings.Contains(plain, "$ bash") {
		t.Errorf("tool head must render alongside the default-expanded result, got:\n%s", plain)
	}
}

// TestTranscript_blockFocusCyclesAndToggles exercises the per-block focus:
// Tab cycles the focus through the collapsible blocks (CoT, then tools) and
// Enter toggles the focused block between hint and full body.
func TestTranscript_blockFocusCyclesAndToggles(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()                                           // reasoning + tool + answer, collapsed by default
	tx.messages[1].expansion.clear(blockReasoning, reasoningWholeID) // flowTranscript seeds it expanded; clear the per-block state

	if got := len(tx.collapsibleBlocks()); got != 2 {
		t.Fatalf("collapsibleBlocks() = %d, want 2 (reasoning + tool)", got)
	}

	// Focus has not been entered yet: toggling must be a no-op.
	tx.toggleFocused()
	var before strings.Builder
	tx.renderHistory(&before, nil, nil)
	if strings.Contains(ansiStrip(before.String()), "Let me check the repo first.") {
		t.Fatalf("premature toggle must not expand the CoT, got:\n%s", before.String())
	}

	// Tab -> focus the reasoning block; Enter expands it.
	tx.focusNext()
	blk, ok := tx.focused()
	if !ok || blk.kind != blockReasoning {
		t.Fatalf("first focus = %+v ok=%v, want the reasoning block", blk, ok)
	}
	tx.toggleFocused()
	var expanded strings.Builder
	tx.renderHistory(&expanded, nil, nil)
	if !strings.Contains(ansiStrip(expanded.String()), "Let me check the repo first.") {
		t.Errorf("Enter on the focused reasoning block must reveal the full CoT, got:\n%s", expanded.String())
	}

	// Enter again collapses it back to the hint.
	tx.toggleFocused()
	var recollapsed strings.Builder
	tx.renderHistory(&recollapsed, nil, nil)
	if strings.Contains(ansiStrip(recollapsed.String()), "Let me check the repo first.") {
		t.Errorf("Enter on an expanded reasoning block must collapse it back to the hint, got:\n%s", recollapsed.String())
	}

	// Tab -> focus the tool entry; Enter reveals the tool result.
	tx.focusNext()
	blk, ok = tx.focused()
	if !ok || blk.kind != blockTool || blk.toolIdx != 0 {
		t.Fatalf("second focus = %+v ok=%v, want the tool block", blk, ok)
	}
	tx.toggleFocused()
	var toolExpanded strings.Builder
	tx.renderHistory(&toolExpanded, nil, nil)
	if !strings.Contains(ansiStrip(toolExpanded.String()), "a.go") {
		t.Errorf("Enter on the focused tool block must reveal the result, got:\n%s", toolExpanded.String())
	}

	// Tab wraps back to the reasoning block.
	tx.focusNext()
	blk, ok = tx.focused()
	if !ok || blk.kind != blockReasoning {
		t.Fatalf("wrap focus = %+v ok=%v, want the reasoning block again", blk, ok)
	}
}

// TestTranscript_focusedBlockRendersMarker shows the focused block's hint line
// carries the focus marker so the user can see where Enter will land.
func TestTranscript_focusedBlockRendersMarker(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()
	tx.focusNext() // focus the reasoning header

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	if !strings.Contains(plain, focusMarker()+" ") {
		t.Errorf("focused reasoning header must carry the focus marker, got:\n%s", plain)
	}
}

// TestTranscript_eExpandsAllECollapsesToHints exercises the global keys: e
// expands every collapsible block, E collapses every collapsible block back to
// its hint/one-liner.
func TestTranscript_eExpandsAllECollapsesToHints(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()

	tx.setExpandAll(true)
	var expanded strings.Builder
	tx.renderHistory(&expanded, nil, nil)
	ex := ansiStrip(expanded.String())
	if !strings.Contains(ex, "Let me check the repo first.") || !strings.Contains(ex, "a.go") {
		t.Errorf("e must expand both CoT and tool result, got:\n%s", ex)
	}

	tx.setCollapseAll(true)
	var collapsed strings.Builder
	tx.renderHistory(&collapsed, nil, nil)
	cl := ansiStrip(collapsed.String())
	if strings.Contains(cl, "Let me check the repo first.") {
		t.Errorf("E must collapse CoT to the hint, got:\n%s", cl)
	}
	if strings.Contains(cl, "a.go") {
		t.Errorf("E must collapse the tool result to its one-liner, got:\n%s", cl)
	}
	if !strings.Contains(cl, "tok") || !strings.Contains(cl, "$ bash") {
		t.Errorf("E must keep the CoT hint and tool head, got:\n%s", cl)
	}
}

// TestModel_eExpandsAllECollapseAllHints drives the e/E keys through the
// Model's key handling with an empty composer.
func TestModel_eExpandsAllECollapseAllHints(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	if strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Fatalf("completed turn must start collapsed, got: %q", view(m))
	}

	m = keypress(t, m, "e")
	if !strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Errorf("e must expand the CoT block, got: %q", view(m))
	}

	m = keypress(t, m, "E")
	if got := m.composer.Value(); got != "E" {
		t.Errorf("Shift+E as the first draft letter must reach the composer, got %q", got)
	}

	// Typing a letter with a draft must type, not expand.
	m = typeText(t, m, "again")
	if got := m.composer.Value(); got != "Eagain" {
		t.Errorf("typing with a draft must reach the composer, got %q", got)
	}
}

// TestModel_tabFocusesEnterTogglesBlock drives the Tab/Enter per-block
// interaction through the Model: Tab with an empty composer cycles the focus,
// Enter toggles the focused block.
func TestModel_tabFocusesEnterTogglesBlock(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "go")
	m = submitAndWait(t, m)
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"true"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "done\n"}})

	// Tab focuses the reasoning block; Enter expands it.
	m = keypress(t, m, "tab")
	m = keypress(t, m, "enter")
	var afterCoT strings.Builder
	m.tx.renderHistory(&afterCoT, nil, nil)
	if !strings.Contains(ansiStrip(afterCoT.String()), "hidden reasoning") {
		t.Errorf("Enter on the focused reasoning block must reveal the CoT, got: %q", afterCoT.String())
	}

	// Tab focuses the tool block; Enter expands its result.
	m = keypress(t, m, "tab")
	m = keypress(t, m, "enter")
	var afterTool strings.Builder
	m.tx.renderHistory(&afterTool, nil, nil)
	if !strings.Contains(ansiStrip(afterTool.String()), "done") {
		t.Errorf("Enter on the focused tool block must reveal the result, got: %q", afterTool.String())
	}
}
