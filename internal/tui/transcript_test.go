package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestTranscript_rendersStandalone(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		messages:        []message{{role: "you", content: "hello"}},
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	content := hist.String()
	if !strings.Contains(content, "hello") {
		t.Errorf("standalone Transcript must render its messages, got: %q", content)
	}
	if strings.Contains(content, "workspace:") {
		t.Errorf("workspace must not render in the transcript header (it lives in the band status row), got: %q", content)
	}

	region := tx.renderHistoryViewport(content, 2) // reserve 2 rows (a synthetic band)
	if region == "" {
		t.Fatalf("standalone Transcript viewport render returned empty for content %q", content)
	}
	if n := strings.Count(region, "\n"); n > 11 {
		t.Errorf("viewport render exceeded reserved height (%d newline-terminated rows)", n)
	}
}

func TestTranscript_appendMsgAppendsAndInvalidates(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
	}
	tx.layout.dirty = false

	tx.appendMsg(helpView())

	if len(tx.messages) != 1 {
		t.Fatalf("appendMsg must append one message, got %d", len(tx.messages))
	}
	if tx.messages[0].role != "eitri" {
		t.Errorf("appendMsg message role = %q, want eitri", tx.messages[0].role)
	}
	if tx.messages[0].content != helpView() {
		t.Errorf("appendMsg must store the given content, got %q", tx.messages[0].content)
	}
	if !tx.layout.dirty {
		t.Error("appendMsg must mark the message layout dirty so the appended block re-wraps")
	}
}

func TestTranscript_thinkingGateScopesReasoningBlock(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(thinkingRequested bool) string {
		var hist strings.Builder
		tx := Transcript{
			theme:           th,
			configTheme:     config.DefaultTheme,
			reasoningEffort: "medium",
			messages: []message{{
				role:              "eitri",
				content:           "final answer",
				reasoning:         "sneaked chain-of-thought",
				thinkingRequested: thinkingRequested,
				expansion:         expansionWithReasoningForces(true, false),
				events:            []TimelineEvent{{Kind: EventAnswer, Delta: "final answer"}},
			}},
			histFollow:   true,
			histViewport: newHistoryViewport(),
		}
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	if off := render(false); strings.Contains(off, "🤔") || strings.Contains(ansiStrip(off), "sneaked chain-of-thought") {
		t.Errorf("thinking-off turn rendered a reasoning block, got: %q", off)
	}
	on := render(true)
	if !strings.Contains(on, "tok") {
		t.Errorf("thinking-on turn must render the reasoning hint, got: %q", on)
	}
	if !strings.Contains(ansiStrip(on), "sneaked chain-of-thought") {
		t.Errorf("expanded thinking-on turn must carry its reasoning, got: %q", on)
	}
}

func TestTranscript_expandAllOverridesThinkingExpansion(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(expandAll bool, forceExpand bool, forceCollapse bool) string {
		var hist strings.Builder
		tx := Transcript{
			theme:           th,
			configTheme:     config.DefaultTheme,
			reasoningEffort: "medium",
			expandAll:       expandAll,
			messages: []message{{
				role:              "eitri",
				content:           "final answer",
				reasoning:         "the reasoning body",
				thinkingRequested: true,
				expansion:         expansionWithReasoningForces(forceExpand, forceCollapse),
				events:            []TimelineEvent{{Kind: EventAnswer, Delta: "final answer"}},
			}},
			histFollow:   true,
			histViewport: newHistoryViewport(),
		}
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	if off := render(false, false, false); strings.Contains(ansiStrip(off), "the reasoning body") {
		t.Errorf("mode OFF collapsed: must not render the body, got: %q", off)
	}
	if on := render(true, false, false); !strings.Contains(ansiStrip(on), "the reasoning body") {
		t.Errorf("mode ON must render the body even when per-turn flag is false, got: %q", on)
	}
	if over := render(true, false, true); strings.Contains(ansiStrip(over), "the reasoning body") {
		t.Errorf("mode ON with a collapse override must render collapsed, got: %q", over)
	}
	if exp := render(false, true, false); !strings.Contains(ansiStrip(exp), "the reasoning body") {
		t.Errorf("mode OFF per-turn expanded must render the body, got: %q", exp)
	}
}

func TestTranscript_ownsRailSurface(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true,
			"eitri-1", "/tmp/eitri-1"),
	}

	if !tx.railVisible() {
		t.Fatalf("Transcript with a wired rail must report it visible")
	}

	if bw := tx.bandWidth(); bw != presizeTerminalWidth {
		t.Errorf("bandWidth = %d, want %d", bw, presizeTerminalWidth)
	}

	if bw, tw := tx.bandWidth(), tx.transcriptWidth(); tw >= bw {
		t.Errorf("transcriptWidth %d must be rail-shrunk below bandWidth %d", tw, bw)
	}

	tx.width, tx.height = 120, 40
	if c := tx.railClampHeight(4); c != 36 {
		t.Errorf("railClampHeight(band 4) = %d, want height-band=36", c)
	}

	pane := tx.renderPane("band\n")
	out := tx.viewWithRail(pane, 4)
	if !strings.Contains(out, "STATS") || !strings.Contains(out, "│") {
		t.Errorf("Transcript rail surface must render STATS + rail border, got: %q", out)
	}
}

