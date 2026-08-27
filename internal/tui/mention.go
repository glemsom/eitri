package tui

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// mentionCapRows caps how many mention candidates render above the composer so
// a dense workspace never pushes the band past a visible bound. The list scrolls
// within this window as the selection moves (see Move/Select).
const mentionCapRows = 8

type Mention struct {
	workspace string
	open      bool
	start     int
	partial   string
	cands     []string
	view      []string // the visible window into cands, capped at mentionCapRows
	offset    int      // cands index of view[0]
	idx       int      // absolute cands index of the selection

	// manifest caches the workspace's full path tree (dirs with a trailing "/")
	// for the current mention session. It is populated once, asynchronously off
	// the main loop, and reused while the dropdown stays open so each keystroke
	// only re-filters in memory instead of hitting disk.
	manifest []string
	walking  bool // a background manifest walk is in flight
}

func NewMention(workspace string) *Mention {
	return &Mention{workspace: workspace}
}

// Track rescans the mention state from the composer's current value and byte
// cursor: it opens the dropdown and rebuilds candidates when the caret sits at
// the tail of an `@` at a word boundary, otherwise it closes the dropdown.
// When the workspace manifest isn't cached yet it returns a background walk
// command that delivers candidates asynchronously so the UI never blocks.
func (mn *Mention) Track(value string, cursor int) tea.Cmd {
	start, partial, ok := atMentionAt(value, cursor)
	if !ok {
		mn.open = false
		return nil
	}
	if !mn.open || mn.partial != partial || mn.start != start {
		mn.open = true
		mn.start = start
		mn.partial = partial
		mn.idx = 0
		if len(mn.manifest) == 0 {
			if !mn.walking {
				mn.walking = true
				mn.cands = nil
				mn.recomputeView()
				return mentionWalkCmd(mn.workspace)
			}
			mn.recomputeView()
			return nil
		}
		mn.cands = candidatesForPartial(mn.manifest, partial)
	}
	if len(mn.cands) > 0 && mn.idx >= len(mn.cands) {
		mn.idx = len(mn.cands) - 1
	}
	mn.recomputeView()
	return nil
}

// setManifest installs the freshly walked workspace tree and re-filters the
// current partial in place, so the previously shown list swaps to the new
// results without a loading state or flicker.
func (mn *Mention) setManifest(paths []string) {
	mn.walking = false
	mn.manifest = paths
	if !mn.open {
		return
	}
	mn.cands = candidatesForPartial(mn.manifest, mn.partial)
	if len(mn.cands) > 0 && mn.idx >= len(mn.cands) {
		mn.idx = len(mn.cands) - 1
	}
	mn.recomputeView()
}

func (mn *Mention) Candidates() []string { return mn.cands }

func (mn *Mention) isOpen() bool { return mn.open }

func (mn *Mention) SelectedCandidate() string {
	if len(mn.cands) == 0 {
		return ""
	}
	return mn.cands[mn.idx]
}

// recomputeView derives the visible window (offset..offset+len) that keeps the
// selection on screen, so the dropdown scrolls past the cap as the highlight moves.
func (mn *Mention) recomputeView() {
	if !mn.open || len(mn.cands) == 0 {
		mn.view = nil
		mn.offset = 0
		return
	}
	if mn.idx < mn.offset {
		mn.offset = mn.idx
	}
	if mn.idx >= mn.offset+mentionCapRows {
		mn.offset = mn.idx - mentionCapRows + 1
	}
	end := mn.offset + mentionCapRows
	if end > len(mn.cands) {
		end = len(mn.cands)
	}
	mn.view = mn.cands[mn.offset:end]
}

func (mn *Mention) Move(delta int) {
	if len(mn.cands) == 0 {
		return
	}
	mn.idx += delta
	if mn.idx < 0 {
		mn.idx = len(mn.cands) - 1
	} else if mn.idx >= len(mn.cands) {
		mn.idx = 0
	}
	mn.recomputeView()
}

// Reset closes the dropdown and drops cached state, e.g. after a selection or submit.
func (mn *Mention) Reset() {
	mn.open = false
	mn.cands = nil
	mn.view = nil
	mn.partial = ""
	mn.start = 0
	mn.idx = 0
	mn.offset = 0
	mn.manifest = nil
	mn.walking = false
}

// CandidateCount returns how many mention rows render above the composer for the
// current dropdown window.
func (mn *Mention) CandidateCount() int {
	return len(mn.view)
}

// RenderCompletion appends the visible mention candidate window to the band,
// highlighting the selection.
func (mn *Mention) RenderCompletion(b *strings.Builder, th Theme) {
	if !mn.open {
		return
	}
	for i, c := range mn.view {
		if mn.offset+i == mn.idx {
			b.WriteString(th.slashSelectStyle.Render(g("▸ ", "> ") + c))
		} else {
			b.WriteString(th.statusStyle.Render("  " + c))
		}
		b.WriteString("\n")
	}
}

// Select applies the candidate: it replaces only the tracked `@partial` span in
// value with the candidate's bare path (the `@` stripped), preserving the rest
// of the draft and any other mentions. The boolean reports whether a selection
// was made.
func (mn *Mention) Select(value string) (string, bool) {
	if !mn.open || len(mn.cands) == 0 {
		return value, false
	}
	cand := mn.cands[mn.idx]
	bare := strings.TrimSuffix(cand, "/")
	end := mn.start + 1 + len(mn.partial)
	if end > len(value) {
		end = len(value)
	}
	out := value[:mn.start] + bare
	if end < len(value) {
		out += value[end:]
	}
	mn.Reset()
	return out, true
}

