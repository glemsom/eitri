package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// flowTranscript builds a completed turn whose event log interleaves reasoning,
// a tool start/result, and an answer — the exact arrival sequence the T02
// flat-flow renderer must reproduce as one continuous block.
func flowTranscript() *Transcript {
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
				expansion:         expansionWithReasoningForces(true, false),
				events: []TimelineEvent{
					{Kind: EventReasoning, Seq: 0, Delta: "Let me check the repo first."},
					{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
					{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
					{Kind: EventAnswer, Seq: 3, Delta: "Done."},
				},
			},
		},
	}
}

func TestTranscript_rendersTurnAsFlatFlowInArrivalOrder(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	ri := strings.Index(plain, "Let me check the repo first.")
	ti := strings.Index(plain, g("🔧 bash", "$ bash"))
	ai := strings.Index(plain, "Done.")
	if ri < 0 || ti < 0 || ai < 0 {
		t.Fatalf("flat flow render is missing segments (reasoning %d, tool %d, answer %d):\n%s", ri, ti, ai, plain)
	}
	// The acceptance criterion: one block in arrival order — reasoning reads
	// first, tool activity sits between it and the answer, never below it.
	if !(ri < ti && ti < ai) {
		t.Errorf("flat flow must order reasoning < tool < answer, got %d, %d, %d:\n%s", ri, ti, ai, plain)
	}
	// No segment may render twice: the flow replaces the three separate panes
	// (thinking pane / tool log / answer pane) with one pass over the events.
	for _, marker := range []string{"Let me check the repo first.", g("🔧 bash", "$ bash"), "2 lines", "Done."} {
		if n := strings.Count(plain, marker); n != 1 {
			t.Errorf("marker %q rendered %d times, want exactly once (flat flow, no duplicates):\n%s", marker, n, plain)
		}
	}
}

func TestTranscript_flatFlowAnswerKeepsAgentHue(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	rendered := hist.String()

	// The completed answer block must keep the full agent accent, not the
	// dimmed streaming hue, on the merged stream.
	answerColor := lineBorderColor(rendered, "Done.")
	if answerColor != borderColorStr(tx.theme.agentPaneStyle) {
		t.Errorf("flat-flow answer border color = %q, want agent accent %q:\n%s", answerColor, borderColorStr(tx.theme.agentPaneStyle), ansiStrip(rendered))
	}
}

func TestTranscript_liveTurnRendersFromTimelineFlow(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
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
			{role: "you", content: "read it"},
			{role: "eitri", content: "It is alpha", reasoning: "Reading the file.", streaming: true, thinkingRequested: true},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventReasoning, Seq: 0, Delta: "Reading the file."},
		{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
		{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
		{Kind: EventAnswer, Seq: 3, Delta: "It is"},
		{Kind: EventAnswer, Seq: 4, Delta: " alpha"},
	})

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	ri := strings.Index(plain, "Reading the file.")
	ai := strings.Index(plain, "a.txt")
	bi := strings.Index(plain, "It is alpha")
	if ri < 0 || ai < 0 || bi < 0 {
		t.Fatalf("live flow render is missing segments (reasoning %d, tool %d, answer %d):\n%s", ri, ai, bi, plain)
	}
	if !(ri < ai && ai < bi) {
		t.Errorf("live flow must order reasoning < tool < growing answer, got %d, %d, %d:\n%s", ri, ai, bi, plain)
	}
}

func TestTranscript_flatFlowCollapsesReasoningOnCompletion(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()
	tx.messages[1].expansion.clear(blockReasoning, reasoningWholeID) // the turn completed (no expand-all mode)

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	if strings.Contains(plain, "Let me check the repo first.") {
		t.Errorf("completed turn must collapse the reasoning body to the hint, got:\n%s", plain)
	}
	if !strings.Contains(plain, "tok") {
		t.Errorf("collapsed reasoning must keep the 🤔 N tok hint, got:\n%s", plain)
	}
	// The tool entry and answer must still render after the reasoning hint.
	if !strings.Contains(plain, g("🔧 bash", "$ bash")) || !strings.Contains(plain, "Done.") {
		t.Errorf("collapsed turn must still render tool and answer, got:\n%s", plain)
	}
}