func TestTranscript_dynamicRailWidth(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		rail: NewRail("opencode-go", "deepseek-v4-flash", "low", true,
			"eitri-1", "/tmp/eitri-1"),
		width:  120,
		height: 30,
	}

	if d := tx.railWidthOrDefault(); d != 30 {
		t.Errorf("railWidthOrDefault = %d, want default 30", d)
	}
	defaultTW := tx.transcriptWidth()
	if defaultTW != 120-2-31 {
		t.Errorf("transcriptWidth = %d at default width, want %d", defaultTW, 120-2-31)
	}

	tx.setRailWidth(22)
	if tx.railWidthOrDefault() != 22 {
		t.Errorf("railWidthOrDefault = %d after set, want 22", tx.railWidthOrDefault())
	}
	if tw := tx.transcriptWidth(); tw != 120-2-23 {
		t.Errorf("transcriptWidth = %d at railWidth 22, want %d", tw, 120-2-23)
	}
	if !tx.layout.dirty {
		t.Errorf("setRailWidth must mark the layout cache dirty (re-wrap trigger), got clean")
	}
	rails := strings.Split(tx.viewWithRail(tx.renderPane("band\n"), 4), "\n")
	bordered := false
	for _, ln := range rails {
		if w := lipgloss.Width(ln); w > 120 {
			t.Errorf("surface row width %d exceeds terminal 120 at railWidth 22: %q", w, ln)
		}
		if strings.Contains(ln, g("│", "|")) {
			bordered = true
		}
	}
	if !bordered {
		t.Errorf("rail must still render its left border at width 22, got:\n%q", strings.Join(rails, "\n"))
	}
}

func TestTranscript_matchesModelRender(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	msgs := []message{{role: "you", content: "hello"}, {role: "eitri", content: "**plain** answer"}}
	tx := Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		messages:        msgs,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}
	m := NewModelCfg(Dependencies{WorkspacePath: "/tmp/acme", Config: config.Config{Theme: config.DefaultTheme, ReasoningEffort: "medium"}})
	m.tx.messages = msgs
	m.tx.width = 80
	m.tx.height = 12

	var viaModel strings.Builder
	m.tx.renderHistory(&viaModel, nil, nil)

	var viaTranscript strings.Builder
	tx.renderHistory(&viaTranscript, nil, nil)

	if viaModel.String() != viaTranscript.String() {
		t.Errorf("Model render diverged from an equal standalone Transcript:\nmodel:      %q\ntranscript: %q",
			viaModel.String(), viaTranscript.String())
	}
}

func transcriptScrollModel(t *testing.T) *Transcript {
	t.Helper()
	m := newTallHistoryModel(t)
	m = resizeTo(t, m, 120, 12)
	tx := m.tx // the stable Transcript root, navigated in place
	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	tx.renderHistoryViewport(hist.String(), m.bandHeight())
	if vp := tx.histViewport; vp.TotalLineCount() <= vp.Height() {
		t.Fatalf("test must overflow: viewport lines (%d) should exceed height (%d)", vp.TotalLineCount(), vp.Height())
	}
	return tx
}

