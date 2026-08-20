package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/diff"
)

// toolEntry is one rendered tool call in the transcript: the tool name + args, plus the delivered result and its deterministic compression and file line-delta metadata.
type toolEntry struct {
	name         string
	args         string
	result       string
	bytesDropped int
	lines        int
	dropped      int
	compressed   bool
	added        int
	removed      int
	anchor       int // index of the triggering "you" message in messages
	complete     bool
	startedAt    time.Time
	doneAt       time.Time
	before       string
	after        string
	path         string
}

// toolLog is the deep value type that owns the transcript's tool-call entries end to end.
type toolLog struct {
	entries   []toolEntry
	curAnchor int
	// expansion owns every tool entry's per-block expansion force (keyed by the
	// entry's log index) and the open/collapsed decision, so the per-entry
	// expanded / force-collapse flags live behind the ExpansionState seam rather
	// than on each entry (issue #470).
	expansion ExpansionState
}

// Len reports the number of entries the log holds.
func (l toolLog) Len() int { return len(l.entries) }

// Entry returns a copy of the entry at index i.
func (l toolLog) Entry(i int) toolEntry { return l.entries[i] }

// SetAnchor records the message index new entries opened by Apply are anchored to (the "you" message of the turn currently running).
func (l *toolLog) SetAnchor(a int) { l.curAnchor = a }

// anchoredIndices returns the log entry indices anchored to the given message
// index, in append order — the order tool-start events arrive in a turn's
// event log, so the flat-flow renderer pairs each start event with its entry
// without re-deriving the pairing.
func (l toolLog) anchoredIndices(anchor int) []int {
	var out []int
	for i, e := range l.entries {
		if e.anchor == anchor {
			out = append(out, i)
		}
	}
	return out
}

// SetStart re-anchors a completed entry's observed execution start time.
func (l *toolLog) SetStart(i int, t time.Time) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.entries[i].startedAt = t
}

// Apply folds one tool-call observation into the log: a Start appends a fresh incomplete entry anchored to the current turn; a Result pairs back to the most recent not-yet-complete entry for that tool name and fills in its result/compression/line-delta metadata and marks it complete.
func (l *toolLog) Apply(u ToolUpdate) {
	if u.Start != nil {
		l.entries = append(l.entries, toolEntry{
			name:      u.Start.Name,
			args:      u.Start.Args,
			anchor:    l.curAnchor,
			startedAt: time.Now(),
		})
		return
	}
	if u.Result != nil {
		for i := len(l.entries) - 1; i >= 0; i-- {
			if l.entries[i].name == u.Result.Name && !l.entries[i].complete {
				l.entries[i].result = u.Result.Result
				l.entries[i].bytesDropped = u.Result.BytesDropped
				l.entries[i].lines = u.Result.Lines
				l.entries[i].dropped = u.Result.Dropped
				l.entries[i].compressed = u.Result.Compressed
				l.entries[i].added = u.Result.Added
				l.entries[i].removed = u.Result.Removed
				l.entries[i].before = u.Result.Before
				l.entries[i].after = u.Result.After
				l.entries[i].path = u.Result.Path
				l.entries[i].doneAt = time.Now()
				l.entries[i].complete = true
				return
			}
		}
	}
}

// Expand pins one entry force-expanded on the seam so the per-block toggle can
// reveal a single result even under a collapsing default.
func (l *toolLog) Expand(i int) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.expansion.set(blockTool, i, true)
}

// ForceCollapse pins one entry force-collapsed on the seam, beating the
// expanded-view mode, an expanded default, and any per-entry expanded flag.
func (l *toolLog) ForceCollapse(i int) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.expansion.set(blockTool, i, false)
}

// expandedFor returns whether entry i renders expanded, read through the
// ExpansionState seam: a per-block force always wins, the expand-all mode
// expands everything else, the collapse-all mode collapses everything else, and
// otherwise the entry follows its collapsed-by-default flag (issue #432). The
// seam's own mode and toolExpanded default are bound from the render call's
// params, mirroring how the transcript passes them today, so the decision lives
// in one place while the per-entry forces persist on l.expansion.
func (l toolLog) expandedFor(i int, mode viewMode, defaultCollapsed bool) bool {
	if i < 0 || i >= len(l.entries) {
		return false
	}
	e := l.expansion
	e.mode = mode
	e.toolExpanded = !defaultCollapsed
	return e.expanded(blockTool, i)
}

