package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Transcript is the named value surface for the transcript region: the
// layout/scroll/follow/render concerns that used to live in the TUI Model
// god-object (issue #243). It is an "expand" seam: it owns the height/width,
// the follow position, the persisted scroll viewport, and the render of the
// agent-history scroll region, so the transcript renders without reaching
// through the rest of Model.
//
// It is deliberately a value type constructed from Model state (newTranscript):
// Model keeps its own transcript fields for now (a later contract step #248
// removes them and makes Transcript standalone), so nothing breaks — the two
// share the same render output. The fixture that proves the seam is real: a
// test constructs a Transcript directly and renders through it, independent of
// Model.
//
// The one pointer field is the persisted viewport (histViewport). It is shared
// with Model — and across Model's value copies in View — so scroll-state
// changes made during a render survive the next re-render cycle. Every other
// field is plain value state the constructor copies out of Model.
//
// Since issue #244 the Transcript also owns the transcript's navigation: the
// pointer-receiver navigateHistory / navigateMouse drive the shared viewport
// and return the resulting follow flag so a forwarding Model can persist the
// decision into its own histFollow copy.
//
// Scope note: Transcript owns the scroll region (the history the user reads)
// and the review overlay that sits above it in the same pane. It does NOT own
// the bottom band (status strip + composer, renderBand); that composer concern
// stays on Model, and the band string is passed into renderPane so Transcript
// can compose the full left pane. render.go/glyphs.go/markdown.go value
// helpers stay where they are.
type Transcript struct {
	// theme is the styling surface the transcript draws from (issue #178).
	theme Theme
	// messages is the committed conversation log rendered in the scroll region.
	messages []message
	// busy is true while a turn runs; it drives the working-state footer row.
	busy bool
	// spinner is the busy-spinner frame index (issue #211); 0 when idle.
	spinner int
	// reasoningEffort is the run's reasoning-effort tier, shown in the collapsed
	// thinking hint (issue #85 AC2).
	reasoningEffort string
	// configTheme is the raw config theme name used to render markdown.
	configTheme string
	// workspacePath is the run's working directory, surfaced as a header row
	// (issue #82 AC1). Empty renders no header.
	workspacePath string
	// log is the deep tool-call log rendered in the transcript (issue #208).
	// Since issue #245 the Transcript owns every tool-log operation — the
	// start/result pairing (apply), per-entry and all-entry expansion (toggle, the
	// click-to-expand hit-test), and the persistent row->entry layout index surface.
	log toolLog
	// showToolResult expands all tool entries to their full result (issue #84).
	// It lives on the Transcript (issue #245): alt+y toggles it through
	// toggleShowToolResult so the render reads Transcript state directly.
	showToolResult bool
	// layout is the persistent transcript layout cache (issue #242), shared with
	// Model through a pointer (like histViewport): one batched renderHistory pass
	// records the row->tool-entry index, the row->message index, and the
	// ANSI-stripped plain-row space the hit-tests read back instead of re-deriving
	// layout on every pointer event. Since issue #245 the click-to-expand hit-test
	// (toolEntryAtLine) reads this recorded index on the Transcript. It is
	// advisory for performance — correctness is guaranteed because renderHistory
	// here is the very pass that builds it. dirty is true while a
	// transcript-affecting change makes the cached index stale.
	layout *transcriptLayout
	// telemetry is the live status-strip surface (issue #86); nil disables the
	// busy footer fallback row.
	telemetry *Telemetry
	// review is the open changed-file review overlay (issue #90); nil means
	// only the transcript scroll region renders.
	review *reviewPanel
	// dragSel tracks an in-progress click-drag selection (issue #124), whose
	// range the scroll region highlights.
	dragSel *dragSelect
	// width is the terminal width; 0 until the first resize lands.
	width int
	// height is the terminal height; 0 until the first resize lands.
	height int
	// histFollow is true while the viewport re-anchors to the newest output
	// (issue #119); T2 navigation (issue #120) breaks it on scroll-up.
	histFollow bool
	// histViewport is the persisted history scroll component (issue #119),
	// shared with Model so scroll state survives render cycles.
	histViewport *viewport.Model
	// railOn records whether the right context rail is wired (issue #88), so
	// the transcript width yields room for it (issue #227).
	railOn bool
}

