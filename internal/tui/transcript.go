package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Transcript is the single owner of the transcript region: the
// layout/scroll/follow/render concerns that used to live in the TUI Model
// god-object, and the only home of
// the transcript state. It owns the height/width, the messages, the tool log,
// the right context rail, the follow position, the drag-select, and the
// render of the agent-history scroll region.
//
// Model holds a live Transcript value as its owned tx field: the
// Transcript is a genuinely owned, mutating surface rather than a per-frame
// value rebuilt from duplicated Model fields. NewModelCfg constructs it once;
// Model mutates it in place (appends messages, applies tool updates, drives
// navigation) and the render paths read it directly.
//
// The one pointer field is the persisted viewport (histViewport): it is shared
// with the value copies Bubble Tea makes during View (which runs on a value
// Model holding the same tx), so scroll-state changes made during a render
// survive the next re-render cycle. The layout cache (layout) and drag-select
// (dragSel) is likewise a heap-allocated pointer so its state survives those
// value copies.
//
// The Transcript also owns the transcript's navigation: the
// pointer-receiver navigateHistory / navigateMouse drive the shared viewport
// and mutate the follow flag in place.
//
// Scope note: Transcript owns the scroll region (the history the user reads),
// the right context rail — its visibility, band/transcript width accounting,
// clamp height, and render all resolve here. It does NOT own the bottom band
// (status strip + composer, renderBand); that composer concern stays on Model,
// the band string is passed into renderPane so Transcript can compose the full
// left pane, and the band's row count is passed in for railClampHeight and the
// surface merge. render.go/glyphs.go/markdown.go value helpers stay where they
// are.
type Transcript struct {
	// theme is the styling surface the transcript draws from .
	theme Theme
	// messages is the committed conversation log rendered in the scroll region.
	messages []message
	// busy is true while a turn runs; it drives the working-state footer row.
	busy bool
	// spinner is the busy-spinner frame index ; 0 when idle.
	spinner int
	// busyPulse counts down the accent-bright frames after the first stream
	// delta arrives; 0 means no pulse, >0 means render the spinner bright.
	busyPulse int
	// reasoningEffort is the run's reasoning-effort tier, shown in the collapsed
	// thinking hint .
	reasoningEffort string
	// configTheme is the raw config theme name used to render markdown.
	configTheme string
	// workspacePath is the run's working directory, surfaced as a header row
	// . Empty renders no header.
	workspacePath string
	// log is the deep tool-call log rendered in the transcript .
	// Since the Transcript owns every tool-log operation — the
	// start/result pairing (apply), per-entry and all-entry expansion (toggle, the
	// click-to-expand hit-test), and the persistent row->entry layout index surface.
	log toolLog
	// expandAll is the persistent Ctrl+E "expanded view" mode: one
	// global flag that switches every tool entry — past and future turns —
	// between the collapsed (default, noise-reduced) delta summary and the fully
	// expanded framed result card. It is sticky until toggled off. It evolves the
	// legacy alt+y showToolResult toggle into the single
	// Ctrl+E-bound mode; Ctrl+E routes through toggleExpandAll so the render
	// reads Transcript state directly.
	expandAll bool
	// layout is the persistent transcript layout cache, shared with
	// Model through a pointer (like histViewport): one batched renderHistory pass
	// records the row->tool-entry index, the row->message index, and the
	// ANSI-stripped plain-row space the hit-tests read back instead of re-deriving
	// layout on every pointer event. Since the click-to-expand hit-test
	// (toolEntryAtLine) reads this recorded index on the Transcript. It is
	// advisory for performance — correctness is guaranteed because renderHistory
	// here is the very pass that builds it. dirty is true while a
	// transcript-affecting change makes the cached index stale.
	layout *transcriptLayout
	// telemetry is the live status-strip surface ; nil disables the
	// busy footer fallback row.
	telemetry *Telemetry
	// dragSel tracks an in-progress click-drag selection, whose
	// range the scroll region highlights.
	dragSel *dragSelect
	// width is the terminal width; 0 until the first resize lands.
	width int
	// height is the terminal height; 0 until the first resize lands.
	height int
	// histFollow is true while the viewport re-anchors to the newest output
	// ; T2 navigation breaks it on scroll-up.
	histFollow bool
	// histViewport is the persisted history scroll component,
	// shared across Model's value copies so scroll state survives render cycles.
	histViewport *viewport.Model

	// railWidth is the right rail's column width; 0 until a width is set, in
	// which case consumers fall back to defaultRailWidth. Drag-resize (issue
	// #305) mutates it to re-wrap the transcript and re-render the rail.
	railWidth int

	// rail is the right context pane ; nil disables it. Its
	// visibility (railVisible), band/transcript width accounting
	// (bandWidth/transcriptWidth), clamp height (railClampHeight), and render
	// (viewWithRail) all resolve on this surface, so the transcript width
	// yields room for it .
	rail *Rail
}