func TestTranscript_navigateScrollsSharedViewport(t *testing.T) {
	tx := transcriptScrollModel(t)
	start := tx.histViewport.YOffset()
	if start <= 0 {
		t.Fatalf("overflowed follow should start at the bottom, got offset %d", start)
	}

	if follow := tx.navigateHistory("pgup"); follow {
		t.Errorf("PgUp must break follow, got follow=true")
	}
	if up := tx.histViewport.YOffset(); up >= start {
		t.Errorf("PgUp must move the viewport up: offset %d -> %d", start, up)
	}

	tx.navigateHistory("home")
	if top := tx.histViewport.YOffset(); top != 0 {
		t.Errorf("Home should jump to the transcript top, got offset %d", top)
	}

	tx.navigateHistory("pgdown")
	if down := tx.histViewport.YOffset(); down <= 0 {
		t.Errorf("PgDn must move the viewport down from the top, got offset %d", down)
	}
	tx.navigateHistory("end")
	if !tx.histViewport.AtBottom() {
		t.Errorf("End should jump to the transcript bottom, got offset %d", tx.histViewport.YOffset())
	}
	if follow := tx.navigateHistory("end"); !follow {
		t.Errorf("End reaching the bottom should re-engage follow, got follow=false")
	}

	wheelStart := tx.histViewport.YOffset()
	if follow := tx.navigateMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp}); follow {
		t.Errorf("wheel up must break follow, got follow=true")
	}
	if up := tx.histViewport.YOffset(); up >= wheelStart {
		t.Errorf("wheel up must scroll up, offset %d -> %d", wheelStart, up)
	}
}

func transcriptWithTool(t *testing.T) Transcript {
	t.Helper()
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	var log toolLog
	log.SetAnchor(0)
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	log.Apply(ToolUpdate{Result: &ToolResult{Name: "bash", Result: "a.go\nb.go\n", Lines: 2}})
	return Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		messages: []message{
			{role: "you", content: "run it"},
			// The committed turn's event log mirrors what commitTimeline
			// attaches in production: one start/result pair per log entry.
			{role: "eitri", events: []TimelineEvent{
				{Kind: EventToolStart},
				{Kind: EventToolResult},
			}},
		},
		log:    log,
		width:  80,
		height: 12,
		layout: transcriptLayout{dirty: true},
	}
}

func TestTranscript_toolEntryAtLineReadsPersistentIndex(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.ensureLayout() // build the persistent row->entry index once
	if len(tx.layout.rows) == 0 {
		t.Fatalf("hit-test must record a tool-entry row index")
	}
	head := tx.layout.rows[0].start

	idx, collapsed, ok := tx.toolEntryAtLine(head)
	if !ok || idx != 0 || !collapsed {
		t.Errorf("toolEntryAtLine(%d) = %d/%v/%v, want entry 0 collapsed=ok", head, idx, collapsed, ok)
	}

	for i := 0; i < 20; i++ {
		if idx, _, ok := tx.toolEntryAtLine(head); !ok || idx != 0 {
			t.Errorf("toolEntryAtLine(%d) = %d/%v, want entry 0 (reused index)", head, idx, ok)
		}
	}
	if tx.layout.builds != 1 {
		t.Errorf("hit-test must build the layout exactly once, got builds=%d", layoutBuildsOf(tx))
	}

	if _, _, ok := tx.toolEntryAtLine(99); ok {
		t.Errorf("toolEntryAtLine(99) must be out of range")
	}
}

func TestTranscript_toggleToolEntryFlipsExpansion(t *testing.T) {
	tx := transcriptWithTool(t)

	tx.toggleToolEntry(0)
	if !tx.toolExpandedFor(0) {
		t.Errorf("toggleToolEntry(0) must expand entry 0")
	}
	tx.toggleToolEntry(0)
	if tx.toolExpandedFor(0) {
		t.Errorf("second toggleToolEntry(0) must collapse entry 0")
	}
}