// Render renders every entry anchored to the given message into the shared head/text surface and records each rendered entry's content-row range. focusedIdx is the log index of the entry under the block focus, or -1 when none.
func (l toolLog) Render(th Theme, mode viewMode, defaultCollapsed bool, now time.Time, width, anchor int, pulse bool, focusedIdx int) (string, []toolRowRange) {
	var b strings.Builder
	var rows []toolRowRange
	nl := 0
	emit := func(s string) {
		b.WriteString(s)
		nl += strings.Count(s, "\n")
	}
	for ti, te := range l.entries {
		if te.anchor != anchor {
			continue
		}
		start := nl
		s := renderToolEntry(th, te, l.expandedFor(ti, mode, defaultCollapsed), now, width, pulse, ti == focusedIdx)
		rowsInEntry := strings.Count(s, "\n")
		emit(s)
		if rowsInEntry > 0 {
			rows = append(rows, toolRowRange{start: start, end: start + rowsInEntry - 1, idx: ti})
		}
	}
	return b.String(), rows
}

// PlainText renders every entry anchored to the given message as plain text for the clipboard transcript: the ⊕ tool head plus the indented full result when complete.
func (l toolLog) PlainText(anchor int) string {
	var b strings.Builder
	for _, te := range l.entries {
		if te.anchor != anchor {
			continue
		}
		b.WriteString(toolEntryHead(te))
		b.WriteString("\n")
		if te.complete && te.result != "" {
			b.WriteString("  " + strings.ReplaceAll(strings.TrimRight(te.result, "\n"), "\n", "\n  ") + "\n")
		}
	}
	return b.String()
}

// Review projects the changed-file review from the log's file-mutating (edit/write) entries: it consolidates by path, keeping the most recent state per path.
func (l toolLog) Review() []reviewEntry {
	var files []reviewEntry
	byPath := map[string]int{}
	for _, te := range l.entries {
		if te.name != "edit" && te.name != "write" {
			continue
		}
		if te.path == "" {
			continue
		}
		entry := reviewEntryFromTool(te)
		idx, ok := byPath[te.path]
		if !ok {
			byPath[te.path] = len(files)
			files = append(files, entry)
			continue
		}
		files[idx].before = entry.before
		files[idx].after = entry.after
		files[idx].added = entry.added
		files[idx].removed = entry.removed
	}
	return files
}

// AtLine maps a content-line coordinate to the tool entry that owns it via the shared layout pass. rows is the row-account already produced by Render (the log never re-derives layout separately), so the hit-test cannot drift from what the transcript renders.
func (l toolLog) AtLine(line int, rows []toolRowRange) (idx int, collapsed bool, ok bool) {
	for _, r := range rows {
		if line >= r.start && line <= r.end {
			if r.idx < len(l.entries) {
				// collapsed mirrors the seam's open/collapsed decision in the
				// default (collapsed-by-default) mode, matching the historical
				// read of the per-entry expanded flag the seam now owns.
				return r.idx, !l.expansion.expanded(blockTool, r.idx), true
			}
			return 0, false, false
		}
	}
	return 0, false, false
}

// toolEntryLabel renders the category-colored `⊕ tool` label part of the entry head.
func toolEntryLabel(te toolEntry) string {
	glyph := toolGlyph(te.name)
	return glyph + " " + te.name
}

// toolEntryArgs renders the dimmed detail part of the entry head: the display args hint, the invoked line range for range-limited reads (`⊕ read path:start-end`), and the line-delta tag for file-edit tools (`[+N, −M]`).
func toolEntryArgs(te toolEntry) string {
	s := ""
	if arg := toolArgsHint(te.args); arg != "" {
		s += "  " + arg
		if te.name == "read" {
			if r := readRangeHint(te.args); r != "" {
				s += ":" + r
			}
		}
	}
	if te.name == "edit" || te.name == "write" {
		s += "  " + deltaTag(te.added, te.removed)
	}
	return s
}

