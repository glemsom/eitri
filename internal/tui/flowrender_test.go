package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/glemsom/eitri/internal/config"
)

// renderFlowInput builds the smallest flowInput a flow test needs: a theme for
// the default theme and a generous width, with no Transcript or tool log in
// sight. The expansion config is supplied up front — default mode with
// reasoning expanded, so fixtures need no pinned force to show a reasoning
// body. The per-turn event log, the message snapshot, and any paired tool
// entries are supplied by each test.
func renderFlowInput(events []TimelineEvent, msg message, tools []flowTool) flowInput {
	return flowInput{
		Events:      events,
		Msg:         msg,
		Theme:       themeFor(config.DefaultTheme),
		ConfigTheme: config.DefaultTheme,
		Width:       100,
		Cfg:         expansionConfig{mode: viewDefault, cotExpanded: true},
		Tools: tools,
	}
}

// bashTool is one paired, completed bash tool entry for flow tests.
func bashTool() flowTool {
	now := time.Now()
	return flowTool{
		entry:    toolEntry{name: "bash", args: `{"command":"ls"}`, anchor: 0, complete: true, result: "a.go\nb.go", lines: 2, startedAt: now.Add(-time.Second), doneAt: now},
		logIdx:   0,
		expanded: false,
	}
}

func TestRenderFlow_committedRendersReasoningOnceAtFirstToolBoundary(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventReasoning, Delta: "think first"},
			{Kind: EventToolStart, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
			{Kind: EventToolResult, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
			{Kind: EventReasoning, Delta: "after tool"},
			{Kind: EventAnswer, Delta: "Done."},
		},
		message{reasoning: "think first", content: "Done.", thinkingRequested: true, expansion: ExpansionState{}},
		[]flowTool{bashTool()},
	)

	out, rows := RenderFlow(in)
	plain := ansiStrip(out)

	ri := strings.Index(plain, "think first")
	ti := strings.Index(plain, "$ bash")
	ai := strings.Index(plain, "Done.")
	if ri < 0 || ti < 0 || ai < 0 {
		t.Fatalf("committed flow missing segments ri=%d ti=%d ai=%d:\n%s", ri, ti, ai, plain)
	}
	// A committed turn's reasoning is one authoritative snapshot at the first
	// tool boundary (or the tail); a reasoning event that resumes after a tool
	// is never re-rendered as a second block.
	if !(ri < ti && ti < ai) {
		t.Errorf("committed flow must order reasoning < tool < answer, got %d, %d, %d:\n%s", ri, ti, ai, plain)
	}
	if n := strings.Count(plain, "think first"); n != 1 {
		t.Errorf("committed reasoning snapshot rendered %d times, want exactly once:\n%s", n, plain)
	}
	if strings.Contains(plain, "after tool") {
		t.Errorf("committed turn must not re-render resumed reasoning, got:\n%s", plain)
	}
	if len(rows) != 1 || rows[0].idx != 0 {
		t.Errorf("flow must report the tool row range with its log idx, got %+v", rows)
	}
}

// TestRenderFlow_committedReasoningSnapshotOnceAtTailWhenNoToolFollows locks the
// #451 contract for a committed turn that streams no tool: its reasoning is one
// authoritative snapshot (never split or re-rendered) and, with no tool boundary
// to anchor it, renders exactly once at the tail, directly before the answer.
func TestRenderFlow_committedReasoningSnapshotOnceAtTailWhenNoToolFollows(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventReasoning, Delta: "think first"},
			{Kind: EventReasoning, Delta: " think second"},
			{Kind: EventAnswer, Delta: "Done."},
		},
		message{reasoning: "think first think second", content: "Done.", thinkingRequested: true, expansion: ExpansionState{}},
		nil,
	)

	out, _ := RenderFlow(in)
	plain := ansiStrip(out)

	ri := strings.Index(plain, "think first")
	ai := strings.Index(plain, "Done.")
	if ri < 0 || ai < 0 {
		t.Fatalf("no-tool committed flow missing reasoning/answer (r=%d a=%d):\n%s", ri, ai, plain)
	}
	// With no tool boundary, the reasoning snapshot renders at the tail, sitting
	// directly before the final answer and never after it.
	if !(ri < ai) {
		t.Errorf("no-tool committed reasoning must sit at the tail before the answer, got r=%d a=%d:\n%s", ri, ai, plain)
	}
	if n := strings.Count(plain, "think first"); n != 1 {
		t.Errorf("no-tool committed reasoning rendered %d times, want exactly once at the tail:\n%s", n, plain)
	}
}

