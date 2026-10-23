package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/tui/telemetry"
)

// The construction seam. NewTranscript is the single production construction
// site: it takes the varying inputs as one config and owns its own defaults
// (follow on, a fresh history viewport, a dirty layout).

func TestNewTranscriptAppliesConfigAndInvariants(t *testing.T) {
	cfg := TranscriptConfig{
		Theme:           "tokyo-night",
		ReasoningEffort: "high",
		Telemetry:       telemetry.NewTelemetry("deepseek-v4-flash", "high", true, 250),
		Rail:            &Rail{},
		RailWidth:       42,
		// CoTCollapsedByDefault / ToolResultsCollapsedByDefault zero -> expanded.
	}
	tx := NewTranscript(cfg)

	if tx.ConfigTheme() != "tokyo-night" {
		t.Errorf("NewTranscript configTheme = %q, want tokyo-night", tx.ConfigTheme())
	}
	if tx.Theme().accent == themeFor(config.DefaultTheme).accent {
		t.Error("NewTranscript must derive the theme from the config name, got the default accent")
	}
	if tx.reasoningEffort != "high" {
		t.Errorf("NewTranscript reasoningEffort = %q, want high", tx.reasoningEffort)
	}
	if tx.telemetry != cfg.Telemetry {
		t.Error("NewTranscript did not carry the telemetry through")
	}
	if tx.rail != cfg.Rail {
		t.Error("NewTranscript did not carry the rail through")
	}
	if tx.railWidth != 42 {
		t.Errorf("NewTranscript railWidth = %d, want 42", tx.railWidth)
	}
	if !tx.cotExpanded || !tx.toolResultsExpanded {
		t.Error("NewTranscript must default the collapse flags to expanded when not told collapsed")
	}
	if !tx.histFollow {
		t.Error("NewTranscript must default follow on")
	}
	if !tx.layout.dirty {
		t.Error("NewTranscript must come out with a dirty layout")
	}
	if tx.histViewport.Width() != 0 || tx.histViewport.Height() != 0 || tx.histViewport.MouseWheelEnabled {
		t.Errorf("NewTranscript must start with a fresh empty viewport, got %dx%d wheel=%v",
			tx.histViewport.Width(), tx.histViewport.Height(), tx.histViewport.MouseWheelEnabled)
	}
}

func TestNewTranscriptCollapseFlagsByDefault(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{
		CoTCollapsedByDefault:         true,
		ToolResultsCollapsedByDefault: true,
	})
	if tx.cotExpanded {
		t.Error("cotExpanded = true, want false when CoTCollapsedByDefault set")
	}
	if tx.toolResultsExpanded {
		t.Error("toolResultsExpanded = true, want false when ToolResultsCollapsedByDefault set")
	}
}

func TestApplySettingsAppliesReasoningEffort(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{ReasoningEffort: "low"})
	tx.ApplySettings(config.Config{ReasoningEffort: "high"})
	if tx.reasoningEffort != "high" {
		t.Errorf("ApplySettings reasoningEffort = %q, want high", tx.reasoningEffort)
	}
}

func TestThemeQueriesReportResolvedPalette(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{Theme: "dracula"})
	if got := tx.ConfigTheme(); got != "dracula" {
		t.Errorf("ConfigTheme = %q, want dracula", got)
	}
	if got := tx.Theme().accent; got != lipgloss.Color("#BD93F9") {
		t.Errorf("Theme().accent = %v, want the dracula accent", got)
	}

	tx.ApplySettings(config.Config{Theme: config.DefaultTheme})
	if got := tx.ConfigTheme(); got != config.DefaultTheme {
		t.Errorf("ConfigTheme after save = %q, want %q", got, config.DefaultTheme)
	}
	if got := tx.Theme().accent; got != lipgloss.Color("#7AA2F7") {
		t.Errorf("Theme().accent after save = %v, want the default accent", got)
	}
}

func TestPlainLinesQueryReturnsCopySpace(t *testing.T) {
	tx := newTestTx()
	tx.SetSize(100, 30)
	projectStart(&tx, "run the thing", false)
	projectStream(&tx, AnswerStream, "final answer")

	lines := tx.PlainLines()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"run the thing", "final answer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("PlainLines must carry the history text %q, got:\n%s", want, joined)
		}
	}
	for i, line := range lines {
		if ansiStrip(line) != line {
			t.Errorf("PlainLines row %d = %q carries styling; the copy space must be ANSI-stripped", i, line)
		}
	}
}

func historyRowIndex(t *testing.T, lines []string, marker string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, marker) {
			return i
		}
	}
	t.Fatalf("marker %q not found in the history:\n%s", marker, strings.Join(lines, "\n"))
	return -1
}