// toolEntryHead renders the compact one-line `⊕ tool args` head shared by the transcript entry and the clipboard copy: the tool name and display args, plus the [+N, −M] line-delta tag for file-edit tools.
func toolEntryHead(te toolEntry) string {
	return toolEntryLabel(te) + toolEntryArgs(te)
}

// readRangeHint extracts the explicit 1-based line range a `read` call was invoked with from its raw JSON args.
func readRangeHint(argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	start, ok := lineArg(args, "start_line")
	if !ok {
		return ""
	}
	end, ok := lineArg(args, "end_line")
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d-%d", start, end)
}

// lineArg reads a 1-based integer tool argument from raw JSON args.
func lineArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key].(float64)
	if !ok || v != math.Trunc(v) || v < 1 {
		return 0, false
	}
	return int(v), true
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
	budget := width - lipgloss.Width(label) - 8 // room for the outcome + timer
	if budget > 1 && lipgloss.Width(args) > budget {
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

	if te.name == "edit" || te.name == "write" {
		frame := cardFrame(th, te)
		entry := reviewEntryFromTool(te)
		if te.before == "" && te.after == "" {
			b.WriteString(frame.Render(strings.TrimRight(renderCountSummary(entry, th), "\n")))
		} else {
			b.WriteString(frame.Render(strings.TrimRight(renderToolCardDiff(entry, th), "\n")))
		}
		b.WriteString("\n")
		return b.String()
	}
	if te.result != "" {
		frame := cardFrame(th, te)
		b.WriteString(frame.Render(strings.TrimSuffix(te.result, "\n")))
		b.WriteString("\n")
	}
	return b.String()
}

// cardFrame is the expanded tool card's frame: a left border in the entry's category hue, shared by the result-dump and inline-diff content paths so both render with the same designed block look.
func cardFrame(th Theme, te toolEntry) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.Border{Left: g("│", "|")}).
		BorderLeft(true).
		PaddingLeft(1).
		BorderForeground(th.toolCategoryStyle(toolCategoryOf(te.name)).GetForeground())
}

// renderToolCardDiff renders a file-mutating entry's before→after content as an inline diff — the git-style @@ hunk headers plus +/-/context lines with word-level emphasis on modified pairs — so the expanded tool card shows the change instead of the raw result dump.
func renderToolCardDiff(f reviewEntry, th Theme) string {
	if h := diff.Diff(f.before, f.after); len(h) > 0 {
		f.hunks = h
		return renderDiff(f, th)
	}
	return renderCountSummary(f, th)
}

// deltaTag renders the conventional [+N, −M] add/delete vocabulary shared by the card diff body, the no-diff fallback, and the transcript's file-edit head, so the count formatting lives beside the log it renders for.
func deltaTag(added, removed int) string {
	return fmt.Sprintf("[+%d, "+g("−", "-")+"%d]", added, removed)
}

// isToolFailure reports whether a delivered tool result is error-shaped: the engine surfaces tool failures as plain-text result strings with these prefixes (internal/engine/engine.go), so the TUI can tag them ✗ without coupling to the engine package's error types.
func isToolFailure(result string) bool {
	return strings.HasPrefix(result, "error executing tool:") ||
		strings.HasPrefix(result, "invalid tool arguments:")
}

// toolCategory groups tool entries by the work the tool does so the transcript can colorize a long session by category: shell commands, file reads/writes/edits, web fetches and browser opens, and skill activations.
type toolCategory int

const (
	catOther toolCategory = iota
	catShell
	catFile
	catWeb
	catSkill
)

// toolCategoryOf maps a tool name to its transcript category.
func toolCategoryOf(name string) toolCategory {
	switch name {
	case "bash":
		return catShell
	case "read", "write", "edit":
		return catFile
	case "web_fetch", "open_in_browser":
		return catWeb
	case "skill":
		return catSkill
	}
	return catOther
}
