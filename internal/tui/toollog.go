package tui

import (
	"strings"
	"time"
)

// toolLog is the deep value type that owns the transcript's tool-call entries
// end to end (issue #84, deepened in issue #208). It holds the ordered list of
// entries plus every operation on them — the start/result pairing, per-entry
// expansion, the render with content-row accounting, plain-text transcription,
// and the derived changed-file review projection. The Model keeps a single
// `log toolLog` field and delegates; nothing outside the log mutates entries.
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

// Apply folds one tool-call observation into the log (issue #208 US3): a Start
// appends a fresh incomplete entry anchored to the current turn; a Result pairs
// back to the most recent not-yet-complete entry for that tool name and fills
// in its result/compression/line-delta metadata and marks it complete. Nothing
// outside the log may mutate entries.
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

// Toggle flips one entry's expansion state with bounds checking (issue #208
// US7). It never touches other entries or the global alt+y flag.
func (l *toolLog) Toggle(i int) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.entries[i].expanded = !l.entries[i].expanded
}

// Render renders every entry anchored to the given message into the shared
// head/text surface and records each rendered entry's content-row range. The
// row ranges are relative to the block start (0-based) so the transcript can
// offset them by its running content-row count; they share one layout pass with
// the mouse hit-test so the two can never drift (issue #208 US6). showAll
// forces every entry open (the global alt+y flag, owned by the Model); a call
// passes `now` for the live elapsed readout while a turn runs (zero when idle).
func (l toolLog) Render(th Theme, showAll bool, now time.Time, width, anchor int) (string, []toolRowRange) {
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
		s := renderToolEntry(th, te, showAll || te.expanded, now, width)
		rowsInEntry := strings.Count(s, "\n")
		emit(s)
		if rowsInEntry > 0 {
			rows = append(rows, toolRowRange{start: start, end: start + rowsInEntry - 1, idx: ti})
		}
	}
	return b.String(), rows
}

// PlainText renders every entry anchored to the given message as plain text for
// the clipboard transcript (issue #123): the ⊕ tool head plus the indented full
// result when complete. ANSI-free.
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
// (edit/write) entries (issue #90, #208 US5): it consolidates by path, keeping
// the most recent state per path and classifying added/deleted/modified. This
// is the seed of the retrospective "review as a projection" hollowing.
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
		status := "modified"
		switch {
		case te.before == "" && te.after != "":
			status = "added"
		case te.before != "" && te.after == "":
			status = "deleted"
		}
		idx, ok := byPath[te.path]
		if !ok {
			byPath[te.path] = len(files)
			files = append(files, reviewEntry{
				path: te.path, before: te.before, after: te.after,
				status: status, added: te.added, removed: te.removed,
			})
			continue
		}
		// Most recent state for an already-listed file wins.
		files[idx].before = te.before
		files[idx].after = te.after
		files[idx].status = status
		files[idx].added = te.added
		files[idx].removed = te.removed
	}
	return files
}

// EntryAtLine maps a content-line coordinate to the tool entry that owns it via
// the rendered row ranges (issue #208 US6), and whether that entry is currently
// collapsed — a click on a collapsed head toggles it open, on an open entry it
// toggles closed. It is a pure lookup over rows already produced by Render.
func (l toolLog) EntryAtLine(line int, rows []toolRowRange) (idx int, collapsed bool, ok bool) {
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
