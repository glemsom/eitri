package tui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
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
	// focus owns the block-focus cursor (the per-block Tab/Enter interaction):
	// whether the cursor is active and which collapsible block it points at.
	// A bare Transcript's zero value means no block is focused.
	focus collapseFocus
	// live is the TurnSession owning the in-progress turn, wired at Begin so
	// render paths can read the live event log; a bare Transcript has none.
	live         *TurnSession
	layout       transcriptLayout
	telemetry    *Telemetry
	weaver       selectionWeaver
	width        int
	height       int
	histFollow   bool
	histViewport viewport.Model

	railWidth int

	rail *Rail
}

type toolRowRange struct {
	start, end, idx int
}

// msgRowRange maps a rendered history row span to the message that owns it, so the transcript exposes a row->message index alongside the row->tool-entry index. start/end are content-line indexes in the viewport's split space (the same space mouseToContent maps into); idx indexes the Transcript-owned messages.
type msgRowRange struct {
	start, end, idx int
}

// transcriptLayout is the persistent layout cache for the history region : one batched renderHistory pass captures the row->tool-entry mapping (rows), the row->message mapping (msgs), both in content-line coordinates, and the ANSI-stripped history rows (plain, the drag-select copy space) so the mouse hit-test reads the recorded index instead of re-deriving layout on every pointer event. dirty is true when a transcript-affecting change makes the cached index stale; the lazy hit-test rebuilds exactly once per invalidate.
type transcriptLayout struct {
	rows                []toolRowRange // row->tool-entry index in content-line coordinates
	msgs                []msgRowRange  // row->message index in content-line coordinates
	rendered            string         // ANSI-rendered history rows, cached for scroll/mouse-only frames
	renderedLines       []string       // rendered split rows, reused by visible-only selection highlight
	plain               []string       // ANSI-stripped history rows (the drag-select space)
	viewportSyncedBuild int            // layout.builds value last installed in the viewport
	dirty               bool
	builds              int
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

// hasContent reports whether any turn material (committed messages or a live timeline) exists, i.e. the transcript is no longer showing the empty welcome state.
func (t Transcript) hasContent() bool {
	return len(t.messages) > 0 || t.LiveTimeline() != nil || t.busy
}

// Reset clears all turn material so the transcript returns to the empty
// welcome state — the `/new` surface (issue #613). It drops committed messages,
// the tool log, the live session, and the focused block; configuration, the
// prompt-history ring, and the settings overlay all live outside the
// transcript and are untouched.
func (t *Transcript) Reset() {
	t.messages = nil
	t.log = toolLog{}
	t.live = nil
	t.focus = collapseFocus{}
	t.layout = transcriptLayout{dirty: true}
	t.histFollow = true
	t.busy = false
}

// LiveTimeline returns the in-progress turn's event log through the wired
// session — read-only; the session alone writes it. A transcript with no wired
// session reads empty.
func (t Transcript) LiveTimeline() []TimelineEvent {
	if t.live == nil {
		return nil
	}
	return t.live.LiveTimeline()
}

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
	return scrollRegionHeight(t.height, bandHeight)
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

// SetSize stores the terminal dimensions and marks the shared layout cache dirty, so the next render pass re-wraps the history at the new transcript width; the Model's WindowSizeMsg handler routes here instead of writing the dirty flag by hand.
func (t *Transcript) SetSize(width, height int) {
	t.width = width
	t.height = height
	t.layout.dirty = true
}

// applySettings applies the Settings-save outcomes that affect the transcript — theme, and the expand/collapse render defaults (issue #432) — and marks the layout cache dirty in the same step, since the flip can re-wrap the transcript.
func (t *Transcript) applySettings(cfg config.Config) {
	t.theme = themeFor(cfg.Theme)
	t.configTheme = cfg.Theme
	t.cotExpanded = !cfg.CoTCollapsedByDefault
	t.toolResultsExpanded = !cfg.ToolResultsCollapsedByDefault
	t.layout.dirty = true
}

// appendUserMsg appends a user message (a slash/skill/login activation prompt) to the transcript and marks the shared layout cache dirty in the same step, so callers never invalidate by hand around the append.
func (t *Transcript) appendUserMsg(content string) {
	t.messages = append(t.messages, message{role: "you", content: content})
	t.layout.dirty = true
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
	content := ""
	if t.busy {
		var hist strings.Builder
		t.renderHistory(&hist, nil, nil)
		content = hist.String()
	} else {
		t.ensureLayout()
		content = t.layout.rendered
	}
	histRegion := t.renderHistoryViewport(content, lineCount(bandStr))
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
	// after). A turn is flow-worthy the moment it starts: an empty timeline in
	// the pre-stream gap synthesizes a minimal empty event log so the prompt
	// renders through the FlowRenderer like every other turn.
	if t.isLiveTurnPrompt(i) {
		return t.LiveTimeline(), true
	}
	// A finished turn's events live on the first message that follows its
	// prompt: a later prompt means this one left no log, an assistant message
	// with events carries them, and anything else terminates the search.
	if i+1 < len(t.messages) {
		m := t.messages[i+1]
		switch {
		case m.role == "you":
			return nil, false // the next turn began; this one left no log
		case len(m.events) > 0:
			return m.events, true
		case t.busy && m.streaming && len(t.LiveTimeline()) > 0:
			return t.LiveTimeline(), true
		}
	}
	return nil, false
}

// isLiveTurnPrompt reports whether user message i is the prompt of the turn
// currently running: busy, and the last message in the transcript.
func (t Transcript) isLiveTurnPrompt(i int) bool {
	return t.busy && i == len(t.messages)-1
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
	// emitFlow renders one turn's event sequence through the single FlowRenderer
	// emitter and records its tool rows into the shared index. Every flow-worthy
	// turn funnels through this one emission path — a committed turn (its
	// finalized event log), a live turn (the in-progress timeline), and the
	// tool-heavy gap before any assistant message — so no parallel in-loop flow
	// code can drift from RenderFlow.
	emitFlow := func(events []TimelineEvent, anchor, msgIdx int, msg message) {
		base := nl
		block, rows := t.renderEventFlow(events, anchor, msg, msgIdx, now)
		emit(block)
		recordToolRows(rows, base)
	}
	for i, msg := range t.messages {
		msgStart := nl // content row where this message's block begins
		w := t.transcriptWidth()

		if msg.role == "you" {
			anchor = i
			md, _ := RenderMarkdown(msg.content, w-4, t.configTheme)
			bubble := renderUserPromptCard(t.theme, md, w)
			emit(bubble + "\n")
			if t.isLiveTurnPrompt(i) {
				// The running turn renders as a flat flow from the moment the
				// prompt lands: its tools appear at their arrival positions once
				// events arrive; the pre-stream gap renders the synthesized
				// minimal (empty) log. There is no fallback render path.
				flow, _ := t.turnFlowEvents(i)
				emitFlow(flow, anchor, i, message{})
			}
		} else if len(msg.events) > 0 {
			// Committed turn: walk its typed event log as one continuous flow
			// through the shared FlowRenderer emitter.
			emitFlow(msg.events, anchor, i, msg)
		} else if t.busy && msg.streaming && len(t.LiveTimeline()) > 0 {
			// Live turn: walk the in-progress timeline as one continuous flow
			// through the shared FlowRenderer emitter.
			emitFlow(t.LiveTimeline(), anchor, i, msg)
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

// renderEventFlow renders one turn's event sequence as one continuous flow
// through the FlowRenderer seam: it assembles the flowInput from the
// Transcript's state and delegates the now-shared fold-and-render to RenderFlow.
// It returns the rendered text and the tool-entry row ranges in the same shape
// as toolLog.Render, so the shared row->entry hit-test keeps working on the
// merged stream.
func (t Transcript) renderEventFlow(events []TimelineEvent, anchor int, msg message, msgIdx int, now time.Time) (string, []toolRowRange) {
	tools := make([]flowTool, 0)
	for _, idx := range t.log.anchoredIndices(anchor) {
		tools = append(tools, flowTool{
			entry:    t.log.entries[idx],
			logIdx:   idx,
			expanded: t.log.expandedFor(idx, t.expansionConfig()),
		})
	}
	return RenderFlow(flowInput{
		Events:      events,
		Msg:         msg,
		MsgIdx:      msgIdx,
		Theme:       t.theme,
		ConfigTheme: t.configTheme,
		Width:       t.transcriptWidth(),
		Pulse:       t.busyPulse > 0,
		Effort:      t.reasoningEffort,
		Cfg:         t.expansionConfig(),
		Now:         now,
		Tools:       tools,
		IsFocused:   t.focusedBlockIs,
	})
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
	if content != t.layout.rendered || t.layout.viewportSyncedBuild != t.layout.builds || (content != "" && t.histViewport.TotalLineCount() == 0) {
		t.histViewport.SetContent(content)
		t.layout.viewportSyncedBuild = t.layout.builds
	}
	if t.histFollow {
		t.histViewport.GotoBottom()
	}
	if t.weaver.active {
		return t.highlightedViewportView(content)
	}
	return t.histViewport.View()
}

// highlightedViewportView paints an in-progress drag over only the visible rows.
// Drag motion arrives far more often than keyboard paging, so repainting the
// whole transcript on every mouse move makes selection feel stuck on long
// conversations.
func (t *Transcript) highlightedViewportView(content string) string {
	w, h := t.histViewport.Width(), t.histViewport.Height()
	if sw := t.histViewport.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := t.histViewport.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}
	if w == 0 || h == 0 {
		return ""
	}

	lines := t.layout.renderedLines
	if content != t.layout.rendered {
		lines = strings.Split(content, "\n")
	}
	top := t.histViewport.YOffset()
	bottom := min(top+h, len(lines))
	visible := []string(nil)
	if top < bottom {
		visible = append(visible, lines[top:bottom]...)
	}
	visible = t.weaver.highlightVisible(visible, top, t.theme.selectionBgSGR())

	contentWidth := w - t.histViewport.Style.GetHorizontalFrameSize()
	contentHeight := h - t.histViewport.Style.GetVerticalFrameSize()
	contents := lipgloss.NewStyle().
		Width(contentWidth).
		Height(contentHeight).
		Render(strings.Join(visible, "\n"))
	return t.histViewport.Style.
		UnsetWidth().UnsetHeight().
		Render(contents)
}

// navigateHistory applies a T2 keyboard scroll command to the persisted history viewport owned by the Transcript: PgUp/Home move toward the older output and break the follow position; PgDn/End move toward the newest and re-engage follow when they reach the bottom. PgUp/PgDn page by half the visible height so the reading position keeps its place in view.
func (t *Transcript) navigateHistory(key string) bool {
	switch key {
	case "pgup":
		if t.histViewport.AtTop() {
			return t.histFollow // already at the oldest output; nothing to do
		}
		t.histViewport.ScrollUp(t.pageRows())
		t.histFollow = false // scrolling up breaks follow
	case "home":
		if t.histViewport.AtTop() {
			return t.histFollow
		}
		t.histViewport.GotoTop()
		t.histFollow = false
	case "pgdown":
		t.histViewport.ScrollDown(t.pageRows())
		if t.histViewport.AtBottom() {
			t.histFollow = true // paging to the newest re-engages follow
		}
	case "end":
		t.histViewport.GotoBottom()
		t.histFollow = true
	}
	return t.histFollow
}

// navigateMouse applies a T2 mouse-wheel scroll to the persisted history viewport owned by the Transcript: wheel up scrolls toward older output and breaks follow; wheel down scrolls toward the newest and re-engages follow once it reaches the bottom. Each notch scrolls a tenth of the visible height so the wheel moves the reading position gently.
func (t *Transcript) navigateMouse(msg tea.MouseWheelMsg) bool {
	if !t.inScrollRegion(msg.Y) {
		return t.histFollow
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		if t.histViewport.AtTop() {
			return t.histFollow
		}
		t.histViewport.ScrollUp(t.mouseWheelRows())
		t.histFollow = false
	case tea.MouseWheelDown:
		t.histViewport.ScrollDown(t.mouseWheelRows())
		if t.histViewport.AtBottom() {
			t.histFollow = true
		}
	}
	return t.histFollow
}

func (t *Transcript) pageRows() int {
	rows := t.histViewport.Height() / 2
	if rows < 1 {
		return 1
	}
	return rows
}

func (t *Transcript) mouseWheelRows() int {
	rows := t.histViewport.Height() / 10
	if rows < 1 {
		return 1
	}
	return rows
}

// scrollRegion assembles the history-region seam from the persisted viewport's
// current size and scroll position plus the plain content line count, so render,
// click-drag selection, and wheel scroll route through one region source and
// coordinates and on-screen rows cannot drift apart.
func (t *Transcript) scrollRegion() scrollRegion {
	vp := t.histViewport
	return scrollRegion{
		height:  vp.Height(),
		yOffset: vp.YOffset(),
		content: len(t.plainLines()),
	}
}

// inScrollRegion answers whether a screen row lies inside the history scroll region, read from the single region source — the persisted viewport's height (sized by renderHistoryViewport via scrollRegionHeight).
func (t *Transcript) inScrollRegion(y int) bool {
	return t.scrollRegion().inRegion(y)
}

// contentLineAtScreenRow is the scroll-region hit-test seam: it answers "is a screen row y inside the history scroll region, and which content line does it map to".
func (t *Transcript) contentLineAtScreenRow(y int) (line int, ok bool) {
	return t.scrollRegion().contentLineAtScreenRow(y)
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
	l.rendered = hist.String()
	l.renderedLines = strings.Split(l.rendered, "\n")
	l.plain = l.plain[:0]
	for _, line := range l.renderedLines {
		l.plain = append(l.plain, ansiStrip(line))
	}
	l.dirty = false
	l.builds++
}

// toolEntryAtLine returns the tool entry whose rendered rows include the given content line, and whether that entry currently renders collapsed (a click on a collapsed head toggles it open; on an open entry it toggles closed).
func (t *Transcript) toolEntryAtLine(line int) (idx int, collapsed bool, ok bool) {
	t.ensureLayout()
	toolIdx, _, ok := t.log.AtLine(line, t.layout.rows, t.expansionConfig())
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

// toolExpandedFor reports whether tool entry idx renders expanded under the
// current mode and collapsed-by-default flag.
func (t Transcript) toolExpandedFor(idx int) bool {
	return t.log.expandedFor(idx, t.expansionConfig())
}

// expansionConfig builds the transcript-owned config bundle every expansion
// decision takes: the current global mode plus each kind's collapsed-by-default
// default. One builder keeps every call site on the same values the renderer used.
func (t Transcript) expansionConfig() expansionConfig {
	return expansionConfig{mode: t.viewMode(), cotExpanded: t.cotExpanded, toolExpanded: t.toolResultsExpanded}
}

// appendMsg appends a finished assistant entry to the transcript and marks the shared message layout dirty in the same step, so the appended block re-wraps at the current transcript width on the next frame instead of rendering at a stale width.
func (t *Transcript) appendMsg(content string) {
	t.messages = append(t.messages, message{role: "eitri", content: content, events: synthAnswerLog(content)})
	t.layout.dirty = true
}

// synthAnswerLog builds the one-event answer log every assistant entry owns,
// so entries that never streamed (appended notes, non-streaming turns) still
// render through the FlowRenderer and no legacy render branch survives.
func synthAnswerLog(content string) []TimelineEvent {
	return []TimelineEvent{{Kind: EventAnswer, Delta: content}}
}

// syncStreamSnapshots re-derives the streaming message's content/reasoning text from the turn's event log: the log is the single arrival-ordered source of text, and the snapshots keep copy-to-clipboard, telemetry, and the gateway export reading identical content without touching their seams. The snapshot sync is the one point where streamed text lands in the transcript, so it marks the shared layout cache dirty itself.
func (t *Transcript) syncStreamSnapshots(i int, events []TimelineEvent) {
	m := &t.messages[i]
	m.content, m.reasoning = deriveSnapshots(events)
	t.layout.dirty = true
}

// applyTool routes one tool observation into the tool log and marks the shared layout cache dirty in the same step: an entry changes the tool log's rendered rows.
func (t *Transcript) applyTool(u ToolUpdate) {
	t.log.Apply(u)
	t.layout.dirty = true
}

// endTurn clears the busy state after a completed turn and marks the shared layout cache dirty, so completion-time message finalization re-wraps without caller-side invalidation.
func (t *Transcript) endTurn() {
	t.busy = false
	t.spinner = 0
	t.layout.dirty = true
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
// expand-all mode can show all blocks: the reasoning blocks' collapse-direction
// forces and the tool entries' collapse-direction forces, both on the
// ExpansionState seam.
func (t *Transcript) clearCollapseForces() {
	for i := range t.messages {
		t.messages[i].expansion.clearForcesOf(false)
	}
	t.log.expansion.clearForcesOf(false)
}

// clearExpandForces drops every per-block force-expand flag so the
// collapse-all mode can hide all block bodies: the reasoning blocks'
// expand-direction forces and the tool entries' expand-direction forces, both
// on the seam.
func (t *Transcript) clearExpandForces() {
	for i := range t.messages {
		t.messages[i].expansion.clearForcesOf(true)
	}
	t.log.expansion.clearForcesOf(true)
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
// the log index for a tool block, fragIdx the reasoning fragment index within
// the turn (0 for single-fragment turns and tool blocks).
type collapsibleBlock struct {
	kind    blockKind
	msgIdx  int
	toolIdx int
	fragIdx int
}

// collapsibleBlocks returns the transcript's collapsible blocks in render
// order: each turn's reasoning fragments at their prompt position (one block per
// fragment the flow emits — per streaming delta on a live turn (issue #657), one
// whole-turn block otherwise), then that turn's tool entries in anchored order —
// the traversal Tab cycles the block focus through.
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
			if m.streaming {
				// A live turn's flow flushes one reasoning fragment per delta, so
				// the focus owns one block per emitted fragment in emission order —
				// fragments on both sides of a tool entry included (issue #658 AC1).
				// A turn that never asked for reasoning emits none; its blocks stay
				// unfocusable.
				if m.thinkingRequested {
					for k := range liveReasoningFragments(t.flowEventsFor(m)) {
						blocks = append(blocks, collapsibleBlock{kind: blockReasoning, msgIdx: j, fragIdx: k})
					}
				}
				break
			}
			frags := reasoningFragments(t.flowEventsFor(m))
			if m.thinkingRequested && (m.reasoning != "" || len(frags) > 0) {
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

// flowEventsFor returns the event sequence a message's flow renders from: the
// live timeline while the message is the running turn's streaming reply, else
// its committed event log.
func (t Transcript) flowEventsFor(m message) []TimelineEvent {
	if t.busy && m.streaming && len(t.LiveTimeline()) > 0 {
		return t.LiveTimeline()
	}
	return m.events
}

// liveReasoningFragments returns the reasoning fragment texts a live turn's
// flow emits: one per non-empty reasoning delta, in emission order — exactly the
// per-delta fragments flowRenderer.fold flushes (issue #657). Tool boundaries
// never merge fragments on a live turn, so the focus enumeration must not merge
// them either; a committed turn folds all reasoning into one snapshot block and
// uses reasoningFragments instead.
func liveReasoningFragments(events []TimelineEvent) []string {
	var out []string
	for _, ev := range events {
		if ev.Kind == EventReasoning && ev.Delta != "" {
			out = append(out, ev.Delta)
		}
	}
	return out
}

// reasoningFragments splits an event log into its reasoning fragments: each run
// of consecutive reasoning deltas is one fragment, delimited by the tool
// boundaries renderEventFlow flushes at. Empty runs are dropped, so the returned
// slice indexes exactly the fragments the flow actually emits.
func reasoningFragments(events []TimelineEvent) []string {
	var out []string
	var cur strings.Builder
	for _, ev := range events {
		switch ev.Kind {
		case EventReasoning:
			cur.WriteString(ev.Delta)
		case EventToolStart, EventToolResult:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// focusNext advances the block focus to the next collapsible block, wrapping;
// the first Tab activates the focus cursor on the first block; a transcript
// with no collapsible blocks stays unfocused.
func (t *Transcript) focusNext() {
	t.focus.focusNext(len(t.collapsibleBlocks()))
}

// focused returns the block currently under the focus cursor.
func (t Transcript) focused() (collapsibleBlock, bool) {
	blocks := t.collapsibleBlocks()
	idx, ok := t.focus.focusedIndex(len(blocks))
	if !ok {
		return collapsibleBlock{}, false
	}
	return blocks[idx], true
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
		t.toggleThinkingFragment(blk.msgIdx, blk.fragIdx)
	case blockTool:
		t.toggleToolEntry(blk.toolIdx)
	}
}

// focusedBlockIs reports whether the block identified by kind/msgIdx/toolIdx/
// fragIdx is the one under the focus cursor, so the renderer can mark it.
func (t Transcript) focusedBlockIs(kind blockKind, msgIdx, toolIdx, fragIdx int) bool {
	blk, ok := t.focused()
	return ok && blk.kind == kind && blk.msgIdx == msgIdx && blk.toolIdx == toolIdx && blk.fragIdx == fragIdx
}

// thinkingExpandedForBlock is the free-function form of the whole-turn
// reasoning-block expansion decision, read through the ExpansionState seam by
// both the Transcript and the FlowRenderer. It builds the explicit config bundle
// from the render-time mode and CoT-collapsed-by-default flag and asks the seam
// for the whole-block decision without mutating any stored state, folding in the
// live-stream auto-expand (a streaming reasoning block stays open so the user
// watches chain-of-thought arrive, yielding to a pinned force-collapse or the
// hide-every-body collapse-all mode). A pinned
// whole-block force (the migrated thinkingCollapsed / thinkingExpanded flags now
// live on the seam keyed on reasoningWholeID) always wins, then the global modes,
// then the collapsed-by-default flag.
func thinkingExpandedForBlock(msg message, cfg expansionConfig) bool {
	if msg.streaming && msg.reasoning != "" && cfg.mode != viewCollapseAll {
		// a live streamed block auto-expands unless pinned force-collapsed, so
		// the user watches chain-of-thought arrive; the collapse-all mode's hide-
		// every-body request wins over the auto-expand so E covers every fragment
		// of a live per-delta burst too (issue #658 AC3).
		if f, ok := msg.expansion.forceFor(blockReasoning, reasoningWholeID); ok && !f {
			return false
		}
		return true
	}
	return msg.expansion.expanded(blockReasoning, reasoningWholeID, cfg)
}

// thinkingExpandedForFrag is the free-function form of the per-fragment
// expansion decision, shared by the Transcript and the FlowRenderer: a
// fragment's own pin on the seam wins, else it follows the whole-block decision.
func thinkingExpandedForFrag(msg message, fragIdx int, cfg expansionConfig) bool {
	if f, ok := msg.expansion.forceFor(blockReasoning, fragIdx); ok {
		return f
	}
	return thinkingExpandedForBlock(msg, cfg)
}

// thinkingExpandedForFragment returns whether the fragIdx-th reasoning fragment
// of msg renders expanded: a per-fragment force wins over the whole-block logic
// and the live auto-expand, and a fragment without a force follows the
// whole-block decision. A force is a per-block override, so it beats the global
// modes exactly as before (Enter on a focused block collapses it even in
// expand-all mode); the modes take over only once the forces are cleared on mode
// entry.
func (t Transcript) thinkingExpandedForFragment(msg message, fragIdx int) bool {
	return thinkingExpandedForFrag(msg, fragIdx, t.expansionConfig())
}

// toggleThinkingFragment flips the expansion of one reasoning fragment (Enter on
// a focused interleaved fragment), targeting only that fragment's rendering while
// the others keep their own state — the independent per-fragment collapse of
// issue #449 user story 3. The per-fragment force routes through the seam.
func (t *Transcript) toggleThinkingFragment(i, fragIdx int) {
	if i < 0 || i >= len(t.messages) {
		return
	}
	e := &t.messages[i].expansion
	e.set(blockReasoning, fragIdx, !t.thinkingExpandedForFragment(t.messages[i], fragIdx))
	t.layout.dirty = true // one fragment expanded/collapsed changes its rendered rows
}

// clearReasoningFragments drops the per-fragment reasoning forces of message i
// — the turn-commit cleanup that discards a live turn's fragment pins once its
// chain-of-thought collapses to a single committed block.
func (t *Transcript) clearReasoningFragments(i int) {
	if i < 0 || i >= len(t.messages) {
		return
	}
	t.messages[i].expansion.clearReasoningFragments()
}

// clearReasoningExpandForce drops message i's whole-block force-expand (the old
// thinkingExpanded flag) so a completed turn auto-collapses to its hint outside
// the expand-all mode.
func (t *Transcript) clearReasoningExpandForce(i int) {
	if i < 0 || i >= len(t.messages) {
		return
	}
	e := &t.messages[i].expansion
	if f, ok := e.forceFor(blockReasoning, reasoningWholeID); ok && f {
		e.clear(blockReasoning, reasoningWholeID)
	}
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
