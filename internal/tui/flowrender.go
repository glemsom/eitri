package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

const collapsedToolCommandMaxLines = 5

// flowInput is one turn's complete rendering context for the flow renderer:
// its arrival-ordered event log, the assistant-message snapshot that carries
// the derived reasoning/answer text and the committed-vs-live stream state,
// the theme/view parameters, and any tool entries already paired to the turn's
// tool-start events. The caller (the Transcript) assembles these from its
// state; a unit test can build the same struct directly, with no Transcript or
// tool log scaffolding.
type flowInput struct {
	Events      []TimelineEvent
	Msg         message
	MsgIdx      int
	Theme       Theme
	ConfigTheme string
	Width       int
	Pulse       bool
	Effort      string
	// Cfg is the explicit expansion config bundle (mode plus the per-kind
	// collapsed-by-default flags) every reasoning open/collapsed decision in
	// this flow reads; the Transcript supplies its own bundle, tests theirs.
	Cfg       expansionConfig
	Now       time.Time
	Tools     []flowTool
	IsFocused func(kind blockKind, msgIdx, toolIdx, fragIdx int) bool
}

// flowTool is one tool entry the flow renderer emits: the log entry plus its
// expansion decision (computed by the caller from the view mode) and its log
// index (-1 for an entry synthesized from a bare start event with no log
// counterpart).
type flowTool struct {
	entry    toolEntry
	logIdx   int
	expanded bool
}

// flowItemKind discriminates one block of a turn's merged flow.
type flowItemKind int

const (
	flowBlockReasoning flowItemKind = iota
	flowBlockTool
	flowBlockAnswer
)

// flowItem is one block of a turn's merged flow in emission order, the unit the
// renderer folds the event log into. A reasoning item carries its fragment body
// and expansion decision; a tool item the pre-paired entry; an answer item the
// fragment text and whether it is the turn's final block (which drives the
// stopped marker).
type flowItem struct {
	kind     flowItemKind
	text     string
	fragIdx  int
	expanded bool
	tool     flowTool
	final    bool
}

// flowRenderer holds the rendering parameters shared by the fold and render
// passes, so they are threaded through as values rather than captured in each
// closure.
type flowRenderer struct {
	theme  Theme
	config string
	width  int
	pulse  bool
	effort string
	cfg    expansionConfig
	now    time.Time
	tools  []flowTool
}

// RenderFlow renders one turn's event log as a single continuous merged flow:
// reasoning reads as a dimmed internal monologue, tool calls and results render
// inline at their arrival positions, and the answer lands at the tail. It
// returns the rendered text and the tool-entry row ranges in the same shape
// toolLog.Render produces, so the shared row->entry hit-test keeps working on
// the merged stream unchanged.
func RenderFlow(in flowInput) (string, []toolRowRange) {
	r := flowRenderer{
		theme:  in.Theme,
		config: in.ConfigTheme,
		width:  in.Width,
		pulse:  in.Pulse,
		effort: in.Effort,
		cfg:    in.Cfg,
		now:    in.Now,
		tools:  in.Tools,
	}
	items := r.fold(in.Events, in.Msg)
	return r.render(items, in.Msg, in.MsgIdx, in.IsFocused)
}