// railVisible reports whether the right context rail should render now. The
// rail is the sole, permanent stats surface: it is always on
// whenever it is wired — no auto-hide on small windows and no ctrl+b toggle.
// The transcript keeps a hard floor via transcriptWidth, so on an
// extreme-minimum terminal the rail yields width so the transcript stays
// readable .
func (t Transcript) railVisible() bool { return t.rail != nil }

// transcriptWidth returns the column width the transcript pane should use for
// wrapping: the terminal width (or a sane default before a resize) minus the
// 2-col gutter, and minus the rail + separator when the rail is visible, so the
// history re-wraps to leave the rail room . A floor keeps the
// pane usable on tiny windows .
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

// bandWidth returns the column width the bottom band renders at: the terminal
// width (or a sane non-composer default before the first resize lands) minus the
// 2-col gutter. The band is the edge-to-edge bottom region: it
// spans the full terminal width all the way under the right rail, so its
// separator row, status strip, and composer run to the width's edge — no
// railWidth x bandHeight dead corner (the rail yields the transcript its
// columns). It is independent of transcriptWidth() in
// the call graph (it never calls transcriptWidth and never reads the composer's
// width). bandWidth is the SEAM for: it is the single width source
// for the bottom band, independent of transcriptWidth(). Since it is
// owned by the Transcript (the band asks the Transcript for its width).
func (t Transcript) bandWidth() int {
	base := t.width
	if base == 0 {
		base = presizeTerminalWidth // no resize yet; use a sane full-width start
	}
	return base - 2
}

// railClampHeight returns the maximum number of rows the right context rail may
// occupy so it matches the history region's visible height:
// both panes clamp to the rows left over by the fixed bottom band, so the two
// form one coherent row. It is -1 before the first resize lands, leaving the
// rail unclamped — mirroring renderHistoryViewport; a non-negative result is
// the actual row budget (0 when the band fills the whole terminal, in which
// case the rail renders nothing). Since it lives on the Transcript;
// bandHeight (the fixed bottom band's row count) is passed in by the caller
// because the band itself stays a Model-owned concern ( keeps it
// there).
func (t Transcript) railClampHeight(bandHeight int) int {
	if t.height <= 0 {
		return -1
	}
	// The rail shares the history viewport's vertical budget: terminal height
	// minus whatever the fixed bottom band occupies.
	vh := t.height - bandHeight
	if vh < 0 {
		return 0
	}
	return vh
}