// lineBorderColor returns the left-border SGR color of the rendered line that
// contains body, mirroring borderColorCode but scoped to one line.
func lineBorderColor(rendered, body string) string {
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(ansiStrip(line), body) {
			continue
		}
		start := strings.Index(line, "\x1b[38;2;")
		if start == -1 {
			return ""
		}
		end := strings.IndexByte(line[start+len("\x1b[38;2;"):], 'm')
		if end == -1 {
			return ""
		}
		return line[start+len("\x1b[38;2;") : start+len("\x1b[38;2;")+end]
	}
	return ""
}

func wireLive(tx *Transcript, events []TimelineEvent) {
	s := NewTurnSession(nil)
	for _, ev := range events {
		switch ev.Kind {
		case EventReasoning:
			s.flow.Observe(ReasoningStream, ev.Delta)
		case EventAnswer:
			s.flow.Observe(AnswerStream, ev.Delta)
		default:
			s.flow.ObserveTool(ev)
		}
	}
	tx.live = s
}

// answerInterleaveTranscript builds a completed turn whose answer text streams
// in fragments around two tool calls — partial answers before and between the
// tools, and a final fragment at the tail — the arrival order the flat flow
// must reproduce exactly as the provider emitted it.
func answerInterleaveTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}})
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "x", Lines: 1}})
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		log:             log,
		messages: []message{
			{role: "you", content: "p"},
			{role: "eitri", content: "alpha, and ls gave gives-x-final", thinkingRequested: true},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventAnswer, Seq: 0, Delta: "alpha, "},
		{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
		{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
		{Kind: EventAnswer, Seq: 3, Delta: "and ls gave "},
		{Kind: EventToolStart, Seq: 4, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
		{Kind: EventToolResult, Seq: 5, Result: &ToolResult{Name: "bash", Result: "x", Lines: 1}},
		{Kind: EventAnswer, Seq: 6, Delta: "gives-x-final"},
	})
	return tx
}

// liveReasoningInterleaveTranscript builds a Transcript mid-live-turn whose
// in-progress timeline interleaves chain-of-thought around two tool calls: a
// reasoning fragment before the first tool and a second fragment after the
// first tool's result, before the second tool — the emission order the live
// flow must reproduce without dropping the resumed reasoning.
func liveReasoningInterleaveTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}})
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "x", Lines: 1}})
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
			{role: "eitri", reasoning: "reasoning one reasoning two", streaming: true, thinkingRequested: true},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventReasoning, Seq: 0, Delta: "reasoning one"},
		{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
		{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
		{Kind: EventReasoning, Seq: 3, Delta: "reasoning two"},
		{Kind: EventToolStart, Seq: 4, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
		{Kind: EventToolResult, Seq: 5, Result: &ToolResult{Name: "bash", Result: "x", Lines: 1}},
		{Kind: EventAnswer, Seq: 6, Delta: "final answer text"},
	})
	return tx
}

func TestTranscript_liveReasoningInterleavesWithToolsInEmissionOrder(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveReasoningInterleaveTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	r1 := strings.Index(plain, "reasoning one")
	tool1 := strings.Index(plain, "read  a.txt")
	r2 := strings.Index(plain, "reasoning two")
	tool2 := strings.Index(plain, "bash  ls")
	finalA := strings.Index(plain, "final answer text")
	if r1 < 0 || tool1 < 0 || r2 < 0 || tool2 < 0 || finalA < 0 {
		t.Fatalf("live interleave render is missing segments r1=%d t1=%d r2=%d t2=%d a=%d:\n%s", r1, tool1, r2, tool2, finalA, plain)
	}
	// The resumed reasoning fragment lands between the two tool entries, in
	// emission order, rather than being dropped or hoisted above the first tool.
	if !(r1 < tool1) {
		t.Errorf("first reasoning must precede tool1, got r1=%d t1=%d:\n%s", r1, tool1, plain)
	}
	if !(tool1 < r2 && r2 < tool2) {
		t.Errorf("resumed reasoning must sit between the tool entries, got t1=%d r2=%d t2=%d:\n%s", tool1, r2, tool2, plain)
	}
	if !(tool2 < finalA) {
		t.Errorf("answer must follow tool2, got t2=%d a=%d:\n%s", tool2, finalA, plain)
	}
	// Each reasoning fragment renders exactly once: interleaving never
	// duplicates a fragment, and never merges the two into one block.
	for _, marker := range []string{"reasoning one", "reasoning two"} {
		if n := strings.Count(plain, marker); n != 1 {
			t.Errorf("reasoning marker %q rendered %d times, want exactly once:\n%s", marker, n, plain)
		}
	}
}

