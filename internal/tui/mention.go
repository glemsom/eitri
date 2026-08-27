package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Mention struct {
	workspace string
	open      bool
	start     int
	partial   string
	cands     []string
	idx       int
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
	if mn.idx >= len(mn.cands) {
		mn.idx = 0
	}
}

func (mn *Mention) Candidates() []string { return mn.cands }

func (mn *Mention) isOpen() bool { return mn.open }

func (mn *Mention) SelectedCandidate() string {
	if len(mn.cands) == 0 {
		return ""
	}
	return mn.cands[mn.idx]
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
}

// Reset closes the dropdown and drops cached state, e.g. after a selection or submit.
func (mn *Mention) Reset() {
	mn.open = false
	mn.cands = nil
	mn.partial = ""
	mn.start = 0
	mn.idx = 0
}

// CandidateCount returns how many mention rows the composer value renders above the composer.
func (mn *Mention) CandidateCount(value string, cursor int) int {
	// count only while the dropdown is tracked open for the current caret position
	if !mn.open {
		return 0
	}
	return len(mn.cands)
}

// RenderCompletion appends the mention candidate list to the band, highlighting the selection.
func (mn *Mention) RenderCompletion(b *strings.Builder, th Theme, value string, cursor int) {
	if !mn.open {
		return
	}
	if len(mn.cands) == 0 {
		return
	}
	for i, c := range mn.cands {
		if i == mn.idx {
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
// rendered with a trailing slash. It descends into a directory named by the
// partial so `@src/fi` surfaces paths under `src/`. Candidates are sorted.
func mentionCandidates(dir, partial string) []string {
	base := dir
	// a partial naming a subpath (contains a separator) that resolves to a
	// directory surfaces the paths under it; a bare partial matches siblings at
	// the current level, so `@src` offers the `src/` folder itself rather than
	// stepping into it.
	if partial != "" && strings.Contains(partial, "/") {
		p := filepath.Join(dir, partial)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			base = p
			partial = ""
		}
	}
	var out []string
	entries, err := os.ReadDir(base)
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
	for _, c := range cands {
		out = append(out, filepath.ToSlash(c))
	}
	return out
}