// newTranscript builds a Transcript value from a Model, extracting the
// transcript-relevant state so the render paths can run without reaching
// through the rest of the Model god-object (issue #243). Model keeps its own
// fields for now; this constructor is the single adapter between them, so the
// two can never drift in what they render.
func newTranscript(m Model) Transcript {
	t := Transcript{
		theme:           m.theme,
		messages:        m.messages,
		busy:            m.busy,
		spinner:         m.spinner,
		reasoningEffort: m.reasoningEffort,
		configTheme:     m.deps.Config.Theme,
		workspacePath:   m.deps.WorkspacePath,
		log:             m.log,
		showToolResult:  m.showToolResult,
		layout:          m.layout,
		telemetry:       m.telemetry,
		review:          m.review,
		dragSel:         m.dragSel,
		width:           m.width,
		height:          m.height,
		histFollow:      m.histFollow,
		histViewport:    m.histViewport,
		railOn:          m.rail != nil,
	}
	return t
}

// railVisible reports whether the right context rail should render now.
func (t Transcript) railVisible() bool { return t.railOn }

// transcriptWidth returns the column width the transcript pane should use for
// wrapping: the terminal width (or a sane default before a resize) minus the
// 2-col gutter, and minus the rail + separator when the rail is visible, so the
// history re-wraps to leave the rail room (issue #88 AC3). A floor keeps the
// pane usable on tiny windows (issue #227 AC3).
func (t Transcript) transcriptWidth() int {
	base := t.width
	if base == 0 {
		base = presizeTerminalWidth
	}
	w := base - 2
	if t.railOn {
		w -= railWidth + 1
		if w < 20 {
			w = 20
		}
	}
	return w
}

// renderPane renders the transcript + composer surface into the left pane. It
// is the single-pane view; when the rail is visible it is overlaid onto the
// pane's top-right by viewString's surfaceWithRail (issue #232) rather than
// joined to the pane's right, so the full-width bottom band stays edge-to-edge.
// band is the already-rendered bottom band (status strip + composer), which the
// Transcript does not own.
//
// Render is split into explicit, ordered regions (issue T01): the review
// overlay region (when open) on top, the scroll region (history), then the
// fixed bottom band. Each region renders independently; renderPane concatenates
// them in order. The scroll region is Height-aware: its content clamps to the
// terminal height, so the band stays pinned and only the history scrolls.
func (t Transcript) renderPane(band string) string {
	bandStr := band

	// Overlay region: the review panel takes over the top of the pane (Layout B,
	// issue #90). It is its own height-clipped region (issue T06).
	reviewStr := ""
	reviewLines := 0
	if t.review != nil {
		var review strings.Builder
		t.renderReview(&review)
		reviewStr = review.String()
		reviewLines = t.reviewRegionRows(reviewStr, lineCount(bandStr))
		reviewStr = clipReviewRegion(reviewStr, reviewLines)
	}

	// The scroll region renders through the native bubbletea/viewport component
	// (T1 alt-screen pivot, issue #119), which owns the history clip + follow.
	var hist strings.Builder
	t.renderHistory(&hist, nil, nil)
	histRegion := t.renderHistoryViewport(hist.String(), lineCount(bandStr)+reviewLines)
	// The scroll region must end on its own row before the band joins: the
	// persisted viewport renders its rows newline-joined with no trailing
	// newline (and pads to the scroll height), so without this terminator the
	// band separator fuses onto the viewport's last padded row.
	if histRegion != "" && !strings.HasSuffix(histRegion, "\n") {
		histRegion += "\n"
	}
	return reviewStr + histRegion + bandStr
}