func TestToolCardAtQueryResolvesTheEntry(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(context.Context, string, string) (TurnResult, error) {
			return TurnResult{Answer: "final answer"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "full output line one", Lines: 1})
	view(m)

	lines := m.tx.PlainLines()
	head := historyRowIndex(t, lines, "bash")
	body := historyRowIndex(t, lines, "full output line one")
	answer := historyRowIndex(t, lines, "final answer")

	idx, ok := m.tx.ToolCardAt(head)
	if !ok || idx != 0 {
		t.Errorf("ToolCardAt(tool head line %d) = (%d,%v), want the single entry 0", head, idx, ok)
	}
	if got, ok := m.tx.ToolCardAt(body); !ok || got != idx {
		t.Errorf("ToolCardAt(tool body line %d) = (%d,%v), want the same entry %d", body, got, ok, idx)
	}
	if _, ok := m.tx.ToolCardAt(answer); ok {
		t.Errorf("ToolCardAt(answer line %d) reported a tool card; answer text is not one", answer)
	}

	m.tx.ToggleToolEntry(idx)
	if collapsed := strings.Join(m.tx.PlainLines(), "\n"); strings.Contains(collapsed, "full output line one") {
		t.Errorf("toggling the entry ToolCardAt resolved must collapse that card, got:\n%s", collapsed)
	}
}

func TestRenderedLineQueryCarriesHyperlink(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(context.Context, string, string) (TurnResult, error) {
			return TurnResult{Answer: "[Example](https://example.com/path)"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "show a link")
	m = submitAndWait(t, m)

	lines := m.tx.PlainLines()
	line := historyRowIndex(t, lines, "Example")

	rendered := m.tx.RenderedLine(line)
	if ansiStrip(rendered) != lines[line] {
		t.Errorf("RenderedLine(%d) = %q, want the plain copy-space row %q", line, rendered, lines[line])
	}
	col := lipgloss.Width(lines[line][:strings.Index(lines[line], "Example")])
	if target, linked := hyperlinkAt(rendered, col); !linked || target != "https://example.com/path" {
		t.Errorf("hyperlinkAt(RenderedLine(%d), %d) = %q/%v, want the row's OSC 8 target", line, col, target, linked)
	}
	if past := m.tx.RenderedLine(len(lines) + 3); past != "" {
		t.Errorf("RenderedLine past the content = %q, want empty", past)
	}
}

// The input commands. The key and mouse handlers reach the transcript's
// selection, scroll, focus and expansion state through these commands instead of
// writing the fields, so the interaction state has one owner.

func TestPointerCommandsResolveDragToCopyText(t *testing.T) {
	tx := newTestTx()
	tx.SetSize(100, 30)
	projectStart(&tx, "run the thing", false)
	projectStream(&tx, AnswerStream, "final answer")

	lines := tx.PlainLines()
	line := historyRowIndex(t, lines, "final answer")
	row := lines[line]
	col := len([]rune(row[:strings.Index(row, "final")]))

	if tx.PointerActive() {
		t.Fatal("a transcript with no pointer input must not report a drag in progress")
	}
	tx.PointerDown(line, col)
	if !tx.PointerActive() {
		t.Fatal("a pointer press must anchor a drag selection")
	}
	tx.PointerMove(line, col+len("final")-1)

	out := tx.PointerUp()
	if !out.Dragged || !out.InBounds {
		t.Fatalf("drag release = %+v, want the in-bounds drag it completed", out)
	}
	if out.Text != "final" {
		t.Errorf("drag release text = %q, want the %q the drag covered", out.Text, "final")
	}
	if out.Clicked {
		t.Errorf("a drag ending off its anchor is not a tool-card click: %+v", out)
	}
	if tx.PointerActive() {
		t.Error("the release must end the drag")
	}
	if again := tx.PointerUp(); again.Dragged || again.Clicked {
		t.Errorf("a release with no drag behind it must resolve nothing, got %+v", again)
	}
}

func TestPointerCommandsResolveToolCardClick(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(context.Context, string, string) (TurnResult, error) {
			return TurnResult{Answer: "final answer"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "full output line one", Lines: 1})
	view(m)

	head := historyRowIndex(t, m.tx.PlainLines(), "bash")
	m.tx.PointerDown(head, 2)
	out := m.tx.PointerUp()
	if out.Dragged {
		t.Errorf("a press and release on one cell is a click, not a drag: %+v", out)
	}
	if !out.Clicked || out.ToolIdx != 0 {
		t.Fatalf("release on a tool card = %+v, want the single entry 0", out)
	}
	m.tx.ToggleToolEntry(out.ToolIdx)
	if collapsed := strings.Join(m.tx.PlainLines(), "\n"); strings.Contains(collapsed, "full output line one") {
		t.Errorf("toggling the entry the click resolved must collapse that card, got:\n%s", collapsed)
	}

	answer := historyRowIndex(t, m.tx.PlainLines(), "final answer")
	m.tx.PointerDown(answer, 2)
	if click := m.tx.PointerUp(); click.Clicked || click.Dragged {
		t.Errorf("a click on answer text resolved %+v, want nothing", click)
	}
}

func TestPointerCancelDropsTheDrag(t *testing.T) {
	tx := newTestTx()
	tx.SetSize(100, 30)
	projectStart(&tx, "run the thing", false)
	projectStream(&tx, AnswerStream, "final answer")
	line := historyRowIndex(t, tx.PlainLines(), "final answer")

	tx.PointerMove(line, 3)
	if tx.PointerActive() {
		t.Error("a motion with no press behind it must not start a drag")
	}

	tx.PointerDown(line, 0)
	tx.PointerCancel()
	if tx.PointerActive() {
		t.Error("a press outside the history region must drop the drag")
	}
	if out := tx.PointerUp(); out.Dragged || out.Clicked {
		t.Errorf("a release after a drop must resolve nothing, got %+v", out)
	}
}

func TestScrollCommandsPauseAndResumeFollow(t *testing.T) {
	tx := transcriptScrollModel(t)
	start := tx.histViewport.YOffset()
	if start <= 0 {
		t.Fatalf("test needs overflow scrolled to the newest output, got offset %d", start)
	}

	if follow := tx.NavigateHistory("pgup"); follow {
		t.Error("PgUp must break follow and report it off")
	}
	if up := tx.histViewport.YOffset(); up >= start {
		t.Errorf("PgUp must move toward older output: offset %d -> %d", start, up)
	}
	if follow := tx.NavigateHistory("end"); !follow {
		t.Error("End must re-engage follow and report it on")
	}
	if !tx.histViewport.AtBottom() {
		t.Errorf("End must land on the newest output, got offset %d", tx.histViewport.YOffset())
	}

	if follow := tx.NavigateMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp, Y: 0}); follow {
		t.Error("a wheel up inside the region must break follow and report it off")
	}
	scrolled := tx.histViewport.YOffset()
	if follow := tx.NavigateMouse(tea.MouseWheelMsg{Button: tea.MouseWheelUp, Y: tx.Geometry().Height}); follow {
		t.Error("a wheel below the scroll region must not change follow")
	}
	if got := tx.histViewport.YOffset(); got != scrolled {
		t.Errorf("a wheel below the scroll region must not move the reading position: %d -> %d", scrolled, got)
	}

	follow := false
	for i := 0; i < 500 && !follow; i++ {
		follow = tx.NavigateMouse(tea.MouseWheelMsg{Button: tea.MouseWheelDown, Y: 0})
	}
	if !follow {
		t.Fatalf("wheeling down to the newest output must re-engage follow, got offset %d", tx.histViewport.YOffset())
	}
}

