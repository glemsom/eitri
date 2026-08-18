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

// toolEntry is one rendered tool call in the transcript: the tool name + args,
// plus the delivered result and its deterministic compression and file
// line-delta metadata. It renders as a compact one-line `⊕ tool  args`
// summary that collapses the result by default and expands on demand to the
// full inline output (never silently truncated). The byte-cap split: result
// holds the FULL pre-cap string, with bytesDropped the bytes the cap dropped
// — the collapsed summary hints at bytesDropped while the expanded view always
// renders result. anchor is the index into messages of the "you" message whose
// turn this tool call belongs to, so View can interleave the entry
// chronologically after its triggering prompt. It is owned by toolLog.
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
	// startedAt/doneAt bound the tool's execution window for the elapsed-time
	// readout (benchmark §4.1: tool cards carry elapsed time). startedAt is set
	// when the tool begins, doneAt when its result lands; a running tool's live
	// elapsed re-renders while the busy spinner ticks.
	startedAt time.Time
	doneAt    time.Time
	// expanded is the per-entry expansion state toggled by a mouse click on the
	// entry's rows (click-to-expand, benchmark §4.4), independent of the global
	// Ctrl+E expanded-view mode. collapsedOverride is a per-entry collapse
	// that beats the global mode ON; the two are mutually exclusive (only one
	// is ever set by the Transcript's mode-aware toggle).
	expanded bool
	// collapsedOverride, when true, keeps this single entry collapsed even while
	// the Ctrl+E expanded-view mode is ON.
	collapsedOverride bool
	// before/after/path carry the file content and host path a file-mutating
	// edit/write captured: they back the expanded card's inline diff. Empty for
	// non-edit tools and batch runs.
	before string
	after  string
	path   string
}

// toolLog is the deep value type that owns the transcript's tool-call entries
// end to end. It holds the ordered list of entries plus every operation on them
// — the start/result pairing, per-entry expansion, the render with
// content-row accounting, plain-text transcription, and the derived
// changed-file projection. The Model keeps a single `log toolLog` field and
// delegates; nothing outside the log mutates entries.
type toolLog struct {
	entries []toolEntry
	// curAnchor is the message-anchor assigned to entries opened during the
	// current turn (SetAnchor is called on submit so new tool calls interleave
	// after that turn's prompting "you" message).
	curAnchor int
}

// Len reports the number of entries the log holds.
func (l toolLog) Len() int { return len(l.entries) }

// Entry returns a copy of the entry at index i.
func (l toolLog) Entry(i int) toolEntry { return l.entries[i] }

// SetAnchor records the message index new entries opened by Apply are anchored
// to (the "you" message of the turn currently running).
func (l *toolLog) SetAnchor(a int) { l.curAnchor = a }

// SetStart re-anchors a completed entry's observed execution start time. It is
// the log's owned operation for the elapsed readout: the pair's Start lands via
// Apply with the wall time, but a caller that observes a tool's real start (or
// a test seeding a deterministic window) can record it here. Bounds-checked.
func (l *toolLog) SetStart(i int, t time.Time) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.entries[i].startedAt = t
}

// Apply folds one tool-call observation into the log: a Start appends a fresh
// incomplete entry anchored to the current turn; a Result pairs back to the
// most recent not-yet-complete entry for that tool name and fills in its
// result/compression/line-delta metadata and marks it complete. Nothing outside
// the log may mutate entries.
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
		// Pair with the most recent not-yet-complete entry for this tool name.
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

// Toggle flips one entry's per-entry expansion state with bounds checking.
// It never touches other entries or the global Ctrl+E flag, and clears any
// collapse-override so the two mutually-exclusive per-entry states stay
// coherent.
func (l *toolLog) Toggle(i int) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.entries[i].expanded = !l.entries[i].expanded
	l.entries[i].collapsedOverride = false
}

// ToggleCollapse flips one entry's per-entry collapse-override, the mechanism
// that keeps a single entry collapsed while the global Ctrl+E expanded-view
// mode is ON. It is bounds-checked, never touches other entries, and clears
// any per-entry expanded so the two mutually-exclusive states stay coherent.
// The Transcript routes the expandAll-mode click here.
func (l *toolLog) ToggleCollapse(i int) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.entries[i].collapsedOverride = !l.entries[i].collapsedOverride
	l.entries[i].expanded = false
}

// expandedFor returns whether entry i renders expanded given the current global
// Ctrl+E expanded-view mode: a per-entry expanded state wins, a per-entry
// collapse-override beats the global mode ON, and otherwise the entry reflects
// the global flag. It is the single effective-expansion computation both
// Render and the Transcript's toolEntryAtLine hit-test consult, so the
// rendered rows and the click-to-collapse state never disagree.
func (l *toolLog) expandedFor(i int, expandAll bool) bool {
	if i < 0 || i >= len(l.entries) {
		return false
	}
	e := l.entries[i]
	if e.collapsedOverride {
		// A per-entry collapse-override forces this single entry collapsed even
		// while the global mode is ON. The per-entry expanded flag is always
		// cleared when the override is set by ToggleCollapse, so collapsing the
		// override wins deterministically with no dependence on that invariant.
		return false
	}
	return e.expanded || expandAll
}