// renderReview renders the review panel over the transcript (issue #90): a
// dense summary of touched files with add/delete counts and status, plus the
// focused file's inline diff when expanded, and an open_in_browser hint.
func (t Transcript) renderReview(b *strings.Builder) {
	r := t.review
	if r == nil {
		return
	}
	b.WriteString(t.theme.statusStyle.Render("ctrl+d  ") + t.theme.headerStyle.Render(fmt.Sprintf("Review changed files (%d)", len(r.files))))
	b.WriteString("\n")
	if len(r.files) == 0 {
		b.WriteString(t.theme.statusStyle.Render("  no changes yet"))
		b.WriteString("\n")
		return
	}
	for i, f := range r.files {
		marker := " "
		if i == r.cursor {
			marker = t.theme.headerStyle.Render(g("▶", ">")) // accent cursor marker
		}
		status := t.theme.statusStyle.Render(f.status)
		switch f.status {
		case "added":
			status = t.theme.outcomeOKStyle.Render(f.status)
		case "deleted":
			status = t.theme.outcomeErrStyle.Render(f.status)
		}
		b.WriteString(marker + " " + f.path + "  " + t.theme.statusStyle.Render(deltaTag(f.added, f.removed)) + "  " + status)
		b.WriteString("\n")
	}
	if r.expanded && r.cursor < len(r.files) {
		b.WriteString(renderDiff(r.files[r.cursor], t.theme))
	}
	if r.openErr != "" {
		b.WriteString(t.theme.statusStyle.Render("open_in_browser: " + r.openErr))
		b.WriteString("\n")
		// The open_in_browser error is a one-shot band note: it renders once the
		// frame after the failure, then clears so subsequent frames don't redraw it
		// (issue #90). r is the shared *reviewPanel, so clearing here persists.
		r.openErr = ""
	}
	b.WriteString(t.theme.statusStyle.Render("  enter: toggle diff " + g("·", ".") + " o: open_in_browser " + g("·", ".") + " ctrl+d: close"))
	b.WriteString("\n")
}

// reviewRegionRows returns how many rows the review overlay region may occupy
// when open (issue T06 AC1): at most reviewRegionMax rows, never more than the
// terminal leaves after the fixed bottom band.
func (t Transcript) reviewRegionRows(content string, bandLines int) int {
	rrows := lineCount(content)
	capRows := reviewRegionMax
	if t.height > 0 {
		avail := t.height - bandLines
		if avail < capRows {
			capRows = avail
		}
		if capRows < 0 {
			capRows = 0
		}
	}
	if rrows > capRows {
		return capRows
	}
	return rrows
}

// buildReview assembles the review panel from the transcript's accumulated
// file-mutating tool-log entries (issue #90, #246). It delegates to the tool
// log's Review projection, which keeps the most recent state per path and
// derives each file's status from the before/after content the engine
// captured. It never touches the repo or the live loop. Since issue #246 the
// transcript owns the build; ctrl+d routes through toggleReview.
func (t Transcript) buildReview() reviewPanel {
	return reviewPanel{files: t.log.Review()}
}

// toggleReview flips the changed-file review overlay open or closed (issue
// #90, #246 AC2): ctrl+d on the Model forwards here. When closed it builds the
// panel from the transcript's tool log; when open it dismisses it back to the
// transcript surface.
func (t *Transcript) toggleReview() {
	if t.review != nil {
		t.review = nil
		return
	}
	rp := t.buildReview()
	t.review = &rp
}