func TestResumeFollowCommandReEngagesFollow(t *testing.T) {
	tx := transcriptScrollModel(t)
	tx.NavigateHistory("pgup")
	if tx.histFollow {
		t.Fatal("test precondition: paging toward older output must pause follow")
	}
	paused := tx.histViewport.YOffset()

	tx.ResumeFollow()
	if !tx.histFollow {
		t.Error("a new prompt must re-engage follow")
	}
	if got := tx.histViewport.YOffset(); got != paused {
		t.Errorf("re-engaging follow must not move the reading position: %d -> %d", paused, got)
	}
}

// TestFocusCommandsMoveTheBlockCursor locks the per-block input commands: the
// first FocusNext activates the cursor on the first collapsible block, and
// ToggleFocused expands the block the cursor is on.
func TestFocusCommandsMoveTheBlockCursor(t *testing.T) {
	tx := transcriptWithTool(t)
	if _, ok := tx.focused(); ok {
		t.Fatal("test precondition: no block is focused before the first Tab")
	}

	tx.FocusNext()
	blk, ok := tx.focused()
	if !ok || blk.kind != blockTool || blk.toolIdx != 0 {
		t.Fatalf("first FocusNext = %+v ok=%v, want the transcript's single tool entry", blk, ok)
	}
	if tx.toolExpandedFor(0) {
		t.Fatal("test precondition: the tool entry starts collapsed")
	}
	tx.ToggleFocused()
	if !tx.toolExpandedFor(0) {
		t.Error("ToggleFocused must expand the focused block")
	}
}

