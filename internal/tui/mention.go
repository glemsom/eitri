package tui

import (
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
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
}

func NewMention(workspace string) *Mention {
	return &Mention{workspace: workspace}
}

// Track rescans the mention state from the composer's current value and byte
// cursor: it opens the dropdown and rebuilds candidates when the caret sits at
// the tail of an `@` at a word boundary, otherwise it closes the dropdown.
func (mn *Mention) Track(value string, cursor int) {
	start, partial, ok := atMentionAt(value, cursor)
	if !ok {
		mn.open = false
		return
	}
	if !mn.open || mn.partial != partial || mn.start != start {
		mn.open = true
		mn.start = start
		mn.partial = partial
		mn.cands = mentionCandidates(mn.workspace, partial)
		mn.idx = 0
	}
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

// mentionCandidates returns the workspace-relative path candidates under dir
// that share the given @partial prefix: top-level files and folders, folders
// rendered with a trailing slash. Deep completion into a subpath is out of
// scope for this ticket. Candidates are sorted.
func mentionCandidates(dir, partial string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var cands []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		s := name
		if e.IsDir() {
			s += "/"
		}
		if strings.HasPrefix(s, partial) {
			cands = append(cands, s)
		}
	}
	sort.Strings(cands)
	return cands
}
