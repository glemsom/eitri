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
	collapseAll     bool
	// cotExpanded and toolResultsExpanded are the render defaults flipped by
	// the Settings toggles (issue #432): false means the block collapses to its
	// hint/one-liner by default; true renders the full body by default.
	cotExpanded         bool
	toolResultsExpanded bool
	// focusedBlock is the index into collapsibleBlocks() the per-block expand
	// interaction targets; focusOn gates whether the focus cursor is active at
	// all (a bare Transcript's zero value means no block is focused).
	focusOn      bool
	focusedBlock int
	timeline     []TimelineEvent // live arrival-ordered event log of the in-progress turn
	turnSeq      int             // arrival sequence counter feeding the live timeline
	layout       transcriptLayout
	telemetry    *Telemetry
	dragSel      dragSelect
	width        int
	height       int
	histFollow   bool
	histViewport viewport.Model

	railWidth int

	rail *Rail
}

// viewMode is the transcript's global expansion mode: the default (respects
// the config defaults plus per-block state), the e / ctrl+e expand-all mode,
// or the E collapse-all-to-hints mode.
type viewMode int

const (
	viewDefault viewMode = iota
	viewExpandAll
	viewCollapseAll
)

// viewMode returns the effective expansion mode from the mutually exclusive
// expand-all / collapse-all flags.
func (t Transcript) viewMode() viewMode {
	if t.expandAll {
		return viewExpandAll
	}
	if t.collapseAll {
		return viewCollapseAll
	}
	return viewDefault
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
// turnFlowEvents returns the event sequence that renders as the flat flow of
// the turn owned by user message i, and whether that turn renders as a flow.
// A turn becomes a flow the moment its event log has content: the live
// timeline while it is the running turn, the committed log on its assistant
// message once it completes.
func (t Transcript) turnFlowEvents(i int) ([]TimelineEvent, bool) {
	if i < 0 || i >= len(t.messages) || t.messages[i].role != "you" {
		return nil, false
	}
	// The running turn's events live on the live timeline behind its trailing
	// message (the user prompt before any stream delta, the streaming reply
	// after).
	if t.busy && len(t.timeline) > 0 && i == len(t.messages)-1 {
		return t.timeline, true
	}
	// A finished turn's events live on the first assistant message that
	// follows its prompt.
	for j := i + 1; j < len(t.messages); j++ {
		m := t.messages[j]
		if m.role == "you" {
			return nil, false // the next turn began; this one left no log
		}
		if len(m.events) > 0 {
			return m.events, true
		}
		if t.busy && m.streaming && len(t.timeline) > 0 {
			return t.timeline, true
		}
		return nil, false
	}
	return nil, false
}

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
	now := time.Time{}
	if t.busy {
		now = time.Now()
	}
	anchor := -1 // the user-prompt index owning the current turn's tool entries
	recordToolRows := func(rows []toolRowRange, base int) {
		if toolRows == nil {
			return
		}
		for _, r := range rows {
			*toolRows = append(*toolRows, toolRowRange{start: base + r.start, end: base + r.end, idx: r.idx})
		}
	}
	for i, msg := range t.messages {
		msgStart := nl // content row where this message's block begins
		w := t.transcriptWidth()

		if msg.role == "you" {
			anchor = i
			md, _ := RenderMarkdown(msg.content, w-4, t.configTheme)
			bubble := renderUserPromptCard(t.theme, md, w)
			emit(bubble + "\n")
			if flow, ok := t.turnFlowEvents(i); ok {
				// The turn renders as a flat flow: its tools land at their
				// arrival positions inside the flow (the committed flow at the
				// assistant message, the live flow right here when the run is
				// still in the tool-heavy gap with no assistant message yet).
				if t.busy && i == len(t.messages)-1 {
					base := nl
					block, rows := t.renderEventFlow(flow, anchor, message{}, i, now)
					emit(block)
					recordToolRows(rows, base)
				}
			} else {
				// Legacy note turn (no event log): tool entries anchored to the
				// prompt render directly beneath it, as before the flat flow.
				base := nl
				toolBlock, blockRows := t.log.Render(t.theme, t.viewMode(), !t.toolResultsExpanded, now, w, i, t.busyPulse > 0, t.focusedToolIdx())
				emit(toolBlock)
				recordToolRows(blockRows, base)
			}
		} else if len(msg.events) > 0 {
			// Committed turn: walk its typed event log as one continuous flow.
			base := nl
			block, rows := t.renderEventFlow(msg.events, anchor, msg, i, now)
			emit(block)
			recordToolRows(rows, base)
		} else if t.busy && msg.streaming && len(t.timeline) > 0 {
			// Live turn: walk the in-progress timeline as one continuous flow.
			base := nl
			block, rows := t.renderEventFlow(t.timeline, anchor, msg, i, now)
			emit(block)
			recordToolRows(rows, base)
		} else {
			// Legacy assistant block: a message that carries no event log
			// (system notes, help/skill/login cards, error notes).
			if msg.thinkingRequested && msg.reasoning != "" {
				emit(t.thinkingHeaderFor(msg, i, msg.reasoning))
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
			base := nl
			toolBlock, blockRows := t.log.Render(t.theme, t.viewMode(), !t.toolResultsExpanded, now, w, i, t.busyPulse > 0, t.focusedToolIdx())
			emit(toolBlock)
			recordToolRows(blockRows, base)
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

// renderEventFlow renders one turn's event sequence as one continuous flow:
// reasoning reads as an internal monologue (left-ruled, dimmed), tool calls
// and results render inline at their arrival positions, and the answer lands
// at the tail. It returns the rendered text and the tool-entry row ranges in
// the same shape as toolLog.Render, so the shared row->entry hit-test keeps
// working on the merged stream.
func (t Transcript) renderEventFlow(events []TimelineEvent, anchor int, msg message, msgIdx int, now time.Time) (string, []toolRowRange) {
	var b strings.Builder
	var rows []toolRowRange
	nl := 0
	emit := func(s string) {
		b.WriteString(s)
		nl += strings.Count(s, "\n")
	}
	w := t.transcriptWidth()
	pulse := t.busyPulse > 0

	// The turn's tool entries anchored to its prompt appear in the same order
	// as the tool-start events in the log, so each start consumes the next
	// anchored entry; a start whose entry is missing is synthesized from the
	// event so nothing in the stream silently drops.
	anchored := t.log.anchoredIndices(anchor)
	ti := 0
	var reasoning, answer strings.Builder
	reasoningEmitted := false
	anyStreamedAnswer := false
	emittedAnswerBeforeTail := false
	snapshotAnswerEmitted := false

	flushReasoning := func() {
		// A live (streaming) turn flushes each reasoning fragment at the
		// boundary it precedes and resets, so chain-of-thought that resumes
		// after a tool call renders as its own interleaved block in emission
		// order. A committed turn's reasoning is one authoritative snapshot
		// and renders exactly once, at its first tool boundary (or the tail).
		var txt string
		if msg.streaming {
			txt = reasoning.String() // the live delta fragment accumulated since the last boundary
			reasoning.Reset()
		} else {
			if reasoningEmitted {
				return
			}
			txt = msg.reasoning
			if txt == "" {
				txt = reasoning.String() // the live log is the fallback when the message carries no snapshot
			}
			reasoning.Reset()
			reasoningEmitted = true
		}
		if txt == "" || !msg.thinkingRequested {
			return // nothing to show, or the thinking gate hides a turn that never asked for reasoning
		}
		emit(t.thinkingHeaderFor(msg, msgIdx, txt))
		if !t.thinkingExpandedFor(msg) {
			return // collapsed: the hint is the block
		}
		md, _ := RenderMarkdown(txt, w-2, t.configTheme)
		pane := t.theme.thinkingPaneStyle
		if msg.streaming {
			pane = t.theme.streamingThinkingPaneStyle
		}
		pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
		emit(fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n"))))
	}

	emitTool := func(te toolEntry, idx int) {
		start := nl
		s := renderToolEntry(t.theme, te, t.log.expandedFor(idx, t.viewMode(), !t.toolResultsExpanded), now, w, pulse, t.focusedBlockIs(blockTool, 0, idx))
		emit(s)
		if n := strings.Count(s, "\n"); n > 0 {
			rows = append(rows, toolRowRange{start: start, end: start + n - 1, idx: idx})
		}
	}

	// emitAnswer renders one answer block with the pane chosen from the message's
	// flags (streaming dimmed, stopped pane, error, full accent). final marks the
	// last block of the flow so a stopped turn's marker renders exactly once.
	emitAnswer := func(txt string, final bool) {
		if txt == "" {
			return
		}
		md, _ := RenderMarkdown(txt, w-2, t.configTheme)
		pane := t.theme.agentPaneStyle
		if msg.stopped {
			pane = t.theme.stoppedPaneStyle
		} else if strings.HasPrefix(txt, failurePrefix()) {
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
		if final && msg.stopped {
			emit(t.theme.statusStyle.Render(stoppedMarker()) + "\n")
		}
	}

	// flushAnswer emits the answer text accumulated since the last boundary at
	// this boundary, then resets, so each tool call sees exactly the answer
	// fragments the provider emitted before it, in arrival order. A turn whose
	// answer never streamed as deltas (only the authoritative final snapshot
	// survives, e.g. a non-streaming provider) falls back to that snapshot once;
	// a turn that did stream never also shows the snapshot, so the already-
	// interleaved fragments are not duplicated.
	flushAnswer := func(final bool) {
		txt := answer.String()
		answer.Reset()
		switch {
		case txt != "":
			// an interleaved answer fragment sits before this boundary. A turn
			// that finalized with no answer yet split across a tool boundary
			// (nothing rendered mid-flow) prefers the authoritative snapshot,
			// which may be fuller than the last un-emitted stream prefix.
			if final && !emittedAnswerBeforeTail && msg.content != "" {
				txt = msg.content
			}
		case final && !anyStreamedAnswer && !snapshotAnswerEmitted && !msg.streaming && msg.content != "":
			snapshotAnswerEmitted = true
			txt = msg.content
		default:
			return // nothing to show at this boundary
		}
		emitAnswer(txt, final)
		if !final && !emittedAnswerBeforeTail {
			emittedAnswerBeforeTail = true
		}
	}

	for _, ev := range events {
		switch ev.Kind {
		case EventReasoning:
			reasoning.WriteString(ev.Delta)
		case EventToolStart:
			flushReasoning()
			flushAnswer(false)
			te := toolEntry{name: ev.Start.Name, args: ev.Start.Args, startedAt: time.Now()}
			idx := -1
			if ti < len(anchored) {
				idx = anchored[ti] // the log entry for this start, in arrival order
				te = t.log.entries[idx]
			}
			ti++
			emitTool(te, idx)
		case EventToolResult:
			flushReasoning() // the entry sent its head when it started; results surface through it
			flushAnswer(false)
		case EventAnswer:
			anyStreamedAnswer = true
			answer.WriteString(ev.Delta)
		}
	}
	flushReasoning()
	flushAnswer(true)

	return b.String(), rows
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

// toolEntryAtLine returns the tool entry whose rendered rows include the given content line, and whether that entry currently renders collapsed (a click on a collapsed head toggles it open; on an open entry it toggles closed).
func (t *Transcript) toolEntryAtLine(line int) (idx int, collapsed bool, ok bool) {
	t.ensureLayout()
	toolIdx, _, ok := t.log.AtLine(line, t.layout.rows)
	if !ok {
		return 0, false, false
	}
	return toolIdx, !t.toolExpandedFor(toolIdx), true
}

// toggleToolEntry flips one tool entry's expansion state (mouse click
// click-to-expand): an expanded entry force-collapses (beating an expanded
// default or the expand-all mode), a collapsed one force-expands.
func (t *Transcript) toggleToolEntry(idx int) {
	if t.toolExpandedFor(idx) {
		t.log.ForceCollapse(idx)
	} else {
		t.log.Expand(idx)
	}
	t.layout.dirty = true // an entry expanded/collapsed changes its rendered rows
}

// toggleCollapse flips one entry's per-entry collapse-override: it keeps a single entry collapsed even while the global expanded-view mode is ON, and flips back to let the mode show it.
func (t *Transcript) toggleCollapse(idx int) {
	t.log.ToggleCollapse(idx)
	t.layout.dirty = true
}

// toolExpandedFor reports whether tool entry idx renders expanded under the
// current mode and collapsed-by-default flag.
func (t Transcript) toolExpandedFor(idx int) bool {
	return t.log.expandedFor(idx, t.viewMode(), !t.toolResultsExpanded)
}

// appendMsg appends a finished assistant entry to the transcript and marks the shared message layout dirty in the same step, so the appended block re-wraps at the current transcript width on the next frame instead of rendering at a stale width.
func (t *Transcript) appendMsg(content string) {
	t.messages = append(t.messages, message{role: "eitri", content: content})
	t.layout.dirty = true
}

// apply folds one tool-call observation into the transcript's log: tool updates route through the Transcript so they land in the same log renderPane reads. The matching event is also recorded on the per-turn timeline so the log captures tool starts/results in arrival order alongside the stream deltas.
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

// toggleExpandAll flips the persistent Ctrl+E expanded-view mode: Ctrl+E on the Model routes here, and it marks the shared layout dirty because showing or hiding all tool results re-wraps the log. Turning the mode on clears the collapse-all mode; turning it off returns to the defaults (issue #432).
func (t *Transcript) toggleExpandAll() bool {
	t.setExpandAll(!t.expandAll)
	return t.expandAll
}

// setExpandAll enters or leaves the e / Ctrl+E expand-all mode directly.
// Entering clears the per-block collapse forces so every block expands;
// leaving returns to the default mode with per-block state intact.
func (t *Transcript) setExpandAll(v bool) {
	t.expandAll = v
	if v {
		t.collapseAll = false
		t.clearCollapseForces()
	}
	t.layout.dirty = true // showing/hiding blocks re-wraps the transcript
}

// setCollapseAll enters or leaves the E collapse-all-to-hints mode: every
// collapsible block collapses to its hint/one-liner regardless of the defaults.
// Entering clears the per-block expand forces so the collapse is total; a
// fresh per-block toggle (Enter on the focused block) can still re-expand one.
func (t *Transcript) setCollapseAll(v bool) {
	t.collapseAll = v
	if v {
		t.expandAll = false
		t.clearExpandForces()
	}
	t.layout.dirty = true // collapsing/hiding blocks re-wraps the transcript
}

// clearCollapseForces drops every per-block force-collapse flag so the
// expand-all mode can show all blocks.
func (t *Transcript) clearCollapseForces() {
	for i := range t.messages {
		t.messages[i].thinkingCollapsed = false
	}
	for i := range t.log.entries {
		t.log.entries[i].collapsedOverride = false
	}
}

// clearExpandForces drops every per-block force-expand flag so the
// collapse-all mode can hide all block bodies.
func (t *Transcript) clearExpandForces() {
	for i := range t.messages {
		t.messages[i].thinkingExpanded = false
	}
	for i := range t.log.entries {
		t.log.entries[i].expanded = false
	}
}

// blockKind discriminates a collapsible block: a turn's reasoning header or a
// tool entry, the two shapes the per-block expand interaction targets.
type blockKind int

const (
	blockReasoning blockKind = iota
	blockTool
)

// collapsibleBlock identifies one collapsible block for the block focus: kind
// selects the shape, msgIdx the owning message for a reasoning block, toolIdx
// the log index for a tool block.
type collapsibleBlock struct {
	kind    blockKind
	msgIdx  int
	toolIdx int
}

// collapsibleBlocks returns the transcript's collapsible blocks in render
// order: each turn's reasoning header at its prompt position, then that turn's
// tool entries in anchored order — the traversal Tab cycles the block focus
// through.
func (t Transcript) collapsibleBlocks() []collapsibleBlock {
	var blocks []collapsibleBlock
	for i, msg := range t.messages {
		if msg.role != "you" {
			continue
		}
		for j := i + 1; j < len(t.messages); j++ {
			m := t.messages[j]
			if m.role == "you" {
				break // the next turn began without a reasoning block here
			}
			if m.thinkingRequested && (m.reasoning != "" || hasReasoningEvents(m.events)) {
				blocks = append(blocks, collapsibleBlock{kind: blockReasoning, msgIdx: j})
				break
			}
		}
		for _, ti := range t.log.anchoredIndices(i) {
			blocks = append(blocks, collapsibleBlock{kind: blockTool, toolIdx: ti})
		}
	}
	return blocks
}

// hasReasoningEvents reports whether a message's event log derives any
// reasoning text — the committed-turn case where the snapshot lives only in
// the log until the turn finalizes.
func hasReasoningEvents(events []TimelineEvent) bool {
	for _, ev := range events {
		if ev.Kind == EventReasoning && ev.Delta != "" {
			return true
		}
	}
	return false
}

// focusNext advances the block focus to the next collapsible block, wrapping;
// the first Tab activates the focus cursor on the first block; a transcript
// with no collapsible blocks stays unfocused.
func (t *Transcript) focusNext() {
	n := len(t.collapsibleBlocks())
	if n == 0 {
		t.focusOn = false
		t.focusedBlock = 0
		return
	}
	if !t.focusOn {
		t.focusOn = true
		t.focusedBlock = 0
		return
	}
	t.focusedBlock = (t.focusedBlock + 1) % n
}

// focused returns the block currently under the focus cursor.
func (t Transcript) focused() (collapsibleBlock, bool) {
	if !t.focusOn || t.focusedBlock < 0 {
		return collapsibleBlock{}, false
	}
	blocks := t.collapsibleBlocks()
	if t.focusedBlock >= len(blocks) {
		return collapsibleBlock{}, false
	}
	return blocks[t.focusedBlock], true
}

// toggleFocused flips the focused block's expansion (Enter on the model), the
// per-block half of the collapse-by-default interaction.
func (t *Transcript) toggleFocused() {
	blk, ok := t.focused()
	if !ok {
		return
	}
	switch blk.kind {
	case blockReasoning:
		t.toggleThinking(blk.msgIdx)
	case blockTool:
		t.toggleToolEntry(blk.toolIdx)
	}
}

// focusedBlockIs reports whether the block identified by kind/msgIdx/toolIdx
// is the one under the focus cursor, so the renderer can mark it.
func (t Transcript) focusedBlockIs(kind blockKind, msgIdx, toolIdx int) bool {
	blk, ok := t.focused()
	return ok && blk.kind == kind && blk.msgIdx == msgIdx && blk.toolIdx == toolIdx
}

// focusedToolIdx returns the log index of the focused block when it is a tool
// entry, else -1, so the legacy tool renderer marks the same block the flat
// flow does.
func (t Transcript) focusedToolIdx() int {
	blk, ok := t.focused()
	if !ok || blk.kind != blockTool {
		return -1
	}
	return blk.toolIdx
}

// thinkingExpandedFor returns whether msg's reasoning block renders expanded: a
// per-turn thinkingCollapsed override always wins, a per-turn thinkingExpanded
// flag beats the global modes, the expand-all mode expands, the collapse-all
// mode collapses, and otherwise the block follows the CoT-collapsed-by-default
// flag (issue #432). A live streamed block stays expanded unless force-collapsed.
func (t Transcript) thinkingExpandedFor(msg message) bool {
	if msg.thinkingCollapsed {
		return false
	}
	if msg.streaming && msg.reasoning != "" {
		return true
	}
	if msg.thinkingExpanded {
		return true
	}
	switch t.viewMode() {
	case viewExpandAll:
		return true
	case viewCollapseAll:
		return false
	}
	return t.cotExpanded
}

// thinkingHeaderFor renders a turn's reasoning-header line, prefixing it with the focus marker when the block is the one under the focus cursor.
func (t Transcript) thinkingHeaderFor(msg message, msgIdx int, txt string) string {
	h := thinkingHeader(t.theme, txt, t.reasoningEffort)
	if t.focusedBlockIs(blockReasoning, msgIdx, 0) {
		h = t.theme.focusStyle.Render(focusMarker()+" ") + h
	}
	return h
}

// toggleThinking flips one turn's reasoning-block expansion (Enter on the
// focused block, previously tab), kept independent of the global modes: it
// sets the force that opposes how the block currently renders, so a collapsed
// block expands and an expanded block collapses.
func (t *Transcript) toggleThinking(i int) {
	if i < 0 || i >= len(t.messages) {
		return
	}
	msg := &t.messages[i]
	if msg.streaming && msg.reasoning != "" {
		msg.thinkingCollapsed = !msg.thinkingCollapsed
	} else if t.thinkingExpandedFor(*msg) {
		msg.thinkingCollapsed = true // expanded: flip to a forced collapse
		msg.thinkingExpanded = false
	} else {
		msg.thinkingExpanded = true // collapsed: flip to a forced expand
		msg.thinkingCollapsed = false
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