// TestToggleExpandedModeCommandFlipsTheGlobalModes locks the ctrl+e command: the
// first call expands every collapsible block, the second collapses them all to
// their hints, and the third expands again.
func TestToggleExpandedModeCommandFlipsTheGlobalModes(t *testing.T) {
	tx := transcriptWithTool(t)
	rendered := func() string {
		var hist strings.Builder
		tx.renderHistory(&hist, nil, nil)
		return ansiStrip(hist.String())
	}
	if strings.Contains(rendered(), "a.go") {
		t.Fatal("test precondition: the tool result starts collapsed")
	}

	if expand := tx.ToggleExpandedMode(); !expand {
		t.Error("the first toggle must enter expand-all")
	}
	if !strings.Contains(rendered(), "a.go") {
		t.Errorf("expand-all must reveal the collapsed tool result, got:\n%s", rendered())
	}

	if expand := tx.ToggleExpandedMode(); expand {
		t.Error("the second toggle must leave expand-all for collapse-all")
	}
	if strings.Contains(rendered(), "a.go") {
		t.Errorf("collapse-all must hide the tool result again, got:\n%s", rendered())
	}

	if expand := tx.ToggleExpandedMode(); !expand {
		t.Error("the third toggle must return to expand-all")
	}
}

// The seam's outer members: the note-append, pulse and geometry calls the rest
// of package tui makes. Each is exported, so a caller reaches the transcript
// only through its interface.

func TestAppendAssistantMsgSeamAppendsANote(t *testing.T) {
	tx := newTestTx()

	tx.AppendAssistantMsg("login saved")

	if len(tx.messages) != 1 {
		t.Fatalf("AppendAssistantMsg must append one note, got %d messages", len(tx.messages))
	}
	note := tx.messages[0]
	if note.role != "eitri" {
		t.Errorf("appended note role = %q, want eitri", note.role)
	}
	if note.content != "login saved" {
		t.Errorf("appended note content = %q, want the note text", note.content)
	}
	if len(note.events) != 1 || note.events[0].Kind != EventAnswer || note.events[0].Delta != "login saved" {
		t.Errorf("appended note must carry its one-event answer log, got %+v", note.events)
	}
}

func TestRailWidthSeamReportsThroughGeometry(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{Rail: &Rail{}})
	if got := tx.Geometry().RailWidth; got != defaultRailWidth {
		t.Errorf("Geometry().RailWidth with no rail width set = %d, want the default %d", got, defaultRailWidth)
	}

	tx.SetRailWidth(22)
	if got := tx.Geometry().RailWidth; got != 22 {
		t.Errorf("Geometry().RailWidth after SetRailWidth(22) = %d, want 22", got)
	}
}

func TestTranscriptWidthSeamShrinksForTheRail(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{Rail: &Rail{}, RailWidth: defaultRailWidth})
	tx.SetSize(120, 30)

	if bw := tx.Geometry().BandWidth; tx.TranscriptWidth() >= bw {
		t.Errorf("TranscriptWidth %d must sit below the band width %d when the rail is visible", tx.TranscriptWidth(), bw)
	}
	if tw := tx.TranscriptWidth(); tw != 120-2-defaultRailWidth-1 {
		t.Errorf("TranscriptWidth = %d at width 120 rail %d, want %d", tw, defaultRailWidth, 120-2-defaultRailWidth-1)
	}

	tx.SetRailWidth(22)
	if tw := tx.TranscriptWidth(); tw != 120-2-22-1 {
		t.Errorf("TranscriptWidth = %d at rail 22, want %d", tw, 120-2-22-1)
	}
}

func TestRailMaxHeightSeamBoundsTheRailRegion(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{Rail: &Rail{}})
	tx.SetSize(120, 40)

	if got := tx.RailMaxHeight(4); got != 36 {
		t.Errorf("RailMaxHeight(band 4) at height 40 = %d, want 36", got)
	}
	if got := tx.RailMaxHeight(10); got != 30 {
		t.Errorf("RailMaxHeight(band 10) at height 40 = %d, want 30", got)
	}
}

func TestSettleIdleEmberSeamParksTheShimmer(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{})
	if cmd := tx.ArmEmber(false); cmd == nil {
		t.Fatal("precondition: ArmEmber must open the ember window and start its tick")
	}
	tx.TickEmber(false)
	if tx.Status().EmberFrame == 0 {
		t.Fatal("precondition: the armed ember must have advanced off the static mark")
	}

	tx.SettleIdleEmber()
	if s := tx.Status(); s.EmberFrame != 0 || s.EmberRemaining != 0 {
		t.Errorf("SettleIdleEmber = frame %d remaining %d, want the static mark and a closed window", s.EmberFrame, s.EmberRemaining)
	}
}