func TestTranscript_applyFoldsToolUpdate(t *testing.T) {
	tx := transcriptWithTool(t)
	before := tx.log.Len()

	applyTool(&tx, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	applyTool(&tx, ToolUpdate{Result: &ToolResult{Name: "read", Result: "contents", Lines: 1}})

	if tx.log.Len() != before+1 {
		t.Fatalf("apply must fold a new entry, got len before=%d after=%d", before, tx.log.Len())
	}
	e := tx.log.Entry(tx.log.Len() - 1)
	if e.name != "read" || e.result != "contents" || !e.complete {
		t.Errorf("apply result entry = %+v, want read/contents/complete", e)
	}
}

func TestTranscript_toggleExpandAllFlipsGlobalMode(t *testing.T) {
	tx := transcriptWithTool(t)
	if tx.expandAll {
		t.Fatalf("transcript should start collapsed (expanded view off)")
	}
	if v := tx.toggleExpandAll(); !v || !tx.expandAll {
		t.Errorf("toggleExpandAll must expand all, got value=%v field=%v", v, tx.expandAll)
	}
	if v := tx.toggleExpandAll(); v || tx.expandAll {
		t.Errorf("second toggleExpandAll must collapse all, got value=%v field=%v", v, tx.expandAll)
	}
	if v := tx.toggleExpandAll(); !v || !tx.expandAll {
		t.Errorf("third toggleExpandAll must expand all again, got value=%v field=%v", v, tx.expandAll)
	}
}

func TestTranscript_toggleExpandAllEmptyLogIsNoOp(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.log.entries = tx.log.entries[:0]
	if tx.toggleExpandAll() != true {
		t.Fatalf("toggleExpandAll on empty log must turn the mode on")
	}
	var b strings.Builder
	tx.renderHistory(&b, nil, nil)
	if strings.Contains(b.String(), "🔧 bash") {
		t.Errorf("empty log must render no tool entries, got: %q", b.String())
	}
	if tx.expandAll != true {
		t.Errorf("expandAll must stay flipped after rendering an empty log")
	}
}

func TestTranscript_expandAllPerEntryCollapseOverride(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.expandAll = true

	var on strings.Builder
	tx.renderHistory(&on, nil, nil)
	if !strings.Contains(on.String(), "a.go") {
		t.Fatalf("with expandAll on, entry must render its full result, got: %q", on.String())
	}

	tx.toggleToolEntry(0)
	var off strings.Builder
	tx.renderHistory(&off, nil, nil)
	if strings.Contains(off.String(), "a.go") {
		t.Errorf("per-entry collapse must hide the result even in expanded-view mode, got: %q", off.String())
	}

	tx.toggleToolEntry(0)
	var again strings.Builder
	tx.renderHistory(&again, nil, nil)
	if !strings.Contains(again.String(), "a.go") {
		t.Errorf("second per-entry toggle must re-expand in expanded-view mode, got: %q", again.String())
	}
}

func TestTranscript_toolEntryAtLineEffectiveCollapse(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.ensureLayout()
	head := tx.layout.rows[0].start

	tx.expandAll = true
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || collapsed {
		t.Errorf("expanded mode: entry 0 must report expanded, got %d/%v/%v", idx, collapsed, ok)
	}

	tx.toggleToolEntry(0)
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || !collapsed {
		t.Errorf("override collapse: entry 0 must report collapsed, got %d/%v/%v", idx, collapsed, ok)
	}

	tx.toggleToolEntry(0)
	if idx, collapsed, ok := tx.toolEntryAtLine(head); !ok || idx != 0 || collapsed {
		t.Errorf("override released: entry 0 must report expanded, got %d/%v/%v", idx, collapsed, ok)
	}
}

func TestTranscript_expandAllOffDoesNotWipePerEntry(t *testing.T) {
	tx := transcriptWithTool(t)
	tx.toggleToolEntry(0)
	var one strings.Builder
	tx.renderHistory(&one, nil, nil)
	if !strings.Contains(one.String(), "a.go") {
		t.Fatalf("per-entry open must show the result in default mode, got: %q", one.String())
	}

	tx.toggleExpandAll()
	tx.toggleExpandAll()
	if !tx.toolExpandedFor(0) {
		t.Fatalf("toggling expandAll must not wipe per-entry expansion state")
	}
	var back strings.Builder
	tx.renderHistory(&back, nil, nil)
	if !strings.Contains(back.String(), "a.go") {
		t.Errorf("per-entry open must persist after an expandAll on/off cycle, got: %q", back.String())
	}
}

func layoutBuildsOf(tx Transcript) int {
	return tx.layout.builds
}

func newStreamPaneTestTranscript(th Theme, msgs []message) Transcript {
	for i, m := range msgs {
		if m.role == "eitri" && len(m.events) == 0 {
			msgs[i].events = synthAnswerLog(m.content)
		}
	}
	return Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		messages:        msgs,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
	}
}

// liveReasoningFlowTranscript builds a Transcript mid-live-turn whose
// in-progress assistant streams one reasoning delta on the per-turn timeline —
// the merged flat-flow path a live reasoning block now renders through, in
// place of the three-pane layout's standalone-message fallback.
func liveReasoningFlowTranscript(reasoning string) *Transcript {
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		busy:            true,
		messages: []message{
			{role: "you", content: "hi"},
			{role: "eitri", reasoning: reasoning, streaming: true, thinkingRequested: true},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventReasoning, Seq: 0, Delta: reasoning},
	})
	return tx
}