// surfaceWithRail merges the rendered right rail into a full-width pane so the
// bottom band stays edge-to-edge: the band is a
// bottom-anchored region spanning the whole terminal width, so the rail cannot
// sit to its right the way it sits beside the transcript. Instead the rail
// floats ABOVE the band — its rows land in the top railClampHeight() rows of the
// pane, in the railWidth column strip at the right — the Transcript's mutable
// rail width — exactly the room the rail-shrunk transcriptWidth leaves on each
// history row. The band rows (below
// the rail's extent) are untouched, so the separator/status/composer run the
// full width; the rail never overlaps them. pane is the renderPane output; rail
// is styledRail output already clamped to railClampHeight rows. bandHeight is the
// fixed bottom band's row count, which the caller (the Model that owns the band)
// passes in.
//
// Before the first resize lands the height is unknown, so there is no pinned
// band for the rail to float above; the rail falls back to joining the pane's
// right, preserved from the pre-#232 layout for lean embeds that never size.
func (t Transcript) surfaceWithRail(pane, rail string, bandHeight int) string {
	vh := t.railClampHeight(bandHeight)
	if vh <= 0 {
		// No height yet (or the band fills the whole terminal): pre-resize, fall
		// back to the rail beside the pane — there is no pinned band to float
		// above. When the band fills the whole terminal the rail renders nothing
		// and the pane (full-width band) is already complete.
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

// viewWithRail composes the final surface content for a rail-visible pane: it
// renders the wired rail through styledRail at the rail's clamp height and
// floats it above the full-width band via surfaceWithRail. When no rail is
// wired it returns the pane untouched. It is the single rail-render seam (issue
// #247): the rail's visibility, clamp, and render all resolve here on the
// Transcript. bandHeight is the bottom band's row count passed in by the Model
// that owns the band.
func (t Transcript) viewWithRail(pane string, bandHeight int) string {
	if !t.railVisible() {
		return pane
	}
	rw := t.railWidthOrDefault()
	right := styledRail(t.rail.render(t.telemetry, t.theme, rw), t.railClampHeight(bandHeight), rw)
	return t.surfaceWithRail(pane, right, bandHeight)
}

// railWidthOrDefault returns the rail width in effect: the mutable field when
// set, else the default. The 0 fallback mirrors how width/height read 0 until
// the first resize lands, so a hand-built Transcript renders like the
// pre-#305 const-width rail without setting the field.
func (t Transcript) railWidthOrDefault() int {
	if t.railWidth == 0 {
		return defaultRailWidth
	}
	return t.railWidth
}

// setRailWidth stores the rail width and marks the shared layout cache dirty, so
// the next render pass re-wraps the history at the new transcript width and
// re-records the row layout . scroll/follow survive because the
// persisted viewport keeps its position; it is only re-sized, never re-created.
func (t *Transcript) setRailWidth(w int) {
	t.railWidth = w
	t.layoutPtr().dirty = true
}

// renderPane renders the transcript + composer surface into the left pane. It
// is the single-pane view; when the rail is visible it is overlaid onto the
// pane's top-right by viewString's surfaceWithRail rather than
// joined to the pane's right, so the full-width bottom band stays edge-to-edge.
// band is the already-rendered bottom band (status strip + composer), which the
// Transcript does not own.
//
// Render is split into explicit, ordered regions: the scroll
// region (history), then the fixed bottom band. Each region renders
// independently; renderPane concatenates them in order. The scroll region is
// Height-aware: its content clamps to the terminal height, so the band stays
// pinned and only the history scrolls.
func (t Transcript) renderPane(band string) string {
	bandStr := band

	// The scroll region renders through the native bubbletea/viewport component
	// (T1 alt-screen pivot, ), which owns the history clip + follow.
	var hist strings.Builder
	t.renderHistory(&hist, nil, nil)
	histRegion := t.renderHistoryViewport(hist.String(), lineCount(bandStr))
	// The scroll region must end on its own row before the band joins: the
	// persisted viewport renders its rows newline-joined with no trailing
	// newline (and pads to the scroll height), so without this terminator the
	// band separator fuses onto the viewport's last padded row.
	if histRegion != "" && !strings.HasSuffix(histRegion, "\n") {
		histRegion += "\n"
	}
	return histRegion + bandStr
}

// renderHistory renders the scroll region: the agent history that the user
// reads and scrolls. It surfaces the workspace header, every committed message
// (thinking blocks + markdown body), the interleaved tool entries, and the
// busy indicator. It is the only region T02+ makes scrollable and height-
// clamps.
//
// toolRows, when non-nil, receives the row span of every tool entry written,
// in content-line coordinates; msgRows, when non-nil, receives the row span of
// every message written . Every block ends on a newline, so the
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
	// Surface the project's read-only state .
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
		// Reasoning renders as a distinct, collapsible per-turn block .
		// A reasoning block renders only when the turn requested thinking AND the
		// backend actually streamed reasoning: a misbehaving
		// backend sneaking chain-of-thought through a thinking-off turn must be
		// hidden at the display layer. thinkingRequested is folded into message
		// state at request time, never re-sniffed from config here.
		if msg.role != "you" && msg.thinkingRequested && msg.reasoning != "" {
			emit(thinkingHeader(t.theme, msg.reasoning, t.reasoningEffort))
			if t.thinkingExpandedFor(msg) {
				emit(msg.reasoning + "\n")
			}
		}
		// Wrap the history content at the transcript width, decoupled from the
		// composer/band width .
		w := t.transcriptWidth()
		if msg.role == "you" {
			md, _ := RenderMarkdown(msg.content, w-4, t.configTheme)
			bubble := renderUserPromptCard(t.theme, md, w)
			emit(bubble + "\n")
		} else {
			md, _ := RenderMarkdown(msg.content, w-2, t.configTheme)
			pane := t.theme.agentPaneStyle
			if msg.stopped {
				// A user-stopped turn keeps its partial output in a distinct
				// pane (accent-dimmed, not error red) with the stopped marker
				// underneath, so it reads as deliberately aborted.
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
		// Interleave the turn's tool-call entries right after its prompting
		// "you" message . Rendering and content-row accounting share
		// one layout pass owned by the tool log .
		now := time.Time{}
		if t.busy {
			now = time.Now()
		}
		blockStart := nl
		toolBlock, blockRows := t.log.Render(t.theme, t.expandAll, now, w, i)
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
		if t.busyPulse > 0 {
			emit(t.theme.bandStatusStyle.Render(busyLine(t.spinner, t.phase())))
		} else {
			emit(t.theme.statusStyle.Render(busyLine(t.spinner, t.phase())))
		}
		emit("\n")
	}
}

// renderHistoryViewport returns the Height-clamped scroll region: the rendered
// history content limited to the rows the fixed bottom band (the only
// non-reserved region) does not occupy.
// Until the first resize lands (t.height == 0) the history renders unclamped.
//
// The clip is served through the persisted bubbletea/viewport scroll component
// (T1 alt-screen pivot, ), which natively owns the scroll position +
// clip, re-anchoring to the newest output while follow is engaged (
// AC1/AC2). User navigation on the viewport (T2, ) is exposed through
// navigateHistory / navigateMouse on this same value .
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
	// content before the viewport clips it .
	if t.dragSel != nil {
		content = t.highlightSelection(content)
	}
	vp.SetContent(content)
	if t.histFollow {
		vp.GotoBottom()
	}
	return vp.View()
}

// navigateHistory applies a T2 keyboard scroll command to the
// persisted history viewport owned by the Transcript: PgUp/Home move toward the
// older output and break the follow position; PgDn/End move toward the newest
// and re-engage follow when they reach the bottom. It never touches the
// composer, so editing focus is preserved (AC4). The viewport holds its scroll
// state across renders even while the history re-renders each frame.
//
// The method is a pointer receiver because the viewport (histViewport) is a
// pointer shared across Model's value copies, and it mutates the follow flag in
// place; the returned flag reports the resulting state .
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

// navigateMouse applies a T2 mouse-wheel scroll to the
// persisted history viewport owned by the Transcript: wheel up scrolls toward
// older output and breaks follow; wheel down scrolls toward the newest and
// re-engages follow once it reaches the bottom. It never touches the composer,
// preserving input focus. Bubble Tea delivers mouse events only when the
// program enables them (internal/app/tui.go).
//
// Like navigateHistory it is a pointer receiver on the shared viewport that
// mutates the follow flag in place; the returned flag reports the resulting
// state .
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
// it to the visible window .
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
		// Intermediate rows extend to their last rune. highlightRange counts
		// RUNES (skipping escape sequences), and selection columns are
		// rune-indexed, so the bound must be the plain line's rune
		// count minus 1 — not the display width, which would over- or
		// under-shoot for wide/multibyte characters.
		if i < endLine {
			to = len([]rune(ansiStrip(lines[i]))) - 1
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
//: it renders the history into a scratch builder, captures the
// toolRows and msgRows out-params, and derives the ANSI-stripped plain rows from
// the same builder, storing both indexes and clearing dirty. The builds count
// incremented here backs the test hook, which asserts a repeated
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
// content line, and whether that entry currently renders collapsed under the
// Ctrl+E expanded-view mode — a click on a collapsed head toggles
// it open; on an open entry it toggles closed. It reads the persistent layout
// cache owned by the Transcript: it lazily builds the
// row->tool-entry index once per transcript change (via recordLayout, the
// shared renderHistory pass the viewport and mouse coordinates use), so it
// never drifts from what the user sees and a drag reuses the recorded index
// instead of re-deriving layout each event (AC3). The effective collapse is
// derived from the same expandedFor computation Render uses, so the hit-test
// and the rendered rows always agree. It is a pointer receiver because it
// mutates the shared layout cache.
func (t *Transcript) toolEntryAtLine(line int) (idx int, collapsed bool, ok bool) {
	t.ensureLayout()
	toolIdx, _, ok := t.log.AtLine(line, t.layout.rows)
	if !ok {
		return 0, false, false
	}
	// The entry's effective rendered state (per-entry open/override beats the
	// global expandAll mode) is the collapse truth, not the raw per-entry flag.
	return toolIdx, !t.log.expandedFor(toolIdx, t.expandAll), true
}

// toggleToolEntry flips one tool entry's expansion state (mouse click
// click-to-expand, ), kept independent of the Ctrl+E
// expanded-view mode . When the global mode is OFF it delegates to
// the tool log's bounds-checked Toggle (open/close that entry); when the global
// mode is ON it collapses/re-expands just that entry via toggleCollapse so a
// per-entry collapse still works even while everything else stays expanded. It
// never touches other entries and marks the shared layout dirty so the entry's
// new row span is re-recorded before the next hit-test. The Transcript owns the
// log .
func (t *Transcript) toggleToolEntry(idx int) {
	if t.expandAll {
		// Global mode ON: click collapses/re-expands just this entry through the
		// per-entry override so it stays orthogonal to the mode.
		t.toggleCollapse(idx)
	} else {
		t.log.Toggle(idx)
		t.layoutPtr().dirty = true // an entry expanded/collapsed changes its rendered rows
	}
}

// toggleCollapse flips one entry's per-entry collapse-override: it
// keeps a single entry collapsed even while the global expanded-view mode is ON,
// and flips back to let the mode show it. It is the single delegation path for
// the expandAll-mode click and for tests, and it is a thin wrapper over the tool
// log's owned bounds-checked operation plus the shared layout dirty mark.
func (t *Transcript) toggleCollapse(idx int) {
	t.log.ToggleCollapse(idx)
	t.layoutPtr().dirty = true
}

// apply folds one tool-call observation into the transcript's log (
// AC1/AC2): tool updates now route through the Transcript so they land in the
// same log renderPane reads. It delegates to the tool log's Apply (start/result
// pairing) and marks the shared layout dirty so the new entry's rows are
// re-recorded. The Transcript owns the log .
func (t *Transcript) apply(u ToolUpdate) {
	t.log.Apply(u)
	t.layoutPtr().dirty = true // an entry changed the tool log's rendered rows
}

// toggleExpandAll flips the persistent Ctrl+E expanded-view mode:
// Ctrl+E on the Model routes here, and it marks the shared layout
// dirty because showing or hiding all tool results re-wraps the log. It is the
// single global mode; per-entry click-to-expand stays orthogonal.
func (t *Transcript) toggleExpandAll() bool {
	t.expandAll = !t.expandAll
	t.layoutPtr().dirty = true // showing/hiding all tool results re-wraps the log
	return t.expandAll
}

// thinkingExpandedFor returns whether msg's reasoning block renders expanded
// given the persistent Ctrl+E expanded-view mode: a per-turn
// thinkingCollapsed override (tab while the mode is ON) forces this single
// block collapsed, and otherwise the block reflects the global mode (issue
// #274). It mirrors the tool log's expandedFor so the reasoning block and the
// tool cards obey the same effective-expansion computation — a turn started
// with the mode ON renders expanded even though its per-turn
// thinkingExpanded flag defaults false, because the mode overrides it at
// render time.
func (t Transcript) thinkingExpandedFor(msg message) bool {
	if msg.thinkingCollapsed {
		return false
	}
	return t.expandAll || msg.thinkingExpanded
}

// toggleThinking flips one turn's reasoning-block expansion (tab in the
// composer, ), kept independent of the Ctrl+E expanded-view mode
// . When the global mode is OFF it toggles the classic per-turn
// thinkingExpanded flag; when the mode is ON it collapses/re-expands just this
// block through the per-turn thinkingCollapsed override so a single block can
// still be collapsed while everything else stays expanded (mirroring the tool
// log's toggleCollapse). It never touches other messages and marks the shared
// layout dirty so the block's new row span is re-recorded before the next
// hit-test.
func (t *Transcript) toggleThinking(i int) {
	if i < 0 || i >= len(t.messages) {
		return
	}
	msg := &t.messages[i]
	if t.expandAll {
		msg.thinkingCollapsed = !msg.thinkingCollapsed
	} else {
		msg.thinkingExpanded = !msg.thinkingExpanded
	}
	t.layoutPtr().dirty = true // a thinking block expanded/collapsed changes rows
}

// plainLines returns the history scroll content as plain text per rendered row
// (ANSI stripped) — the coordinate space drag selection maps into. The split
// matches the persisted viewport's own line split exactly, so content line
// indexes agree between selection and the rendered transcript. It is lazy +
// cached: it reads the persistent layout cache, rebuilding it once
// per transcript change via recordLayout so a drag's motion events reuse the
// recorded plain-row space instead of re-rendering each one. It is owned by the
// Transcript .
func (t *Transcript) plainLines() []string {
	t.ensureLayout()
	return t.layout.plain
}

// messageAtLine returns the message whose rendered rows include the given
// content line, via the persistent row->message index . It reads
// the same lazy cache as toolEntryAtLine, so it never re-derives layout and
// cannot drift from what the transcript renders. ok is false when the line maps
// to no committed message (the workspace header, idle welcome, or busy footer).
func (t *Transcript) messageAtLine(line int) (idx int, ok bool) {
	t.ensureLayout()
	for _, r := range t.layout.msgs {
		if line >= r.start && line <= r.end {
			return r.idx, true
		}
	}
	return 0, false
}
