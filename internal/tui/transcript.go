package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
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
	log toolLog
	// showToolResult expands all tool entries to their full result (issue #84).
	showToolResult bool
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
// AC1/AC2). T2 (issue #120) adds user navigation on the viewport.
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