// Render renders every entry anchored to the given message into the shared
// head/text surface and records each rendered entry's content-row range. The
// row ranges are relative to the block start (0-based) so the transcript can
// offset them by its running content-row count; they share one layout pass with
// the mouse hit-test so the two can never drift. expandAll reflects the
// persistent Ctrl+E expanded-view mode: with the mode on every entry renders
// its full result unless a per-entry collapse override beats it, so past and
// newly delivered entries alike respect the mode at render time. A call passes
// `now` for the live elapsed readout while a turn runs (zero when idle).
func (l toolLog) Render(th Theme, expandAll bool, now time.Time, width, anchor int) (string, []toolRowRange) {
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
		s := renderToolEntry(th, te, l.expandedFor(ti, expandAll), now, width)
		rowsInEntry := strings.Count(s, "\n")
		emit(s)
		if rowsInEntry > 0 {
			rows = append(rows, toolRowRange{start: start, end: start + rowsInEntry - 1, idx: ti})
		}
	}
	return b.String(), rows
}

// PlainText renders every entry anchored to the given message as plain text for
// the clipboard transcript: the ⊕ tool head plus the indented full result when
// complete. ANSI-free.
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

// Review projects the changed-file review from the log's file-mutating
// (edit/write) entries: it consolidates by path, keeping the most recent state
// per path. This is the seed of the retrospective "review as a projection"
// hollowing.
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
		// Most recent state for an already-listed file wins.
		files[idx].before = entry.before
		files[idx].after = entry.after
		files[idx].added = entry.added
		files[idx].removed = entry.removed
	}
	return files
}

// AtLine maps a content-line coordinate to the tool entry that owns it via
// the shared layout pass. rows is the row-account already produced by Render
// (the log never re-derives layout separately), so the hit-test cannot drift
// from what the transcript renders. It is a pure lookup over those rows. The
// returned collapsed reflects the RAW per-entry expanded flag only; callers
// that need the effective rendered state under the Ctrl+E expanded-view mode
// should combine the index with expandedFor (as Transcript.toolEntryAtLine
// does), since the per-entry state is orthogonal to the global mode.
func (l toolLog) AtLine(line int, rows []toolRowRange) (idx int, collapsed bool, ok bool) {
	for _, r := range rows {
		if line >= r.start && line <= r.end {
			if r.idx < len(l.entries) {
				return r.idx, !l.entries[r.idx].expanded, true
			}
			return 0, false, false
		}
	}
	return 0, false, false
}

// toolEntryLabel renders the category-colored `⊕ tool` label part of the
// entry head.
func toolEntryLabel(te toolEntry) string {
	glyph := toolGlyph(te.name)
	return glyph + " " + te.name
}

// toolEntryArgs renders the dimmed detail part of the entry head: the display
// args hint, the invoked line range for range-limited reads (`⊕ read
// path:start-end`), and the line-delta tag for file-edit tools (`[+N, −M]`).
// Split from the label so the transcript can color the tool name and dim the
// command detail.
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

// toolEntryHead renders the compact one-line `⊕ tool  args` head shared by the
// transcript entry and the clipboard copy: the tool name and display args, plus
// the [+N, −M] line-delta tag for file-edit tools.
func toolEntryHead(te toolEntry) string {
	return toolEntryLabel(te) + toolEntryArgs(te)
}

// readRangeHint extracts the explicit 1-based line range a `read` call was
// invoked with from its raw JSON args. Both start_line and end_line must be
// present as positive integers; omitted or null limits (whole-file reads),
// fractional values, and malformed shapes return "" so the entry head falls
// back to the path-only rendering — never a crash.
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

// lineArg reads a 1-based integer tool argument from raw JSON args. It reports
// ok=false when the arg is absent, null, non-numeric, fractional, or
// non-positive, so range parsing can never emit a bogus tag from an unexpected
// argument shape.
func lineArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key].(float64)
	if !ok || v != math.Trunc(v) || v < 1 {
		return 0, false
	}
	return int(v), true
}

// toolArgsHint extracts a short display hint from a tool call's raw JSON args:
// the `path` for file tools, the `command` for bash, else the raw string
// trimmed to a single line. It keeps the one-line entry glanceable and never
// throws away the model's full arguments (those stay in the engine transcript).
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