// TestTranscript_liveReasoningFocusTogglesSingleFragmentIndependently pins
// again exposes each reasoning fragment as its own focusable block, so Tab + Enter
// collapses just the fragment under the cursor and leaves the others expanded.
func TestTranscript_liveReasoningFocusTogglesSingleFragmentIndependently(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveReasoningInterleaveTranscript() // reasoning frag0, tool read, reasoning frag1, tool bash

	// The live turn exposes each reasoning fragment as its own collapsible block,
	// in emission order, before the anchored tool entries: [frag0, frag1, tool read, tool bash].
	blocks := tx.collapsibleBlocks()
	if len(blocks) != 4 {
		t.Fatalf("collapsibleBlocks() = %d, want 4 (two reasoning fragments + two tools):\n%+v", len(blocks), blocks)
	}
	if blocks[0].kind != blockReasoning || blocks[0].fragIdx != 0 {
		t.Fatalf("first block = %+v, want reasoning fragment 0", blocks[0])
	}
	if blocks[1].kind != blockReasoning || blocks[1].fragIdx != 1 {
		t.Fatalf("second block = %+v, want reasoning fragment 1", blocks[1])
	}
	if blocks[3].kind != blockTool {
		t.Fatalf("fourth block = %+v, want the second tool entry", blocks[3])
	}

	// Tab to the second reasoning fragment and Enter to collapse only that one.
	tx.focusNext() // frag0
	x2, ok := tx.focused()
	if !ok || x2.fragIdx != 0 {
		t.Fatalf("after first Tab focused = %+v ok=%v, want reasoning fragment 0", x2, ok)
	}
	tx.focusNext() // frag1
	blk, ok := tx.focused()
	if !ok || blk.kind != blockReasoning || blk.fragIdx != 1 {
		t.Fatalf("after second Tab focused = %+v ok=%v, want reasoning fragment 1", blk, ok)
	}
	tx.toggleFocused()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())
	if strings.Contains(plain, "reasoning two") {
		t.Errorf("toggling fragment 1 must collapse only its body, got:\n%s", plain)
	}
	if !strings.Contains(plain, "reasoning one") {
		t.Errorf("collapsing fragment 1 must leave fragment 0 expanded, got:\n%s", plain)
	}

	// The collapsed fragment keeps its own hint (the second 🤔 N tok line), and
	// toggling again re-expands just it while fragment 0 stays visible.
	if !strings.Contains(plain, "tok") {
		t.Errorf("collapsed fragment 1 must keep its 🤔 N tok hint, got:\n%s", plain)
	}
	tx.toggleFocused()
	var hist2 strings.Builder
	tx.renderHistory(&hist2, nil, nil)
	plain2 := ansiStrip(hist2.String())
	if !strings.Contains(plain2, "reasoning two") {
		t.Errorf("toggling fragment 1 again must re-expand it, got:\n%s", plain2)
	}
	if !strings.Contains(plain2, "reasoning one") {
		t.Errorf("fragment 0 must stay expanded, got:\n%s", plain2)
	}
}