// renderHistory renders the scroll region: the agent history that the user
// reads and scrolls. It surfaces the workspace header, every committed message
// (thinking blocks + markdown body), the interleaved tool entries, and the
// busy indicator. It is the only region T02+ makes scrollable and height-
// clamps.
//
// toolRows, when non-nil, receives the row span of every tool entry written,
// in content-line coordinates; msgRows, when non-nil, receives the row span of
// every message written (issue #242). Every block ends on a newline, so the
// newline count before a write equals the content row index where it starts.
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
	// Surface the project's read-only state (issue #82 AC1).
	if t.workspacePath != "" {
		emit(t.theme.statusStyle.Render("workspace: " + t.workspacePath))
		emit("\n")
	}
	// The empty-transcript welcome (first-run discoverability), gone on submit.
	if len(t.messages) == 0 && !t.busy {
		emit(idleWelcome(t.theme))
	}
	for i, msg := range t.messages {
		msgStart := nl // content row where this message's block begins
		// Reasoning renders as a distinct, collapsible per-turn block (issue #85).
		if msg.role != "you" && msg.reasoning != "" {
			emit(thinkingHeader(t.theme, msg.reasoning, t.reasoningEffort))
			if msg.thinkingExpanded {
				emit(msg.reasoning + "\n")
			}
		}
		// Wrap the history content at the transcript width, decoupled from the
		// composer/band width (issue #232 AC4).
		w := t.transcriptWidth()
		if msg.role == "you" {
			md, _ := RenderMarkdown(msg.content, w-4, t.configTheme)
			bubble := renderUserPromptCard(t.theme, md, w)
			emit(bubble + "\n")
		} else {
			md, _ := RenderMarkdown(msg.content, w-2, t.configTheme)
			pane := t.theme.agentPaneStyle
			if strings.HasPrefix(msg.content, failurePrefix()) {
				pane = t.theme.errorPaneStyle
			}
			pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
			emit(fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n"))))
		}
		// Interleave the turn's tool-call entries right after its prompting
		// "you" message (issue #84). Rendering and content-row accounting share
		// one layout pass owned by the tool log (issue #208).
		now := time.Time{}
		if t.busy {
			now = time.Now()
		}
		blockStart := nl
		toolBlock, blockRows := t.log.Render(t.theme, t.showToolResult, now, w, i)
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
	// The busy indicator normally lives in the always-visible status strip
	// (renderBand); lean embeds and tests without a telemetry strip keep the
	// history footer row instead.
	if t.busy && t.telemetry == nil {
		emit(t.theme.statusStyle.Render(busyLine(t.spinner)))
		emit("\n")
	}
}

// renderHistoryViewport returns the Height-clamped scroll region: the rendered
// history content limited to the rows the non-reserved regions (the fixed
// bottom band, plus the review overlay when open — issue T06) do not occupy.
// Until the first resize lands (t.height == 0) the history renders unclamped.
//
// The clip is served through the persisted bubbletea/viewport scroll component
// (T1 alt-screen pivot, issue #119), which natively owns the scroll position +
// clip, re-anchoring to the newest output while follow is engaged (issue #108
// AC1/AC2). User navigation on the viewport (T2, issue #120) is exposed through
// navigateHistory / navigateMouse on this same value (issue #244).
func (t Transcript) renderHistoryViewport(content string, reserved int) string {
	if t.height <= 0 {
		return content
	}
	vh := t.height - reserved
	if vh <= 0 {
		return ""
	}
	vp := t.histViewport
	if vp == nil {
		return bottomSlice(content, vh)
	}
	vp.SetWidth(t.transcriptWidth())
	vp.SetHeight(vh)
	// An in-progress drag selection highlights its cell range in the full
	// content before the viewport clips it (issue #124 AC1).
	if t.dragSel != nil {
		content = t.highlightSelection(content)
	}
	vp.SetContent(content)
	if t.histFollow {
		vp.GotoBottom()
	}
	return vp.View()
}

// navigateHistory applies a T2 (issue #120) keyboard scroll command to the
// persisted history viewport owned by the Transcript: PgUp/Home move toward the
// older output and break the follow position; PgDn/End move toward the newest
// and re-engage follow when they reach the bottom. It never touches the
// composer, so editing focus is preserved (AC4). The viewport holds its scroll
// state across renders even while the history re-renders each frame.
//
// The method is a pointer receiver because the viewport (histViewport) is a
// pointer shared with Model, and it returns the resulting follow flag so the
// Model that forwarded the key can persist the Transcript's decision back into
// its own histFollow copy (issue #244).
func (t *Transcript) navigateHistory(key string) bool {
	vp := t.histViewport
	if vp == nil {
		return t.histFollow
	}
	switch key {
	case "pgup":
		if vp.AtTop() {
			return t.histFollow // already at the oldest output; nothing to do
		}
		vp.PageUp()
		t.histFollow = false // scrolling up breaks follow
	case "home":
		if vp.AtTop() {
			return t.histFollow
		}
		vp.GotoTop()
		t.histFollow = false
	case "pgdown":
		vp.PageDown()
		if vp.AtBottom() {
			t.histFollow = true // paging to the newest re-engages follow
		}
	case "end":
		vp.GotoBottom()
		t.histFollow = true
	}
	return t.histFollow
}

// navigateMouse applies a T2 (issue #120 AC1) mouse-wheel scroll to the
// persisted history viewport owned by the Transcript: wheel up scrolls toward
// older output and breaks follow; wheel down scrolls toward the newest and
// re-engages follow once it reaches the bottom. It never touches the composer,
// preserving input focus. Bubble Tea delivers mouse events only when the
// program enables them (internal/app/tui.go).
//
// Like navigateHistory it is a pointer receiver on the shared viewport and
// returns the resulting follow flag for the forwarding Model to persist (issue
// #244).
func (t *Transcript) navigateMouse(msg tea.MouseWheelMsg) bool {
	vp := t.histViewport
	if vp == nil {
		return t.histFollow
	}
	switch msg.Button {
	case tea.MouseWheelUp:
		if vp.AtTop() {
			return t.histFollow
		}
		vp.ScrollUp(3)
		t.histFollow = false
	case tea.MouseWheelDown:
		vp.ScrollDown(3)
		if vp.AtBottom() {
			t.histFollow = true
		}
	}
	return t.histFollow
}

// highlightSelection wraps the cells covered by an in-progress drag in reverse
// video across the full rendered history content; the persisted viewport clips
// it to the visible window (issue #124 AC1).
func (t Transcript) highlightSelection(content string) string {
	d := t.dragSel
	if d == nil {
		return content
	}
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
			to = lipgloss.Width(lines[i]) - 1
		}
		lines[i] = highlightRange(lines[i], from, to)
	}
	return strings.Join(lines, "\n")
}

