package tui

import "time"

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
// decision is pure: the config bundle is built from the render call's params,
// so the hit-test can pass the same bundle and never disagree with what was
// rendered.
func (l toolLog) expandedFor(i int, mode viewMode, defaultCollapsed bool) bool {
	if i < 0 || i >= len(l.entries) {
		return false
	}
	return l.expansion.expanded(blockTool, i, expansionConfig{mode: mode, toolExpanded: !defaultCollapsed})
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
func (l toolLog) AtLine(line int, rows []toolRowRange, cfg expansionConfig) (idx int, collapsed bool, ok bool) {
	for _, r := range rows {
		if line >= r.start && line <= r.end {
			if r.idx < len(l.entries) {
				// collapsed mirrors the exact open/collapsed decision the render
				// made, via the same config bundle the caller rendered with.
				return r.idx, !l.expansion.expanded(blockTool, r.idx, cfg), true
			}
			return 0, false, false
		}
	}
	return 0, false, false
}