// renderToolEntry renders one tool-call entry as a compact, glanceable line —
// `⊕ tool  args` — with the result collapsed by default to a summary, never a
// raw dump into the scroll. A file-mutating edit carries a [+N,-M] line-delta
// tag, a compressed result carries an explicit "+N more" tail marker, and a
// byte-capped delivery carries a "(+N bytes truncated)" hint. When expanded
// (the Ctrl+E expanded-view mode or a per-entry open), the FULL inline result
// is rendered (the entry's pre-cap result, never the capped delivered form) so
// nothing is silently truncated — every collapse has an expand path. It is the
// per-entry renderer the log's Render pass runs, so the transcript and the
// row-account/hit-test share one layout.
func renderToolEntry(th Theme, te toolEntry, expanded bool, now time.Time, width int) string {
	var b strings.Builder
	// The ⊕ tool glyph is constant; a delivered result tags the entry with a
	// ✓/✗ outcome marker so success and failure are glanceable without
	// expanding the collapsed summary. The entry line itself renders in the
	// tool's category hue (shell/file/web/skill), with the glyph + color pair
	// keeping meaning from ever depending on color alone.
	outcome := ""
	if te.complete {
		if isToolFailure(te.result) {
			outcome = " " + th.outcomeErrStyle.Render(g("✗", "X"))
		} else {
			outcome = " " + th.outcomeOKStyle.Render(g("✓", "ok"))
		}
	}
	// The entry head splits into the category-colored ⊕ tool label and the
	// dimmed command detail (args/range/delta): color marks the tool kind, the
	// detail recedes so a busy session reads calmly (benchmark §4.1 tool-cards:
	// label + dimmed path). Long details truncate to the pane width with an
	// ellipsis so a huge URL or command never cuts abruptly at the edge; the
	// full arguments stay in the clipboard copy and the expanded result.
	label := toolEntryLabel(te)
	args := toolEntryArgs(te)
	budget := width - lipgloss.Width(label) - 8 // room for the outcome + timer
	if budget > 1 && lipgloss.Width(args) > budget {
		args = truncateWidth(args, budget-1) + g("…", "...")
	}
	head := th.toolCategoryStyle(toolCategoryOf(te.name)).Render(label)
	if args != "" {
		head += th.statusStyle.Render(args)
	}
	b.WriteString(head + outcome)
	// Elapsed-time readout on the entry head (benchmark §4.1): sub-second tools
	// stay silent — only a tool worth waiting on earns a timer. Completed tools
	// freeze the span; a running tool (non-zero now, e.g. while the busy
	// spinner ticks) shows the live elapsed.
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
		// Collapsed summary: line count + explicit "+N more" tail marker when
		// the result was compressed, and a "(+N bytes truncated)" hint when the
		// delivered form was byte-capped — never a raw dump, never a silent cap.
		// Both hints merge when line and byte truncation both happened, mirroring
		// the merged marker line the model sees.
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

	// Expanded: the full result framed as a card — a left border in the
	// entry's category hue with the content plain, so an expanded tool reads
	// as one designed block instead of a raw text dump. A file-mutating
	// edit/write whose before/after snapshot the engine captured renders its
	// inline diff instead of the result dump: the same pure-Go diff engine +
	// word emphasis, inside the card's frame. An edit/write with no captured
	// content
	// falls back to the [+N, −M] count summary (never the raw dump), matching
	// the projection's no-diff handling.
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

// cardFrame is the expanded tool card's frame: a left border in the entry's
// category hue, shared by the result-dump and inline-diff content paths so
// both render with the same designed block look.
func cardFrame(th Theme, te toolEntry) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.Border{Left: g("│", "|")}).
		BorderLeft(true).
		PaddingLeft(1).
		BorderForeground(th.toolCategoryStyle(toolCategoryOf(te.name)).GetForeground())
}

// renderToolCardDiff renders a file-mutating entry's before→after content as
// an inline diff — the git-style @@ hunk headers plus +/-/context lines with
// word-level emphasis on modified pairs — so the expanded tool card shows the
// change instead of the raw result dump. A path with no diffable content (the
// engine couldn't snapshot it) falls back to the count summary, matching the
// projection.
func renderToolCardDiff(f reviewEntry, th Theme) string {
	if h := diff.Diff(f.before, f.after); len(h) > 0 {
		f.hunks = h
		return renderDiff(f, th)
	}
	return renderCountSummary(f, th)
}

// deltaTag renders the conventional [+N, −M] add/delete vocabulary shared by
// the card diff body, the no-diff fallback, and the transcript's file-edit
// head, so the count formatting lives beside the log it renders for.
func deltaTag(added, removed int) string {
	return fmt.Sprintf("[+%d, "+g("−", "-")+"%d]", added, removed)
}

// isToolFailure reports whether a delivered tool result is error-shaped: the
// engine surfaces tool failures as plain-text result strings with these
// prefixes (internal/engine/engine.go), so the TUI can tag them ✗ without
// coupling to the engine package's error types.
func isToolFailure(result string) bool {
	return strings.HasPrefix(result, "error executing tool:") ||
		strings.HasPrefix(result, "invalid tool arguments:")
}

// toolCategory groups tool entries by the work the tool does so the transcript
// can colorize a long session by category: shell commands, file
// reads/writes/edits, web fetches and browser opens, and skill activations.
// Tools no category recognizes fall back to the generic faint entry — color is
// a layer on top of the persistent ⊕ glyph, never the only signal.
type toolCategory int

const (
	catOther toolCategory = iota
	catShell
	catFile
	catWeb
	catSkill
)

// toolCategoryOf maps a tool name to its transcript category. Unknown names
// (future tools) report catOther so they keep the generic faint tool line
// instead of inventing a hue.
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