func TestRenderHistory_liveInterleavedReasoningRespectsThinkingGate(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveReasoningInterleaveTranscript()
	tx.messages[1].thinkingRequested = false

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())
	if strings.Contains(plain, "reasoning one") || strings.Contains(plain, "reasoning two") {
		t.Errorf("thinking-off live turn must hide interleaved reasoning, got:\n%s", plain)
	}
	// The tools and answer still render on a thinking-off turn.
	if !strings.Contains(plain, "read  a.txt") || !strings.Contains(plain, "final answer text") {
		t.Errorf("thinking-off live turn must still render tools and answer, got:\n%s", plain)
	}
}

func TestTranscript_committedReasoningSnapshotRendersOnce(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
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
		messages: []message{
			{role: "you", content: "p"},
			{role: "eitri", content: "done", reasoning: "snapshot reasoning", thinkingRequested: true, expansion: expansionWithReasoningForces(true, false),
				events: []TimelineEvent{
					{Kind: EventReasoning, Seq: 0, Delta: "snapshot reasoning"},
					{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
					{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
					{Kind: EventReasoning, Seq: 3, Delta: "after-tool reasoning"},
					{Kind: EventAnswer, Seq: 4, Delta: "done"},
				}},
		},
	}

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	if n := strings.Count(plain, "snapshot reasoning"); n != 1 {
		t.Errorf("committed reasoning snapshot rendered %d times, want exactly once:\n%s", n, plain)
	}
	// A committed turn's reasoning is one authoritative snapshot; a reasoning
	// event that resumes after a tool is not re-rendered as a second block.
	if strings.Contains(plain, "after-tool reasoning") {
		t.Errorf("committed turn must not re-render a resumed reasoning fragment, got:\n%s", plain)
	}
}

// TestTranscript_committedReasoningSnapshotOnceAtTailWhenNoToolFollows locks
// that a committed turn that streams no tool renders its reasoning snapshot
// exactly once, at the tail (directly before the answer), through the
// full Transcript render path rather than only the flow fold.
func TestTranscript_committedReasoningSnapshotOnceAtTailWhenNoToolFollows(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          30,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		messages: []message{
			{role: "you", content: "q"},
			{role: "eitri", content: "done", reasoning: "tail reasoning", thinkingRequested: true, expansion: expansionWithReasoningForces(true, false),
				events: []TimelineEvent{
					{Kind: EventReasoning, Seq: 0, Delta: "tail reasoning"},
					{Kind: EventAnswer, Seq: 1, Delta: "done"},
				}},
		},
	}

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	ri := strings.Index(plain, "tail reasoning")
	ai := strings.Index(plain, "done")
	if ri < 0 || ai < 0 {
		t.Fatalf("no-tool committed transcript missing reasoning/answer (r=%d a=%d):\n%s", ri, ai, plain)
	}
	if !(ri < ai) {
		t.Errorf("no-tool committed reasoning must sit at the tail before the answer, got r=%d a=%d:\n%s", ri, ai, plain)
	}
	if n := strings.Count(plain, "tail reasoning"); n != 1 {
		t.Errorf("no-tool committed reasoning rendered %d times, want exactly once at the tail:\n%s", n, plain)
	}
}

func TestTranscript_partialAnswersInterleaveWithToolsInArrivalOrder(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := answerInterleaveTranscript()
	tx.messages[1].events = tx.LiveTimeline()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	partial := strings.Index(plain, "alpha,")
	tool1 := strings.Index(plain, "read  a.txt")
	mid := strings.Index(plain, "and ls gave")
	tool2 := strings.Index(plain, "bash  ls")
	lastX := strings.LastIndex(plain, "gives-x-final")
	if partial < 0 || tool1 < 0 || mid < 0 || tool2 < 0 || lastX < 0 {
		t.Fatalf("missing segments partial=%d t1=%d mid=%d t2=%d lastX=%d:\n%s", partial, tool1, mid, tool2, lastX, plain)
	}
	// Partial answer fragments land where they streamed: before the first tool,
	// between the two tools, and after the last — never collapsed into a single
	// block hoisted above the tools.
	if !(partial < tool1) {
		t.Errorf("partial answer %q must render before tool1, got partial=%d tool1=%d:\n%s", "alpha,", partial, tool1, plain)
	}
	if !(mid > tool1 && mid < tool2) {
		t.Errorf("mid answer fragment must sit between tool1 and tool2, got t1=%d mid=%d t2=%d:\n%s", tool1, mid, tool2, plain)
	}
	if !(tool2 < lastX) {
		t.Errorf("final answer fragment must render after tool2, got t2=%d lastX=%d:\n%s", tool2, lastX, plain)
	}
	// Each answer text appears exactly once: interleaving reorders, never
	// duplicates, the streamed answer.
	for _, marker := range []string{"alpha,", "and ls gave", "gives-x-final"} {
		if n := strings.Count(plain, marker); n != 1 {
			t.Errorf("answer marker %q rendered %d times, want exactly once:\n%s", marker, n, plain)
		}
	}
}