// committedReasoningFlowTranscript builds a completed turn whose committed
// event log walks a reasoning delta then the answer — the flat flow a finished
// reasoning block renders through after finalize.
func committedReasoningFlowTranscript(reasoning, answer string) *Transcript {
	th := themeFor(config.DefaultTheme)
	return &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           80,
		height:          12,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		messages: []message{
			{role: "you", content: "hi"},
			{role: "eitri", content: answer, reasoning: reasoning, thinkingRequested: true,
				events: []TimelineEvent{
					{Kind: EventReasoning, Seq: 0, Delta: reasoning},
					{Kind: EventAnswer, Seq: 1, Delta: answer},
				}},
		},
	}
}

func TestRenderHistory_streamingAssistantUsesDimmedPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(streaming bool) string {
		var hist strings.Builder
		tx := newStreamPaneTestTranscript(th, []message{{role: "eitri", content: "partial", streaming: streaming}})
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	streaming := render(true)
	completed := render(false)

	if !strings.Contains(ansiStrip(streaming), "partial") {
		t.Fatalf("streaming render must contain message content, got: %q", streaming)
	}
	if !strings.Contains(ansiStrip(completed), "partial") {
		t.Fatalf("completed render must contain message content, got: %q", completed)
	}

	if streaming == completed {
		t.Errorf("streaming pane must differ from completed pane (streaming branch not taken)")
	}

	streamingColor := borderColorCode(streaming)
	agentColor := borderColorCode(completed)
	if streamingColor == agentColor {
		t.Errorf("streaming border color %q must differ from completed border color %q", streamingColor, agentColor)
	}

	expectedStreamColor := borderColorStr(th.streamingPaneStyle)
	if streamingColor != expectedStreamColor {
		t.Errorf("streaming border must use streamingPaneStyle color %q, got %q", expectedStreamColor, streamingColor)
	}
}

func TestRenderHistory_completedAssistantHasNoCopyHostileLeftBar(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	var hist strings.Builder
	tx := newStreamPaneTestTranscript(th, []message{{role: "eitri", content: "done", streaming: false}})
	tx.renderHistory(&hist, nil, nil)
	rendered := hist.String()

	line := lineContaining(rendered, "done")
	if line == "" {
		t.Fatalf("completed render must contain message content, got: %q", rendered)
	}
	if strings.Contains(ansiStrip(line), g("│", "|")) {
		t.Errorf("completed assistant reply must not render a left bar, got line: %q", line)
	}
}

func reasoningLineInfo(s, body string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(ansiStrip(line), body) {
			continue
		}
		framed := strings.Contains(line, g("\u2502", "|"))
		start := strings.Index(line, "\x1b[38;2;")
		if start == -1 {
			return "", framed
		}
		end := strings.IndexByte(line[start+len("\x1b[38;2;"):], 'm')
		if end == -1 {
			return "", framed
		}
		return line[start+len("\x1b[38;2;") : start+len("\x1b[38;2;")+end], framed
	}
	return "", false
}

func TestRenderHistory_liveReasoningBlockUsesStreamingPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(streaming bool) string {
		var hist strings.Builder
		var tx *Transcript
		if streaming {
			tx = liveReasoningFlowTranscript("the live reasoning body")
		} else {
			tx = committedReasoningFlowTranscript("the live reasoning body", "final answer")
			tx.messages[1].expansion.set(blockReasoning, reasoningWholeID, true)
		}
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	const body = "the live reasoning body"

	live := render(true)
	liveColor, liveFramed := reasoningLineInfo(live, body)
	if !liveFramed {
		t.Errorf("live reasoning body must render inside a pane, got line info for: %q", live)
	}
	if liveColor != borderColorStr(th.streamingThinkingPaneStyle) {
		t.Errorf("live reasoning block must use streamingThinkingPaneStyle color, got %q", liveColor)
	}

	done := render(false)
	doneColor, doneFramed := reasoningLineInfo(done, body)
	if !doneFramed {
		t.Errorf("completed reasoning body must render inside a pane, got line info for: %q", done)
	}
	if doneColor != borderColorStr(th.thinkingPaneStyle) {
		t.Errorf("completed reasoning block must use thinkingPaneStyle color, got %q", doneColor)
	}
}

