package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Transcript is the single owner of the transcript region: the layout/scroll/follow/render concerns that used to live in the TUI Model god-object, and the only home of the transcript state.
type Transcript struct {
	theme           Theme
	messages        []message
	busy            bool
	spinner         int
	busyPulse       int
	reasoningEffort string
	configTheme     string
	workspacePath   string
	log             toolLog
	expandAll       bool
	timeline        []TimelineEvent // live arrival-ordered event log of the in-progress turn
	turnSeq         int             // arrival sequence counter feeding the live timeline
	layout          transcriptLayout
	telemetry       *Telemetry
	dragSel         dragSelect
	width           int
	height          int
	histFollow      bool
	histViewport    viewport.Model

	railWidth int

	rail *Rail
}

// railVisible reports whether the right context rail should render now.
func (t Transcript) railVisible() bool { return t.rail != nil }

// transcriptWidth returns the column width the transcript pane should use for wrapping: the terminal width (or a sane default before a resize) minus the 2-col gutter, and minus the rail + separator when the rail is visible, so the history re-wraps to leave the rail room .
func (t Transcript) transcriptWidth() int {
	base := t.width
	if base == 0 {
		base = presizeTerminalWidth
	}
	w := base - 2
	if t.railVisible() {
		w -= t.railWidthOrDefault() + 1
		if w < 20 {
			w = 20
		}
	}
	return w
}

// bandWidth returns the column width the bottom band renders at: the terminal width (or a sane non-composer default before the first resize lands) minus the 2-col gutter.
func (t Transcript) bandWidth() int {
	base := t.width
	if base == 0 {
		base = presizeTerminalWidth // no resize yet; use a sane full-width start
	}
	return base - 2
}

// scrollRegionHeight returns the height in rows of the history scroll region — the rows left over by the fixed bottom band.
func (t Transcript) scrollRegionHeight(bandHeight int) int {
	if t.height <= 0 {
		return -1
	}
	vh := t.height - bandHeight
	if vh < 0 {
		return 0
	}
	return vh
}

// railClampHeight returns the maximum number of rows the right context rail may occupy so it matches the history region's visible height: both panes clamp to the rows left over by the fixed bottom band, so the two form one coherent row.
func (t Transcript) railClampHeight(bandHeight int) int {
	return t.scrollRegionHeight(bandHeight)
}

// surfaceWithRail merges the rendered right rail into a full-width pane so the bottom band stays edge-to-edge: the band is a bottom-anchored region spanning the whole terminal width, so the rail cannot sit to its right the way it sits beside the transcript.
func (t Transcript) surfaceWithRail(pane, rail string, bandHeight int) string {
	vh := t.railClampHeight(bandHeight)
	if vh <= 0 {
		if t.height <= 0 {
			return lipgloss.JoinHorizontal(lipgloss.Top, pane, rail)
		}
		return pane
	}
	rows := strings.Split(pane, "\n")
	railRows := strings.Split(rail, "\n")
	for i := 0; i < vh && i < len(railRows); i++ {
		if i >= len(rows) {
			break
		}
		rows[i] = rows[i] + railRows[i]
	}
	return strings.Join(rows, "\n")
}

// viewWithRail composes the final surface content for a rail-visible pane: it renders the wired rail through styledRail at the rail's clamp height and floats it above the full-width band via surfaceWithRail.
func (t Transcript) viewWithRail(pane string, bandHeight int) string {
	if !t.railVisible() {
		return pane
	}
	rw := t.railWidthOrDefault()
	right := styledRail(t.rail.render(t.telemetry, t.theme, rw), t.railClampHeight(bandHeight), rw)
	return t.surfaceWithRail(pane, right, bandHeight)
}

// railWidthOrDefault returns the rail width in effect: the mutable field when set, else the default.
func (t Transcript) railWidthOrDefault() int {
	if t.railWidth == 0 {
		return defaultRailWidth
	}
	return t.railWidth
}

// setRailWidth stores the rail width and marks the shared layout cache dirty, so the next render pass re-wraps the history at the new transcript width and re-records the row layout . scroll/follow survive because the persisted viewport keeps its position; it is only re-sized, never re-created.
func (t *Transcript) setRailWidth(w int) {
	t.railWidth = w
	t.layout.dirty = true
}

// renderPane renders the transcript + composer surface into the left pane.
func (t *Transcript) renderPane(band string) string {
	bandStr := band

	// The scroll region renders through the native bubbletea/viewport component (T1 alt-screen pivot, ), which owns the history clip + follow.
	var hist strings.Builder
	t.renderHistory(&hist, nil, nil)
	histRegion := t.renderHistoryViewport(hist.String(), lineCount(bandStr))
	if histRegion != "" && !strings.HasSuffix(histRegion, "\n") {
		histRegion += "\n"
	}
	return histRegion + bandStr
}