func TestRenderFlow_liveInterleavesReasoningFragmentsInEmissionOrder(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventReasoning, Delta: "reasoning one"},
			{Kind: EventToolStart, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
			{Kind: EventToolResult, Result: &ToolResult{Name: "bash", Result: "a.go", Lines: 1}},
			{Kind: EventReasoning, Delta: "reasoning two"},
			{Kind: EventAnswer, Delta: "final answer"},
		},
		message{reasoning: "reasoning one reasoning two", content: "final answer", streaming: true, thinkingRequested: true},
		[]flowTool{bashTool()},
	)

	out, _ := RenderFlow(in)
	plain := ansiStrip(out)

	r1 := strings.Index(plain, "reasoning one")
	tool := strings.Index(plain, "$ bash")
	r2 := strings.Index(plain, "reasoning two")
	answer := strings.Index(plain, "final answer")
	if r1 < 0 || tool < 0 || r2 < 0 || answer < 0 {
		t.Fatalf("live flow missing segments r1=%d t=%d r2=%d a=%d:\n%s", r1, tool, r2, answer, plain)
	}
	// A live turn flushes each reasoning fragment at the tool boundary it
	// precedes (a streaming turn) rather than one snapshot, so resumed reason-
	// ing lands between the tool and the answer in emission order.
	if !(r1 < tool) {
		t.Errorf("first reasoning must precede the tool, got r1=%d t=%d:\n%s", r1, tool, plain)
	}
	if !(tool < r2 && r2 < answer) {
		t.Errorf("resumed reasoning must sit between tool and answer, got t=%d r2=%d a=%d:\n%s", tool, r2, answer, plain)
	}
	for _, marker := range []string{"reasoning one", "reasoning two"} {
		if n := strings.Count(plain, marker); n != 1 {
			t.Errorf("reasoning marker %q rendered %d times, want exactly once:\n%s", marker, n, plain)
		}
	}
}

func TestRenderFlow_committedCollapsesReasoningToHint(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventReasoning, Delta: "hidden body"},
			{Kind: EventAnswer, Delta: "Done."},
		},
		message{reasoning: "hidden body", content: "Done.", thinkingRequested: true}, // no whole-block force
		nil,
	)
	in.Cfg = expansionConfig{mode: viewDefault} // collapsed-by-default config, the case under test

	out, _ := RenderFlow(in)
	plain := ansiStrip(out)
	if strings.Contains(plain, "hidden body") {
		t.Errorf("collapsed reasoning must hide the body, got:\n%s", plain)
	}
	if !strings.Contains(plain, "tok") {
		t.Errorf("collapsed reasoning must keep the 🤔 N tok hint, got:\n%s", plain)
	}
	if !strings.Contains(plain, "Done.") {
		t.Errorf("collapsed turn must still render the answer, got:\n%s", plain)
	}
}

func TestRenderFlow_committedAnswersInterleaveWithToolsInArrivalOrder(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventAnswer, Delta: "alpha, "},
			{Kind: EventToolStart, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
			{Kind: EventToolResult, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
			{Kind: EventAnswer, Delta: "and ls gave "},
			{Kind: EventToolStart, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
			{Kind: EventToolResult, Result: &ToolResult{Name: "bash", Result: "x", Lines: 1}},
			{Kind: EventAnswer, Delta: "gives-x-final"},
		},
		message{content: "alpha, and ls gave gives-x-final", thinkingRequested: true},
		[]flowTool{
			{entry: toolEntry{name: "read", args: `{"path":"a.txt"}`, anchor: 0, complete: true, result: "alpha", lines: 1}, logIdx: 0, expanded: false},
			{entry: toolEntry{name: "bash", args: `{"command":"ls"}`, anchor: 0, complete: true, result: "x", lines: 1}, logIdx: 1, expanded: false},
		},
	)

	out, _ := RenderFlow(in)
	plain := ansiStrip(out)

	partial := strings.Index(plain, "alpha,")
	t1 := strings.Index(plain, "read  a.txt")
	mid := strings.Index(plain, "and ls gave")
	t2 := strings.Index(plain, "bash  ls")
	last := strings.LastIndex(plain, "gives-x-final")
	if partial < 0 || t1 < 0 || mid < 0 || t2 < 0 || last < 0 {
		t.Fatalf("missing segments partial=%d t1=%d mid=%d t2=%d last=%d:\n%s", partial, t1, mid, t2, last, plain)
	}
	if !(partial < t1) {
		t.Errorf("partial answer must precede tool1, got partial=%d t1=%d:\n%s", partial, t1, plain)
	}
	if !(t1 < mid && mid < t2) {
		t.Errorf("mid answer must sit between tools, got t1=%d mid=%d t2=%d:\n%s", t1, mid, t2, plain)
	}
	if !(t2 < last) {
		t.Errorf("final answer must follow tool2, got t2=%d last=%d:\n%s", t2, last, plain)
	}
	for _, marker := range []string{"alpha,", "and ls gave", "gives-x-final"} {
		if n := strings.Count(plain, marker); n != 1 {
			t.Errorf("answer marker %q rendered %d times, want exactly once:\n%s", marker, n, plain)
		}
	}
}