// gapTranscript builds a busy transcript whose running turn sits in the
// tool-heavy gap with an empty live timeline: the user prompt is the last
// message, nothing has streamed yet.
func gapTranscript() *Transcript {
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:        th,
		configTheme:  config.DefaultTheme,
		width:        100,
		height:       30,
		histFollow:   true,
		histViewport: newHistoryViewport(),
		log:          toolLog{},
		busy:         true,
		messages:     []message{{role: "you", content: "run it"}},
	}
	return tx
}

func TestTurnFlowEvents_emptyTimelineGapIsFlow(t *testing.T) {
	tx := gapTranscript()

	events, ok := tx.turnFlowEvents(0)
	if !ok {
		t.Fatal("a prompt with no committed events while busy must still render as a flow (synthesized minimal event log)")
	}
	if len(events) != 0 {
		t.Errorf("synthesized event log must be empty in the gap, got %+v", events)
	}
}

func TestTranscript_emptyTimelineGapRendersThroughFlowRenderer(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := gapTranscript()

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)

	// The rendered history must be exactly the prompt card plus what the one
	// FlowRenderer emitter produces for the synthesized (empty) log — no
	// legacy tool-log branch output beneath the card.
	want := ""
	md, _ := RenderPromptMarkdown("run it", tx.transcriptWidth()-4, tx.configTheme)
	want += renderUserPromptCard(tx.theme, md, tx.transcriptWidth()) + "\n"
	flow, _ := tx.renderEventFlow(nil, 0, message{}, 0, time.Time{})
	want += flow
	want += tx.theme.statusStyle.Render(busyLine(tx.spinner, tx.phase())) + "\n"

	if hist.String() != want {
		t.Errorf("empty-timeline gap must render prompt card + FlowRenderer output only:\n got %q\nwant %q", hist.String(), want)
	}
}

func TestTranscript_instantErrorTurnRendersFlow(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	s := NewTurnSession(stubTurn("", errors.New("boom")))

	tx := newTestTx()
	tx.width = 100
	tx.height = 30
	tx.histFollow = true
	tx.histViewport = newHistoryViewport()
	s.Begin(&tx, "go", "")

	if _, err := s.Commit(&tx, turnDoneMsg{prompt: "go", err: errors.New("boom")}); err == nil {
		t.Fatal("expected error")
	}

	// The failed turn's prompt renders as a flow via its assistant failure
	// note's synthesized event log; no legacy direct-render fallback fires.
	// The turn-flow lookup must resolve the failed prompt to its committed
	// event log — no fallback for renderable turns.
	if _, ok := tx.turnFlowEvents(len(tx.messages) - 2); !ok {
		t.Fatal("the instant-error prompt's turn-flow lookup must find its committed log")
	}
	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())
	if !strings.Contains(plain, "go") || !strings.Contains(plain, "boom") {
		t.Errorf("instant-error turn must show prompt and failure through the flow:\n%s", plain)
	}
	if n := strings.Count(plain, "boom"); n != 1 {
		t.Errorf("failure text rendered %d times, want once:\n%s", n, plain)
	}
}
