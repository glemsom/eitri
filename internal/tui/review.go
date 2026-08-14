package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/diff"
)

// diffAdd/diffDel give an added/removed line a distinct, conventional hue in the
// inline review diff (green for additions, red for removals) so changes read at
// a glance, mirroring the git/VS Code diff vocabulary (issue #90).
var (
	diffAdd = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	diffDel = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// reviewRegionMax caps the review overlay's own height-clipped region (issue
// T06 AC1): an expanded diff taller than this clips at the region boundary
// instead of overflowing the terminal and pushing the fixed bottom band
// (composer) off-screen. The cap keeps the changed-file header + list readable
// on large terminals while still leaving the transcript viewport visible below.
const reviewRegionMax = 12

// reviewEntry is one changed file surfaced in the review panel (issue #90): its
// host path, the before/after full content captured across the edit/write
// tool call (which the engine seam reported as pure telemetry, never affecting
// the run), the [+N,-M] line delta, the derived status, and the lazily computed
// inline diff hunks. Status is derived from content, not line counts alone, so
// a rewritten file reads as "modified" and an empty-before file as "added".
type reviewEntry struct {
	path    string
	before  string
	after   string
	status  string // "modified" | "added" | "deleted"
	added   int
	removed int
	hunks   []diff.Hunk
}

// reviewPanel is the changed-file review surface opened by ctrl+d over the
// transcript (issue #90). It lists every file the agent touched with per-file
// deltas and status, lets the user inspect a focused file's inline diff, and
// hands the file to the host browser/editor via the open_in_browser escape
// hatch. It is read-only against the repo and the live agent loop: it is built
// once from the already-delivered tool telemetry and never pauses or mutates
// the running session.
type reviewPanel struct {
	files    []reviewEntry
	cursor   int
	expanded bool
	openErr  string
}

// buildReview assembles the review panel from the conversation's accumulated
// file-mutating tool entries (issue #90). It dedupes by file path, keeping the
// most recent state for each, and derives each file's status + inline diff from
// the before/after content the engine captured. It never touches the repo or
// the live loop.
func (m Model) buildReview() reviewPanel {
	var files []reviewEntry
	byPath := map[string]int{}
	for _, te := range m.tools {
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
	return reviewPanel{files: files}
}

// updateReview routes a keypress while the review panel is open. It keeps the
// panel read-only: navigation and the open_in_browser escape hatch never modify
// the repo or the live run (issue #90 AC4).
func (m Model) updateReview(msgi tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msgi.String()
	if len(m.review.files) == 0 {
		// No files: esc / ctrl+d return to the transcript; everything else no-ops.
		if key == "esc" || key == "ctrl+d" {
			m.review = nil
		}
		return m, nil
	}
	switch key {
	case "esc", "ctrl+d":
		m.review = nil
	case "up":
		m.review.move(-1)
	case "down":
		m.review.move(1)
	case "enter", "tab", "i":
		// Toggle the focused file's inline diff in place (no alt-screen).
		if m.review.files[m.review.cursor].hunks == nil {
			m.review.computeHunks(m.review.cursor)
		}
		m.review.expanded = !m.review.expanded
	case "o":
		m.openFocused()
	}
	return m, nil
}

// move steps the review cursor within bounds, wrapping. When the panel is
// showing an inline diff, the newly focused file's hunks are (re)computed so
// the diff stays in sync with the cursor.
func (r *reviewPanel) move(steps int) {
	n := len(r.files)
	if n == 0 {
		return
	}
	r.cursor = (r.cursor + steps + n) % n
	if r.expanded && r.files[r.cursor].hunks == nil {
		r.computeHunks(r.cursor)
	}
}

// focused returns the changed file the cursor currently points at. Callers use
// it for the inline diff and the open_in_browser hatch so the cursor→entry
// navigation lives in one place instead of being re-derived at each call site.
func (r *reviewPanel) focused() reviewEntry {
	return r.files[r.cursor]
}

// computeHunks fills a focused file's inline diff from its before/after content
// using the pure-Go diff engine (issue #90 AC2), so nothing ships to an
// external renderer.
func (r *reviewPanel) computeHunks(idx int) {
	r.files[idx].hunks = diff.Diff(r.files[idx].before, r.files[idx].after)
}

// openFocused hands the focused changed file to the host browser/editor via the
// open_in_browser escape hatch (issue #90 AC3). It is a best-effort host-side
// launch; errors degrade to an on-panel note. Read-only against the live loop.
func (m *Model) openFocused() {
	if m.deps.OpenInBrowser == nil || m.review == nil || len(m.review.files) == 0 {
		return
	}
	if err := m.deps.OpenInBrowser(context.Background(), m.review.focused().path); err != nil {
		m.review.openErr = err.Error()
	}
}

// renderReview renders the review panel over the transcript: a dense,
// code-review-style summary of touched files with add/delete counts and status
// (the VS Code Agents-window lexicon), plus the focused file's inline diff when
// expanded, and a hint for the open_in_browser escape hatch.
func (m Model) renderReview(b *strings.Builder) {
	r := m.review
	if r == nil {
		return
	}
	fmt.Fprintf(b, "~ ctrl+d  Review changed files (%d) ~", len(r.files))
	b.WriteString("\n")
	if len(r.files) == 0 {
		b.WriteString(m.theme.statusStyle.Render("  no changes yet"))
		b.WriteString("\n")
		return
	}
	for i, f := range r.files {
		marker := " "
		if i == r.cursor {
			marker = "▶"
		}
		fmt.Fprintf(b, "%s %s  %s  %s", marker, f.path, deltaTag(f.added, f.removed), f.status)
		b.WriteString("\n")
	}
	if r.expanded && r.cursor < len(r.files) {
		f := r.files[r.cursor]
		b.WriteString(renderDiff(f, m.theme))
	}
	if r.openErr != "" {
		b.WriteString(m.theme.statusStyle.Render("open_in_browser: " + r.openErr))
		b.WriteString("\n")
		r.openErr = ""
	}
	b.WriteString(m.theme.statusStyle.Render("  enter: toggle diff · o: open_in_browser · ctrl+d: close"))
	b.WriteString("\n")
}

// renderDiff renders a changed file's inline hunks as a terminal diff with the
// git-style @@ header plus +/-/context lines, styled distinctly from the
// transcript. A file with no content-diff (e.g. a pure flag change the engine
// couldn't snapshot) falls back to the count summary.
func renderDiff(f reviewEntry, th Theme) string {
	if len(f.hunks) == 0 {
		return th.statusStyle.Render("  "+f.path+" "+deltaTag(f.added, f.removed)) + "\n"
	}
	var sb strings.Builder
	for _, h := range f.hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, l := range h.Lines {
			switch l.Type {
			case '+':
				sb.WriteString(diffAdd.Render("+" + l.Text))
			case '-':
				sb.WriteString(diffDel.Render("-" + l.Text))
			default:
				sb.WriteString(" " + l.Text)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// deltaTag renders the conventional [+N, −M] add/delete vocabulary shared by
// the review file list and the no-diff fallback, so the count formatting lives
// in one place.
func deltaTag(added, removed int) string {
	return fmt.Sprintf("[+%d, −%d]", added, removed)
}