func TestRenderFlow_stoppedTurnRendersMarkerOnFinalAnswer(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventAnswer, Delta: "partial"},
			{Kind: EventAnswer, Delta: " output"},
		},
		message{content: "partial output", stopped: true},
		nil,
	)

	out, _ := RenderFlow(in)
	plain := ansiStrip(out)
	if !strings.Contains(plain, "partial output") {
		t.Errorf("stopped turn must render its answer, got:\n%s", plain)
	}
	if !strings.Contains(plain, "stopped") {
		t.Errorf("stopped turn's final answer must carry the stopped marker, got:\n%s", plain)
	}
}

func TestRenderFlow_thinkingGateHidesReasoningBody(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	in := renderFlowInput(
		[]TimelineEvent{
			{Kind: EventReasoning, Delta: "secret"},
			{Kind: EventToolStart, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
			{Kind: EventToolResult, Result: &ToolResult{Name: "bash", Result: "x", Lines: 1}},
			{Kind: EventAnswer, Delta: "Done"},
		},
		message{content: "Done", thinkingRequested: false}, // thinking gate off
		[]flowTool{bashTool()},
	)

	out, _ := RenderFlow(in)
	plain := ansiStrip(out)
	if strings.Contains(plain, "secret") {
		t.Errorf("thinking-off turn must hide reasoning, got:\n%s", plain)
	}
	if !strings.Contains(plain, "$ bash") || !strings.Contains(plain, "Done") {
		t.Errorf("thinking-off turn must still render tool and answer, got:\n%s", plain)
	}
}

// TestRenderFlow_toolEntryExpansionDrivesBodyAndRows locks the FlowRenderer's
// tool-entry contract end to end: the caller's expansion decision (computed
// through the ExpansionState seam) selects summary vs full result body, and
// the reported row range covers every rendered row of the expanded card so the
// hit-test cannot drift from what rendered.
func TestRenderFlow_toolEntryExpansionDrivesBodyAndRows(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	events := []TimelineEvent{
		{Kind: EventToolStart, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
		{Kind: EventToolResult, Result: &ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2}},
	}

	collapsed, rows := RenderFlow(renderFlowInput(events, message{}, []flowTool{bashTool()}))
	p := ansiStrip(collapsed)
	if !strings.Contains(p, "2 lines") {
		t.Errorf("collapsed tool entry must render the line-count summary, got:\n%s", p)
	}
	if strings.Contains(p, "b.go") {
		t.Errorf("collapsed tool entry must not leak the result body, got:\n%s", p)
	}
	if len(rows) != 1 || rows[0].end != rows[0].start+1 || rows[0].idx != 0 {
		t.Errorf("collapsed tool entry must account exactly two rows (head + summary) for idx 0, got %+v", rows)
	}

	exp := bashTool()
	exp.expanded = true
	expanded, rows := RenderFlow(renderFlowInput(events, message{}, []flowTool{exp}))
	p = ansiStrip(expanded)
	if !strings.Contains(p, "a.go") || !strings.Contains(p, "b.go") {
		t.Errorf("expanded tool entry must render the full result body, got:\n%s", p)
	}
	if len(rows) != 1 || rows[0].end <= rows[0].start || rows[0].idx != 0 {
		t.Errorf("expanded tool entry row range must span multiple rows for idx 0, got %+v", rows)
	}
}
