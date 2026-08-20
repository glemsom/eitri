package tui

import (
	"strings"
	"testing"

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
				thinkingExpanded:  true,
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

func TestTranscript_flatFlowToolRowsRemainClickable(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := flowTranscript()
	tx.layout.dirty = true
	tx.ensureLayout()

	lines := tx.layout.plain
	head := -1
	for i, l := range lines {
		if strings.Contains(l, g("🔧 bash", "$ bash")) {
			head = i
			break
		}
	}
	if head < 0 {
		t.Fatalf("flat flow must render the tool head row, got plain rows:\n%s", strings.Join(lines, "\n"))
	}

	idx, collapsed, ok := tx.toolEntryAtLine(head)
	if !ok || idx != 0 || !collapsed {
		t.Errorf("toolEntryAtLine(%d) = idx %d collapsed %v ok %v, want entry 0 collapsed", head, idx, collapsed, ok)
	}

	// Click on the tool row expands it to its full result in the merged flow.
	tx.toggleToolEntry(0)
	var expanded strings.Builder
	tx.renderHistory(&expanded, nil, nil)
	if !strings.Contains(ansiStrip(expanded.String()), "a.go") {
		t.Errorf("clicked tool in the flat flow must expand to show its result, got:\n%s", ansiStrip(expanded.String()))
	}
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || collapsed {
		t.Errorf("expanded tool must report expanded at the same row, got %d/%v/%v", idx, collapsed, ok)
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
		timeline: []TimelineEvent{
			{Kind: EventReasoning, Seq: 0, Delta: "Reading the file."},
			{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}},
			{Kind: EventToolResult, Seq: 2, Result: &ToolResult{Name: "read", Result: "alpha", Lines: 1}},
			{Kind: EventAnswer, Seq: 3, Delta: "It is"},
			{Kind: EventAnswer, Seq: 4, Delta: " alpha"},
		},
	}

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
	tx.messages[1].thinkingExpanded = false // the turn completed (no expand-all mode)

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
