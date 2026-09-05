package tui

import "time"

// toolEntry is one rendered tool call in the transcript: the tool name + args, plus the delivered result and its deterministic compression metadata.
type toolEntry struct {
	name         string
	args         string
	result       string
	bytesDropped int
	lines        int
	dropped      int
	compressed   bool
	anchor       int // index of the triggering "you" message in messages
	complete     bool
	startedAt    time.Time
	doneAt       time.Time
}

// toolLog is the deep value type that owns the transcript's tool-call entries end to end.
type toolLog struct {
	entries   []toolEntry
	curAnchor int
	// expansion owns every tool entry's per-block expansion force (keyed by the
	// entry's log index) and the open/collapsed decision, so the per-entry
	// expanded / force-collapse flags live behind the ExpansionState seam rather
	// than a per-entry flag.
	expansion ExpansionState
	// entryCache memoizes rendered completed tool entries keyed by log index;
	// see cachedToolRender. A pointer so the value-copied toolLog shares one
	// memo across render passes without copying it.
	entryCache *toolRenderCache
}

// cachedToolRender memoizes one completed tool entry's rendered text plus the
// surface parameters it was built for, so a later frame reuses it only when
// nothing it rendered from changed. Completed entries are immutable between
// frames (result, compression metadata and doneAt are fixed; only the live
// in-progress entry re-renders against the running clock), so a long busy turn
// re-renders its committed tool cards from cache instead of re-paying lipgloss
// Style.Render on every ~80ms spinner frame — the O(entries*width) hot path
// that makes a long tool-heavy live turn crawl.
type cachedToolRender struct {
	width    int
	expanded bool
	focused  bool
	text     string
}

// toolRenderCache is the memo map behind toolLog.entryCache; a map value is
// present only for completed entries. The caller owns invalidation on Apply
// (entry mutation / new entry) and clears on internal invalidation.
type toolRenderCache struct {
	m map[int]cachedToolRender
}

func (l *toolLog) initCache() {
	if l.entryCache == nil {
		l.entryCache = &toolRenderCache{}
	}
	if l.entryCache.m == nil {
		l.entryCache.m = map[int]cachedToolRender{}
	}
}

func (l toolLog) Len() int { return len(l.entries) }

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
		// A Start appends a fresh incomplete entry; the completed-entry memo is
		// left alone because toolLog entries are append-only, so earlier indexes
		// are stable and finished card rows stay cached across the turn. A fresh
		// entry is incomplete and renderEntry never caches incomplete entries, so
		// it needs no invalidation of its own.
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
				l.entries[i].doneAt = time.Now()
				l.entries[i].complete = true
				l.initCache()
				delete(l.entryCache.m, i) // the completed entry re-renders once with its filled result
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
	l.dropEntryCache(i)
}

// ForceCollapse pins one entry force-collapsed on the seam, beating the
// expanded-view mode, an expanded default, and any per-entry expanded flag.
func (l *toolLog) ForceCollapse(i int) {
	if i < 0 || i >= len(l.entries) {
		return
	}
	l.expansion.set(blockTool, i, false)
	l.dropEntryCache(i)
}

// expandedFor returns whether entry i renders expanded, read solely through
// the ExpansionState seam with the transcript's shared config bundle: a
// per-block force always wins, the expand-all mode expands everything else,
// the collapse-all mode collapses everything else, and otherwise the entry
// follows its collapsed-by-default flag. One config builder (the Transcript's
// expansionConfig) keeps every caller on the same values the renderer used.
func (l toolLog) expandedFor(i int, cfg expansionConfig) bool {
	if i < 0 || i >= len(l.entries) {
		return false
	}
	return l.expansion.expanded(blockTool, i, cfg)
}

// dropEntryCache discards the memoized render of entry i (and its dependents'
// indexes if the entry list shrank, which Apply handles separately).
func (l *toolLog) dropEntryCache(i int) {
	if l.entryCache == nil {
		return
	}
	delete(l.entryCache.m, i)
}

// cached returns the memoized render of completed entry i when it matches the
// current surface parameters, and whether one exists.
func (l *toolLog) cached(i, width int, expanded, focused bool) (string, bool) {
	if l.entryCache == nil {
		return "", false
	}
	c, ok := l.entryCache.m[i]
	if !ok {
		return "", false
	}
	if c.width != width || c.expanded != expanded || c.focused != focused || !l.entries[i].complete {
		return "", false
	}
	return c.text, true
}

// storeCache records the memoized render of completed entry i for the given
// surface parameters.
func (l *toolLog) storeCache(i, width int, expanded, focused bool, text string) {
	l.initCache()
	l.entryCache.m[i] = cachedToolRender{width: width, expanded: expanded, focused: focused, text: text}
}

// renderEntry renders entry i (or reuses its cache) using the given surface
// parameters. Completed entries are served from cache; in-progress entries are
// never cached and always render fresh against the running clock (pulse only
// affects the in-progress highlight, so it is not part of the completed-entry
// cache key). The focused flag participates in the cache key because the focus
// marker changes the bytes.
func (l *toolLog) renderEntry(i, width int, th Theme, expanded, focused, pulse bool) string {
	if l.entries[i].complete {
		if s, ok := l.cached(i, width, expanded, focused); ok {
			return s
		}
	}
	s := renderToolEntry(th, l.entries[i], expanded, time.Now(), width, pulse, focused)
	if l.entries[i].complete {
		l.storeCache(i, width, expanded, focused, s)
	}
	return s
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