// mentionWalkMsg carries a freshly walked workspace manifest back from the
// background worker into the UI loop.
type mentionWalkMsg struct{ paths []string }

// mentionWalkCmd reads the workspace tree off the main loop and delivers the
// resulting manifest as a mentionWalkMsg, so a large workspace never blocks UI
// updates while candidates are gathered.
func mentionWalkCmd(dir string) tea.Cmd {
	return func() tea.Msg {
		return mentionWalkMsg{paths: walkWorkspace(dir)}
	}
}

// walkWorkspace returns the workspace-relative path tree in sorted order, dirs
// rendered with a trailing slash, skipping hidden entries and unreadable subtrees.
func walkWorkspace(dir string) []string {
	var paths []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == dir {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return nil
		}
		s := filepath.ToSlash(rel)
		if d.IsDir() {
			s += "/"
		}
		paths = append(paths, s)
		return nil
	})
	sort.Strings(paths)
	return paths
}

// candidatesForPartial filters the cached workspace manifest down to the paths
// matching an `@partial`: a partial naming an existing directory surfaces the
// paths under it, otherwise it matches sibling basenames within the parent
// directory. Folders keep their trailing slash so they read as directories.
// Candidates are sorted.
func candidatesForPartial(manifest []string, partial string) []string {
	dir, base := splitPath(partial)
	out := map[string]bool{}
	// match siblings under dir whose basename has the base prefix
	collectUnder(manifest, dir, base, out)
	// a partial that ends in a slash and names an existing directory surfaces its
	// children; a bare partial keeps matching siblings only until the slash is typed.
	if strings.HasSuffix(partial, "/") {
		if indent := strings.TrimSuffix(partial, "/") + "/"; containsPath(manifest, indent) {
			collectUnder(manifest, indent, "", out)
		}
	}
	res := make([]string, 0, len(out))
	for c := range out {
		res = append(res, c)
	}
	sort.Strings(res)
	return res
}

// collectUnder adds to out the immediate children of the manifest under prefix,
// its first segment surfaced literally (with a trailing slash when it is a
// directory). Segments whose compiled candidate starts with base are kept, so an
// empty base collects every child. Hidden segments are excluded.
func collectUnder(manifest []string, prefix, base string, out map[string]bool) {
	for _, e := range manifest {
		if !strings.HasPrefix(e, prefix) {
			continue
		}
		rest := e[len(prefix):]
		if rest == "" {
			continue
		}
		seg, isDir := firstSegment(rest)
		if strings.HasPrefix(seg, ".") {
			continue
		}
		if strings.HasPrefix(seg, base) {
			cand := prefix + seg
			if isDir {
				cand += "/"
			}
			out[cand] = true
		}
	}
}

// splitPath splits a partial into its leading directory (including a trailing
// slash, or "" at the root) and the remaining basename prefix.
func splitPath(partial string) (dir, base string) {
	if i := strings.LastIndex(partial, "/"); i >= 0 {
		return partial[:i+1], partial[i+1:]
	}
	return "", partial
}

// firstSegment returns the first path segment of s and whether it is a directory.
func firstSegment(s string) (string, bool) {
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return s[:i], true
	}
	return s, false
}

// containsPath reports whether manifest holds the exact path entry.
func containsPath(manifest []string, p string) bool {
	for _, e := range manifest {
		if e == p {
			return true
		}
	}
	return false
}

// atMentionAt inspects the composer value at a byte offset and reports whether
// the caret sits at the tail of an `@...` mention token that starts a new word
// boundary. It returns the byte offset of the `@`, the `@partial` beyond it,
// and whether the trigger should open the mention dropdown. An `@` appearing
// mid-word (e.g. inside an email address) is left literal: only an `@` at a
// line start or preceded by whitespace or an opening bracket/quote counts.
func atMentionAt(value string, cursor int) (start int, partial string, ok bool) {
	if cursor <= 0 || cursor > len(value) {
		return -1, "", false
	}
	// find the token head before the caret
	i := cursor - 1
	for i >= 0 {
		r, _ := utf8.DecodeLastRuneInString(value[:i+1])
		if r == '@' {
			start = i
			ok = true
			break
		}
		if isMentionBoundaryByte(value, i) {
			break
		}
		i--
	}
	if !ok {
		return -1, "", false
	}
	// the @ must follow a boundary or the line start
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(value[:start])
		if !isMentionBoundaryRune(r) {
			return -1, "", false
		}
	}
	partial = value[start+1 : cursor]
	return start, partial, ok
}

// isMentionBoundaryByte reports whether value[i] ends the run of characters
// that make up an @partial token. It must be called with i in-bounds.
func isMentionBoundaryByte(value string, i int) bool {
	r, _ := utf8.DecodeLastRuneInString(value[:i+1])
	return isMentionBoundaryRune(r)
}

// isMentionBoundaryRune reports whether r terminates an @partial token: any
// whitespace or a delimiter that announces the start of a new token.
func isMentionBoundaryRune(r rune) bool {
	return unicode.IsSpace(r) || r == '(' || r == '[' || r == '{' || r == '"' || r == '\''
}