// renderHistory renders the scroll region: the agent history that the user reads and scrolls.
func (t Transcript) renderHistory(b *strings.Builder, toolRows *[]toolRowRange, msgRows *[]msgRowRange) {
	if toolRows != nil {
		*toolRows = (*toolRows)[:0]
	}
	if msgRows != nil {
		*msgRows = (*msgRows)[:0]
	}
	nl := 0
	emit := func(s string) {
		b.WriteString(s)
		nl += strings.Count(s, "\n")
	}
	if t.workspacePath != "" {
		emit(t.theme.statusStyle.Render("workspace: " + t.workspacePath))
		emit("\n")
	}
	if len(t.messages) == 0 && !t.busy {
		emit(idleWelcome(t.theme))
	}
	for i, msg := range t.messages {
		msgStart := nl // content row where this message's block begins
		w := t.transcriptWidth()
		if msg.role != "you" && msg.thinkingRequested && msg.reasoning != "" {
			emit(thinkingHeader(t.theme, msg.reasoning, t.reasoningEffort))
			if t.thinkingExpandedFor(msg) {
				md, _ := RenderMarkdown(msg.reasoning, w-2, t.configTheme)
				pane := t.theme.thinkingPaneStyle
				if msg.streaming {
					pane = t.theme.streamingThinkingPaneStyle
				}
				pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
				emit(fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n"))))
			}
		}
		if msg.role == "you" {
			md, _ := RenderMarkdown(msg.content, w-4, t.configTheme)
			bubble := renderUserPromptCard(t.theme, md, w)
			emit(bubble + "\n")
		} else {
			md, _ := RenderMarkdown(msg.content, w-2, t.configTheme)
			pane := t.theme.agentPaneStyle
			if msg.stopped {
				pane = t.theme.stoppedPaneStyle
			} else if strings.HasPrefix(msg.content, failurePrefix()) {
				if msg.streaming {
					pane = t.theme.streamingErrorPaneStyle
				} else {
					pane = t.theme.errorPaneStyle
				}
			} else if msg.streaming {
				pane = t.theme.streamingPaneStyle
			}
			pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
			emit(fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n"))))
			if msg.stopped {
				emit(t.theme.statusStyle.Render(stoppedMarker()) + "\n")
			}
		}
		now := time.Time{}
		if t.busy {
			now = time.Now()
		}
		blockStart := nl
		toolBlock, blockRows := t.log.Render(t.theme, t.expandAll, now, w, i, t.busyPulse > 0)
		emit(toolBlock)
		if toolRows != nil {
			for _, r := range blockRows {
				*toolRows = append(*toolRows, toolRowRange{start: blockStart + r.start, end: blockStart + r.end, idx: r.idx})
			}
		}
		if msgRows != nil {
			*msgRows = append(*msgRows, msgRowRange{start: msgStart, end: nl - 1, idx: i})
		}
	}
	if t.busy && t.telemetry == nil {
		if t.busyPulse > 0 {
			emit(t.theme.bandStatusStyle.Render(busyLine(t.spinner, t.phase())))
		} else {
			emit(t.theme.statusStyle.Render(busyLine(t.spinner, t.phase())))
		}
		emit("\n")
	}
}

// renderHistoryViewport returns the Height-clamped scroll region: the rendered history content limited to the rows the fixed bottom band (the only non-reserved region) does not occupy.
func (t *Transcript) renderHistoryViewport(content string, reserved int) string {
	vh := t.scrollRegionHeight(reserved)
	if vh < 0 {
		return content
	}
	if vh == 0 {
		return ""
	}
	t.histViewport.SetWidth(t.transcriptWidth())
	t.histViewport.SetHeight(vh)
	if t.dragSel.active {
		content = t.highlightSelection(content)
	}
	t.histViewport.SetContent(content)
	if t.histFollow {
		t.histViewport.GotoBottom()
	}
	return t.histViewport.View()
}

// navigateHistory applies a T2 keyboard scroll command to the persisted history viewport owned by the Transcript: PgUp/Home move toward the older output and break the follow position; PgDn/End move toward the newest and re-engage follow when they reach the bottom.
func (t *Transcript) navigateHistory(key string) bool {
	switch key {
	case "pgup":
		if t.histViewport.AtTop() {
			return t.histFollow // already at the oldest output; nothing to do
		}
		t.histViewport.PageUp()
		t.histFollow = false // scrolling up breaks follow
	case "home":
		if t.histViewport.AtTop() {
			return t.histFollow
		}
		t.histViewport.GotoTop()
		t.histFollow = false
	case "pgdown":
		t.histViewport.PageDown()
		if t.histViewport.AtBottom() {
			t.histFollow = true // paging to the newest re-engages follow
		}
	case "end":
		t.histViewport.GotoBottom()
		t.histFollow = true
	}
	return t.histFollow
}

// navigateMouse applies a T2 mouse-wheel scroll to the persisted history viewport owned by the Transcript: wheel up scrolls toward older output and breaks follow; wheel down scrolls toward the newest and re-engages follow once it reaches the bottom.
func (t *Transcript) navigateMouse(msg tea.MouseWheelMsg) bool {
	if !t.inScrollRegion(msg.Y) {
		return t.histFollow
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		if t.histViewport.AtTop() {
			return t.histFollow
		}
		t.histViewport.ScrollUp(3)
		t.histFollow = false
	case tea.MouseWheelDown:
		t.histViewport.ScrollDown(3)
		if t.histViewport.AtBottom() {
			t.histFollow = true
		}
	}
	return t.histFollow
}

// inScrollRegion answers whether a screen row lies inside the history scroll region, read from the single region source — the persisted viewport's height (sized by renderHistoryViewport via scrollRegionHeight).
func (t *Transcript) inScrollRegion(y int) bool {
	vp := t.histViewport
	if vp.Height() <= 0 {
		return false
	}
	return y >= 0 && y < vp.Height()
}

// contentLineAtScreenRow is the scroll-region hit-test seam: it answers "is a screen row y inside the history scroll region, and which content line does it map to".
func (t *Transcript) contentLineAtScreenRow(y int) (line int, ok bool) {
	if !t.inScrollRegion(y) {
		return 0, false
	}
	v := t.histViewport
	line = v.YOffset() + y
	if line < 0 || line >= len(t.plainLines()) {
		return 0, false
	}
	return line, true
}

// highlightSelection wraps the cells covered by an in-progress drag in reverse video across the full rendered history content; the persisted viewport clips it to the visible window .
func (t Transcript) highlightSelection(content string) string {
	if !t.dragSel.active {
		return content
	}
	d := t.dragSel
	startLine, startCol, endLine, endCol := d.selRange()
	lines := strings.Split(content, "\n")
	if startLine >= len(lines) {
		return content
	}
	for i := startLine; i <= endLine && i < len(lines); i++ {
		from, to := startCol, endCol
		if i > startLine {
			from = 0
		}
		if i < endLine {
			to = len([]rune(ansiStrip(lines[i]))) - 1
		}
		lines[i] = highlightRange(lines[i], from, to)
	}
	return strings.Join(lines, "\n")
}

// ensureLayout rebuilds the transcript layout cache when it is dirty, at most once per transcript change.
func (t *Transcript) ensureLayout() {
	if t.layout.dirty {
		t.recordLayout()
	}
}

// recordLayout performs the one batched layout pass behind the persistent cache : it renders the history into a scratch builder, captures the toolRows and msgRows out-params, and derives the ANSI-stripped plain rows from the same builder, storing both indexes and clearing dirty.
func (t *Transcript) recordLayout() {
	l := &t.layout
	var hist strings.Builder
	l.rows = l.rows[:0]
	l.msgs = l.msgs[:0]
	t.renderHistory(&hist, &l.rows, &l.msgs)
	lines := strings.Split(hist.String(), "\n")
	l.plain = l.plain[:0]
	for _, line := range lines {
		l.plain = append(l.plain, ansiStrip(line))
	}
	l.dirty = false
	l.builds++
}

// toolEntryAtLine returns the tool entry whose rendered rows include the given content line, and whether that entry currently renders collapsed under the Ctrl+E expanded-view mode — a click on a collapsed head toggles it open; on an open entry it toggles closed.
func (t *Transcript) toolEntryAtLine(line int) (idx int, collapsed bool, ok bool) {
	t.ensureLayout()
	toolIdx, _, ok := t.log.AtLine(line, t.layout.rows)
	if !ok {
		return 0, false, false
	}
	return toolIdx, !t.log.expandedFor(toolIdx, t.expandAll), true
}

// toggleToolEntry flips one tool entry's expansion state (mouse click click-to-expand, ), kept independent of the Ctrl+E expanded-view mode .
func (t *Transcript) toggleToolEntry(idx int) {
	if t.expandAll {
		t.toggleCollapse(idx)
	} else {
		t.log.Toggle(idx)
		t.layout.dirty = true // an entry expanded/collapsed changes its rendered rows
	}
}

// toggleCollapse flips one entry's per-entry collapse-override: it keeps a single entry collapsed even while the global expanded-view mode is ON, and flips back to let the mode show it.
func (t *Transcript) toggleCollapse(idx int) {
	t.log.ToggleCollapse(idx)
	t.layout.dirty = true
}

// appendMsg appends a finished assistant entry to the transcript and marks the shared message layout dirty in the same step, so the appended block re-wraps at the current transcript width on the next frame instead of rendering at a stale width.
func (t *Transcript) appendMsg(content string) {
	t.messages = append(t.messages, message{role: "eitri", content: content})
	t.layout.dirty = true
}

// apply folds one tool-call observation into the transcript's log ( AC1/AC2): tool updates now route through the Transcript so they land in the same log renderPane reads. The matching event is also recorded on the per-turn timeline so the log captures tool starts/results in arrival order alongside the stream deltas.
func (t *Transcript) apply(u ToolUpdate) {
	t.log.Apply(u)
	if kind, ok := toolEventKind(u); ok {
		t.recordTurnEvent(TimelineEvent{Kind: kind, Start: u.Start, Result: u.Result})
	}
	t.layout.dirty = true // an entry changed the tool log's rendered rows
}

// recordLive appends one event to the live per-turn log in arrival order,
// stamping it with the turn's next sequence number.
func (t *Transcript) recordLive(ev TimelineEvent) {
	ev.Seq = t.turnSeq
	t.turnSeq++
	t.timeline = append(t.timeline, ev)
}

// recordTurnEvent logs one event on the per-turn event timeline: while a turn runs it lands on the live log the TurnDispatch commits at turn completion; after the turn it attaches to the most recent assistant message so trailing tool results still appear in that turn's log. Seq continues wherever the last log left off, so post-turn appends stay arrival-ordered with the turn's own events.
func (t *Transcript) recordTurnEvent(ev TimelineEvent) {
	if t.busy {
		t.recordLive(ev)
		return
	}
	for i := len(t.messages) - 1; i >= 0; i-- {
		if t.messages[i].role == "eitri" {
			ev.Seq = len(t.messages[i].events)
			t.messages[i].events = append(t.messages[i].events, ev)
			return
		}
	}
}

// syncStreamSnapshots re-derives the streaming message's content/reasoning text from the turn's event log: the log is the single arrival-ordered source of text, and the snapshots keep copy-to-clipboard, telemetry, and the gateway export reading identical content without touching their seams.
func (t *Transcript) syncStreamSnapshots(i int) {
	m := &t.messages[i]
	m.content, m.reasoning = deriveSnapshots(t.timeline)
}

// toggleExpandAll flips the persistent Ctrl+E expanded-view mode: Ctrl+E on the Model routes here, and it marks the shared layout dirty because showing or hiding all tool results re-wraps the log.
func (t *Transcript) toggleExpandAll() bool {
	t.expandAll = !t.expandAll
	t.layout.dirty = true // showing/hiding all tool results re-wraps the log
	return t.expandAll
}

// thinkingExpandedFor returns whether msg's reasoning block renders expanded given the persistent Ctrl+E expanded-view mode: a per-turn thinkingCollapsed override (tab while the mode is ON) forces this single block collapsed, and otherwise the block reflects the global mode (issue #274).
func (t Transcript) thinkingExpandedFor(msg message) bool {
	if msg.thinkingCollapsed {
		return false
	}
	if msg.streaming && msg.reasoning != "" {
		return true
	}
	return t.expandAll || msg.thinkingExpanded
}

// toggleThinking flips one turn's reasoning-block expansion (tab in the composer, ), kept independent of the Ctrl+E expanded-view mode .
func (t *Transcript) toggleThinking(i int) {
	if i < 0 || i >= len(t.messages) {
		return
	}
	msg := &t.messages[i]
	if msg.streaming && msg.reasoning != "" {
		msg.thinkingCollapsed = !msg.thinkingCollapsed
	} else if t.expandAll {
		msg.thinkingCollapsed = !msg.thinkingCollapsed
	} else {
		msg.thinkingExpanded = !msg.thinkingExpanded
	}
	t.layout.dirty = true // a thinking block expanded/collapsed changes rows
}

// plainLines returns the history scroll content as plain text per rendered row (ANSI stripped) — the coordinate space drag selection maps into.
func (t *Transcript) plainLines() []string {
	t.ensureLayout()
	return t.layout.plain
}

// messageAtLine returns the message whose rendered rows include the given content line, via the persistent row->message index .
func (t *Transcript) messageAtLine(line int) (idx int, ok bool) {
	t.ensureLayout()
	for _, r := range t.layout.msgs {
		if line >= r.start && line <= r.end {
			return r.idx, true
		}
	}
	return 0, false
}
