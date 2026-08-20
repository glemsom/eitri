package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

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
	Mode        viewMode
	COTExpanded bool
	Now         time.Time
	Tools       []flowTool
	IsFocused   func(kind blockKind, msgIdx, toolIdx, fragIdx int) bool
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
	theme       Theme
	config      string
	width       int
	pulse       bool
	effort      string
	mode        viewMode
	cotExpanded bool
	now         time.Time
	tools       []flowTool
}

// RenderFlow renders one turn's event log as a single continuous merged flow:
// reasoning reads as a dimmed internal monologue, tool calls and results render
// inline at their arrival positions, and the answer lands at the tail. It
// returns the rendered text and the tool-entry row ranges in the same shape
// toolLog.Render produces, so the shared row->entry hit-test keeps working on
// the merged stream unchanged.
func RenderFlow(in flowInput) (string, []toolRowRange) {
	r := flowRenderer{
		theme:       in.Theme,
		config:      in.ConfigTheme,
		width:       in.Width,
		pulse:       in.Pulse,
		effort:      in.Effort,
		mode:        in.Mode,
		cotExpanded: in.COTExpanded,
		now:         in.Now,
		tools:       in.Tools,
	}
	items := r.fold(in.Events, in.Msg)
	return r.render(items, in.Msg, in.MsgIdx, in.IsFocused)
}

// fold turns the event log into its named flow blocks in emission order. The
// committed-turn vs live-turn difference is a parameter (msg.streaming): a live
// turn flushes each reasoning fragment at the tool boundary it precedes, while
// a committed turn folds its reasoning into one authoritative snapshot rendered
// exactly once, at the first tool boundary (or the tail). Answer fragments are
// flushed at every boundary too, so partial answers land where they streamed.
func (r flowRenderer) fold(events []TimelineEvent, msg message) []flowItem {
	items := []flowItem{}
	ti := 0
	var reasoning, answer strings.Builder
	reasoningEmitted := false
	reasoningFragIdx := 0
	anyStreamedAnswer := false
	emittedAnswerBeforeTail := false
	snapshotAnswerEmitted := false

	flushReasoning := func() {
		var txt string
		if msg.streaming {
			txt = reasoning.String() // the live delta fragment accumulated since the last boundary
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
			expanded: thinkingExpandedForFrag(msg, fragIdx, r.mode, r.cotExpanded),
		})
	}

	flushAnswer := func(final bool) {
		txt := answer.String()
		answer.Reset()
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
			return // nothing to show at this boundary
		}
		items = append(items, flowItem{kind: flowBlockAnswer, text: txt, final: final})
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
			te := flowTool{logIdx: -1}
			if ti < len(r.tools) {
				te = r.tools[ti]
			} else {
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
			// anchor is implied by arrival order), so the focus match uses 0 just
			// like the legacy tool renderer's focusedToolIdx.
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
// Both the FlowRenderer and the legacy non-flow path in renderHistory route
// through this one emitter, so the reasoning block's header/pane rendering
// cannot drift between them.
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