// fold turns the event log into its named flow blocks in emission order. The
// committed-turn vs live-turn difference is a parameter (msg.streaming): a
// live turn paints each reasoning delta as it arrives — the user watches chain-
// of-thought progress during a pure-thinking stretch, no tool boundary needed —
// while a committed turn folds its reasoning into one authoritative snapshot
// rendered exactly once, at the first tool boundary (or the tail). Answer
// fragments are flushed at every boundary too, so partial answers land where
// they streamed.
func (r flowRenderer) fold(events []TimelineEvent, msg message) []flowItem {
	items := []flowItem{}
	ti := 0
	var reasoning, answer strings.Builder
	reasoningEmitted := false
	reasoningFragIdx := 0
	anyStreamedAnswer := false
	emittedAnswerBeforeTail := false
	snapshotAnswerEmitted := false
	emittedAnswerLen := 0 // answer text already flushed as delta fragments this turn

	flushReasoning := func() {
		var txt string
		if msg.streaming {
			txt = reasoning.String() // the live delta fragment accumulated since the last flush
			reasoning.Reset()
		} else {
			if reasoningEmitted {
				return // a committed turn's snapshot renders once
			}
			txt = msg.reasoning
			if txt == "" {
				txt = reasoning.String() // the live log is the fallback when no snapshot is carried
			}
			reasoning.Reset()
			reasoningEmitted = true
		}
		if txt == "" || !msg.thinkingRequested {
			return // nothing to show, or the thinking gate hides a turn that never asked
		}
		fragIdx := reasoningFragIdx
		reasoningFragIdx++
		items = append(items, flowItem{
			kind:     flowBlockReasoning,
			text:     txt,
			fragIdx:  fragIdx,
			expanded: thinkingExpandedForFrag(msg, fragIdx, r.cfg),
		})
	}

	flushAnswer := func(final bool) {
		txt := answer.String()
		answer.Reset()
		// Committed turns own an authoritative snapshot. When this is the tail
		// and the turn is done, whatever of the snapshot has not yet been
		// flushed wins: the full answer must survive even if its bytes never
		// streamed as deltas (e.g. the tail raced past busy=false and was
		// dropped), rather than vanishing behind an early tool-boundary frag.
		//
		// The emitted window only counts snapshot bytes actually rendered (a
		// stream fragment that is a true prefix of the committed content).
		// Interim narration deltas from earlier provider cycles are separate
		// blocks, not a prefix of the snapshot, so they must not shift the
		// window: blind-slicing content[emittedAnswerLen:] would otherwise cut
		// the start of the real answer (final.Answer holds only the final
		// provider cycle's text, not a concatenation of every delta).
		if final && !msg.streaming && msg.content != "" && emittedAnswerLen < len(msg.content) && len(txt) < len(msg.content[emittedAnswerLen:]) {
			txt = msg.content[emittedAnswerLen:]
		}
		switch {
		case txt != "":
			// An interleaved answer fragment sits before this boundary. A turn
			// that finalized with no answer yet split across a boundary prefers
			// the authoritative snapshot, which may be fuller than the last
			// un-emitted stream prefix.
			if final && !emittedAnswerBeforeTail && msg.content != "" {
				txt = msg.content
			}
		case final && !anyStreamedAnswer && !snapshotAnswerEmitted && !msg.streaming && msg.content != "":
			// The answer never streamed as deltas; the authoritative snapshot
			// survives (e.g. a non-streaming provider) and renders once.
			snapshotAnswerEmitted = true
			txt = msg.content
		default:
			return // nothing to stream at this boundary
		}
		if txt == "" {
			return
		}
		items = append(items, flowItem{kind: flowBlockAnswer, text: txt, final: final})
		// Advance the snapshot-output window only when this fragment is a true
		// prefix of the not-yet-emitted committed content. Narration deltas that
		// are not part of the snapshot (earlier provider cycles) are rendered as
		// their own block and must not consume the snapshot's budget, or the
		// tail reconciliation above would slice into the real answer.
		if !msg.streaming && msg.content != "" && emittedAnswerLen < len(msg.content) {
			remaining := msg.content[emittedAnswerLen:]
			if txt != "" && strings.HasPrefix(remaining, txt) {
				emittedAnswerLen += len(txt)
				if emittedAnswerLen > len(msg.content) {
					emittedAnswerLen = len(msg.content)
				}
			}
		}
		if !final && !emittedAnswerBeforeTail {
			emittedAnswerBeforeTail = true
		}
	}

	for _, ev := range events {
		switch ev.Kind {
		case EventReasoning:
			reasoning.WriteString(ev.Delta)
			if msg.streaming {
				flushReasoning() // a live turn paints each delta now; empty/gated flushes are no-ops
			}
		case EventToolStart:
			flushReasoning()
			flushAnswer(false)
			te := flowTool{logIdx: -1}
			if ti < len(r.tools) {
				te = r.tools[ti]
			} else if ev.Start != nil {
				// A start whose log entry is missing is synthesized from the
				// event so nothing in the stream silently drops.
				te.entry = toolEntry{name: ev.Start.Name, args: ev.Start.Args, startedAt: time.Now()}
			}
			ti++
			items = append(items, flowItem{kind: flowBlockTool, tool: te})
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

	return items
}

// render folds the named flow blocks into one rendered block, accounting the
// newlines and the tool-entry row ranges as it goes.
func (r flowRenderer) render(items []flowItem, msg message, msgIdx int, isFocused func(blockKind, int, int, int) bool) (string, []toolRowRange) {
	var b strings.Builder
	var rows []toolRowRange
	nl := 0
	emit := func(s string) {
		b.WriteString(s)
		nl += strings.Count(s, "\n")
	}
	for _, it := range items {
		switch it.kind {
		case flowBlockReasoning:
			emit(r.reasoningBlock(msg, msgIdx, it, isFocused))
		case flowBlockTool:
			// Tool blocks in collapsibleBlocks() always carry msgIdx 0 (their
			// anchor is implied by arrival order), so the focus match uses 0.
			// The FlowRenderer is the only tool-entry renderer.
			start := nl
			s := renderToolEntry(r.theme, it.tool.entry, it.tool.expanded, r.now, r.width, r.pulse, isFocused != nil && isFocused(blockTool, 0, it.tool.logIdx, 0))
			emit(s)
			if n := strings.Count(s, "\n"); n > 0 {
				rows = append(rows, toolRowRange{start: start, end: start + n - 1, idx: it.tool.logIdx})
			}
		case flowBlockAnswer:
			emit(r.answerBlock(msg, it))
		}
	}
	return b.String(), rows
}

// reasoningBlock renders one reasoning fragment through the single shared
// emitter: its header (with the focus marker when the fragment is focused),
// then the body only when expanded — collapsed, the hint is the whole block.
func (r flowRenderer) reasoningBlock(msg message, msgIdx int, it flowItem, isFocused func(blockKind, int, int, int) bool) string {
	focused := isFocused != nil && isFocused(blockReasoning, msgIdx, 0, it.fragIdx)
	return renderReasoningBlock(r.theme, r.config, r.width, r.effort, msg, msgIdx, it.fragIdx, it.text, it.expanded, focused)
}

// renderReasoningBlock renders one whole reasoning fragment as its header line
// (prefixed with the focus marker when focused) and, when expanded, the
// markdown-rendered body in the pane chosen from the message's stream state.
// The FlowRenderer routes through this one emitter, so the reasoning block's
// header/pane rendering has exactly one implementation.
func renderReasoningBlock(theme Theme, config string, width int, effort string, msg message, msgIdx, fragIdx int, text string, expanded, focused bool) string {
	var b strings.Builder
	h := thinkingHeader(theme, text, effort)
	if focused {
		h = theme.focusStyle.Render(focusMarker()+" ") + h
	}
	b.WriteString(h)
	if !expanded {
		return b.String() // collapsed: the hint is the block
	}
	md, _ := RenderMarkdown(text, width-2, config)
	pane := theme.thinkingPaneStyle
	if msg.streaming {
		pane = theme.streamingThinkingPaneStyle
	}
	pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
	b.WriteString(fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n"))))
	return b.String()
}

// answerBlock renders one answer fragment through the single shared emitter:
// the pane chosen from the message's flags, and the stopped marker when it is
// the turn's final block.
func (r flowRenderer) answerBlock(msg message, it flowItem) string {
	return renderAnswerBlock(r.theme, r.config, r.width, msg, it.text, it.final)
}

// renderAnswerBlock renders one answer fragment with the pane chosen from the
// message's flags (accent when done, stopped pane when stopped, error panes
// when the text reads a failure, streaming pane mid-stream) and the stopped
// marker when final. Both the FlowRenderer and the legacy non-flow path in
// renderHistory route through this one emitter, so the answer pane/stopped
// rendering cannot drift between them.
func renderAnswerBlock(theme Theme, config string, width int, msg message, text string, final bool) string {
	if text == "" {
		return ""
	}
	md, _ := RenderMarkdown(text, width-2, config)
	pane := theme.agentPaneStyle
	if msg.stopped {
		pane = theme.stoppedPaneStyle
	} else if strings.HasPrefix(text, failurePrefix()) {
		if msg.streaming {
			pane = theme.streamingErrorPaneStyle
		} else {
			pane = theme.errorPaneStyle
		}
	} else if msg.streaming {
		pane = theme.streamingPaneStyle
	}
	pane = pane.Border(lipgloss.Border{Left: g("│", "|")})
	s := fmt.Sprintf("%s\n", pane.Render(strings.TrimRight(md, "\n")))
	if final && msg.stopped {
		s += theme.statusStyle.Render(stoppedMarker()) + "\n"
	}
	return s
}

// toolEntryLabel renders the category-colored `⊕ tool` label part of the entry head.
func toolEntryLabel(te toolEntry) string {
	glyph := toolGlyph(te.name)
	return glyph + " " + te.name
}

// toolEntryArgs renders the dimmed detail part of the entry head: the display args hint.
func toolEntryArgs(te toolEntry) string {
	s := ""
	if arg := toolArgsHint(te.args); arg != "" {
		s += "  " + arg
	}
	return s
}

// toolEntryHead renders the compact one-line `⊕ tool args` head shared by the transcript entry and the clipboard copy: the tool name and display args.
func toolEntryHead(te toolEntry) string {
	return toolEntryLabel(te) + toolEntryArgs(te)
}

// toolArgsHint extracts a short display hint from a tool call's raw JSON args: the `path` for file tools, the `command` for bash, else the raw string trimmed to a single line.
func toolArgsHint(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		s := strings.TrimSpace(argsJSON)
		if s == "{}" {
			return ""
		}
		return s
	}
	for _, key := range []string{"path", "command", "url"} {
		if s, ok := args[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// renderToolEntry renders one tool-call entry as a compact, glanceable line — `⊕ tool args` — with the result collapsed by default to a summary, never a raw dump into the scroll. focused marks the entry as the currently focused block for the per-block expand interaction.
func renderToolEntry(th Theme, te toolEntry, expanded bool, now time.Time, width int, pulse bool, focused bool) string {
	var b strings.Builder
	outcome := ""
	if te.complete {
		if isToolFailure(te.result) {
			outcome = " " + th.outcomeErrStyle.Render(g("✗", "X"))
		} else {
			outcome = " " + th.outcomeOKStyle.Render(g("✓", "ok"))
		}
	}
	label := toolEntryLabel(te)
	args := toolEntryArgs(te)
	if !expanded {
		args = clampLines(args, collapsedToolCommandMaxLines)
	}
	budget := width - lipgloss.Width(label) - 8 // room for the outcome + timer
	if budget > 1 && !strings.Contains(args, "\n") && lipgloss.Width(args) > budget {
		args = truncateWidth(args, budget-1) + g("…", "...")
	}
	head := th.toolCategoryStyle(toolCategoryOf(te.name)).Render(label)
	if pulse && !te.complete {
		head = th.bandStatusStyle.Render(label)
	}
	if args != "" {
		head += th.statusStyle.Render(args)
	}
	if focused {
		head = th.focusStyle.Render(focusMarker()) + " " + head
	}
	b.WriteString(head + outcome)
	if !te.startedAt.IsZero() {
		var d time.Duration
		if te.complete && !te.doneAt.IsZero() {
			d = te.doneAt.Sub(te.startedAt)
		} else if !now.IsZero() {
			d = now.Sub(te.startedAt)
		}
		if d >= time.Second {
			b.WriteString(" " + th.statusStyle.Render(formatElapsed(d)))
		}
	}
	b.WriteString("\n")

	if !expanded {
		if te.lines > 0 || te.dropped > 0 || te.bytesDropped > 0 {
			summary := fmt.Sprintf("%d line%s", te.lines, plural(te.lines))
			hints := []string{}
			if te.dropped > 0 {
				hints = append(hints, fmt.Sprintf("+%d more", te.dropped))
			}
			if te.bytesDropped > 0 {
				hints = append(hints, fmt.Sprintf("+%d bytes truncated", te.bytesDropped))
			}
			if len(hints) > 0 {
				summary += " (" + strings.Join(hints, ", ") + ")"
			}
			b.WriteString(th.statusStyle.Render("  " + summary))
			b.WriteString("\n")
		}
		return b.String()
	}

	if te.result != "" {
		frame := cardFrame(th, te)
		b.WriteString(frame.Render(strings.TrimSuffix(te.result, "\n")))
		b.WriteString("\n")
	}
	return b.String()
}

// clampLines keeps the first max newline-separated rows of s, adding an ellipsis
// when hidden rows remain. It lets collapsed tool heads stay glanceable even for
// heredoc-heavy bash commands, while expanded cards keep the full command.
func clampLines(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[:max], "\n") + g("…", "...")
}

// cardFrame is the expanded tool card's frame: a left border in the entry's category hue, shared by the result-dump content.
func cardFrame(th Theme, te toolEntry) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.Border{Left: g("│", "|")}).
		BorderLeft(true).
		PaddingLeft(1).
		BorderForeground(th.toolCategoryStyle(toolCategoryOf(te.name)).GetForeground())
}

// isToolFailure reports whether a delivered tool result is error-shaped: the engine surfaces tool failures as plain-text result strings with these prefixes (internal/engine/engine.go), so the TUI can tag them ✗ without coupling to the engine package's error types.
func isToolFailure(result string) bool {
	return strings.HasPrefix(result, "error executing tool:") ||
		strings.HasPrefix(result, "invalid tool arguments:")
}

// toolCategory groups tool entries by the work the tool does so the transcript can colorize a long session by category: shell commands, web fetches and browser opens.
type toolCategory int

const (
	catOther toolCategory = iota
	catShell
	catWeb
)

// toolCategoryOf maps a tool name to its transcript category.
func toolCategoryOf(name string) toolCategory {
	switch name {
	case "bash":
		return catShell
	case "open_in_browser":
		return catWeb
	}
	return catOther
}