func TestRenderHistory_liveReasoningRespectsTabCollapse(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveReasoningFlowTranscript("hidden reasoning")
	tx.messages[1].expansion.set(blockReasoning, reasoningWholeID, false)

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	if strings.Contains(ansiStrip(hist.String()), "hidden reasoning") {
		t.Errorf("tab-collapsed live reasoning block must render collapsed, got: %q", hist.String())
	}
}

func TestRenderHistory_liveReasoningBlockRespectsThinkingGate(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	tx := liveReasoningFlowTranscript("sneaked reasoning")
	tx.messages[1].thinkingRequested = false

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	rendered := hist.String()
	if strings.Contains(ansiStrip(rendered), "sneaked reasoning") {
		t.Errorf("thinking-off live turn must not render the reasoning block, got: %q", rendered)
	}
}

func TestTranscript_liveReasoningBlockTogglesViaTab(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")

	tx := liveReasoningFlowTranscript("visible reasoning")

	var live strings.Builder
	tx.renderHistory(&live, nil, nil)
	if !strings.Contains(ansiStrip(live.String()), "visible reasoning") {
		t.Fatalf("streaming reasoning must render its body expanded, got: %q", live.String())
	}

	tx.toggleThinkingFragment(1, 0)
	var collapsed strings.Builder
	tx.renderHistory(&collapsed, nil, nil)
	if strings.Contains(ansiStrip(collapsed.String()), "visible reasoning") {
		t.Errorf("tab must collapse the live reasoning block, got: %q", collapsed.String())
	}

	tx.toggleThinkingFragment(1, 0)
	var reexpanded strings.Builder
	tx.renderHistory(&reexpanded, nil, nil)
	if !strings.Contains(ansiStrip(reexpanded.String()), "visible reasoning") {
		t.Errorf("tab must re-expand the live reasoning block, got: %q", reexpanded.String())
	}

	// A completed (finalized) turn collapses its reasoning to the hint by
	done := committedReasoningFlowTranscript("visible reasoning", "final answer")
	var doneStr strings.Builder
	done.renderHistory(&doneStr, nil, nil)
	donePlain := ansiStrip(doneStr.String())
	if strings.Contains(donePlain, "visible reasoning") {
		t.Errorf("a completed turn's reasoning block must collapse to the hint, got: %q", donePlain)
	}
	if !strings.Contains(donePlain, "tok") {
		t.Errorf("a completed turn's collapsed reasoning must keep the 🤔 N tok hint, got: %q", donePlain)
	}
}

func TestRenderHistory_streamingErrorPrefixUsesDimmedErrorPane(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	render := func(streaming bool) string {
		var hist strings.Builder
		tx := newStreamPaneTestTranscript(th, []message{{role: "eitri", content: failurePrefix() + "broke", streaming: streaming}})
		tx.renderHistory(&hist, nil, nil)
		return hist.String()
	}

	streaming := render(true)
	completed := render(false)

	if !strings.Contains(ansiStrip(streaming), failurePrefix()) {
		t.Fatalf("streaming error render must contain the error prefix, got: %q", streaming)
	}
	if !strings.Contains(ansiStrip(completed), failurePrefix()) {
		t.Fatalf("completed error render must contain the error prefix, got: %q", completed)
	}

	streamingColor := borderColorCode(streaming)
	completedColor := borderColorCode(completed)
	if streamingColor == completedColor {
		t.Errorf("streaming error border color %q must differ from completed error border color %q", streamingColor, completedColor)
	}

	expectedColor := borderColorStr(th.streamingErrorPaneStyle)
	if streamingColor != expectedColor {
		t.Errorf("streaming error must use streamingErrorPaneStyle color %q, got %q", expectedColor, streamingColor)
	}
}

func borderColorCode(s string) string {
	for _, line := range strings.Split(s, "\n") {
		start := strings.Index(line, "\x1b[38;2;")
		if start == -1 {
			continue
		}
		end := strings.IndexByte(line[start+len("\x1b[38;2;"):], 'm')
		if end == -1 {
			continue
		}
		return line[start+len("\x1b[38;2;") : start+len("\x1b[38;2;")+end]
	}
	return ""
}

func borderColorStr(style lipgloss.Style) string {
	c := style.GetBorderLeftForeground()
	if c == nil {
		return ""
	}
	r, g2, b, _ := c.RGBA()
	return fmt.Sprintf("%d;%d;%d", r>>8, g2>>8, b>>8)
}