// ensureLayout lazily builds the persistent transcript layout cache (issue
// #242) when it is dirty, exactly once per transcript change. It runs ONE
// renderHistory pass into a scratch builder, capturing the row->tool-entry
// index AND building the ANSI-stripped plain-row space (the drag-select copy
// coordinates) from the same builder, then clears dirty so repeated hit-tests
// (mouse motion, toolEntryAtLine) reuse the recorded index until the next
// transcript-affecting change. The layout is a pointer shared with Model, so the
// cache recorded by a Transcript forwarded from Update (which runs on a *Model)
// persists across a drag's motion events — and across the value copies View
// makes, because the cache itself stays in one shared location.
func (t *Transcript) ensureLayout() {
	if t.layoutPtr().dirty {
		t.recordLayout()
	}
}

// layoutPtr guarantees the shared layout cache exists, lazily allocating it on
// first use. All tool-log and hit-test routes allocate through this so the
// nil-guard lives in one place instead of five.
func (t *Transcript) layoutPtr() *transcriptLayout {
	if t.layout == nil {
		t.layout = &transcriptLayout{dirty: true}
	}
	return t.layout
}

// recordLayout performs the one batched layout pass behind the persistent cache
// (issue #242): it renders the history into a scratch builder, captures the
// toolRows and msgRows out-params, and derives the ANSI-stripped plain rows from
// the same builder, storing both indexes and clearing dirty. The builds count
// incremented here backs the issue #242 AC4 test hook, which asserts a repeated
// hit-test reuses the recorded index (one build).
func (t *Transcript) recordLayout() {
	l := t.layoutPtr()
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

// toolEntryAtLine returns the tool entry whose rendered rows include the given
// content line, and whether that entry is currently collapsed (a click on a
// collapsed head toggles it open; on an open entry it toggles closed). It
// reads the persistent layout cache (issue #242) owned by the Transcript (issue
// #245): it lazily builds the row->tool-entry index once per transcript change
// (via recordLayout, the shared renderHistory pass the viewport and mouse
// coordinates use), so it never drifts from what the user sees and a drag
// reuses the recorded index instead of re-deriving layout each event (AC3).
// It is a pointer receiver because it mutates the shared layout cache.
func (t *Transcript) toolEntryAtLine(line int) (idx int, collapsed bool, ok bool) {
	t.ensureLayout()
	return t.log.AtLine(line, t.layout.rows)
}

// toggleToolEntry flips one tool entry's expansion state (mouse click
// click-to-expand, issue #245 AC1). It delegates to the tool log's
// bounds-checked Toggle, never touches other entries or the global alt+y flag,
// and marks the shared layout dirty so an expanded/collapsed entry's new row
// span is re-recorded before the next hit-test. Model forwards here and
// persists the mutated log back into its own copy (issue #248 removes it).
func (t *Transcript) toggleToolEntry(idx int) {
	t.log.Toggle(idx)
	t.layoutPtr().dirty = true // an entry expanded/collapsed changes its rendered rows
}

// apply folds one tool-call observation into the transcript's log (issue #245
// AC1/AC2): tool updates now route through the Transcript so they land in the
// same log renderPane reads. It delegates to the tool log's Apply (start/result
// pairing) and marks the shared layout dirty so the new entry's rows are
// re-recorded. Model keeps its own log copy for now and persists the resulting
// log back into its state (issue #248 removes the duplicate).
func (t *Transcript) apply(u ToolUpdate) {
	t.log.Apply(u)
	t.layoutPtr().dirty = true // an entry changed the tool log's rendered rows
}

// toggleShowToolResult flips the global all-entries expansion state (issue #245
// AC2): alt+y on the Model forwards here, reports the new value back so the
// Model can persist it, and marks the shared layout dirty because showing or
// hiding all tool results re-wraps the log.
func (t *Transcript) toggleShowToolResult() bool {
	t.showToolResult = !t.showToolResult
	t.layoutPtr().dirty = true // showing/hiding all tool results re-wraps the log
	return t.showToolResult
}
