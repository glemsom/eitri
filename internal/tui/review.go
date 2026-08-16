package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/diff"
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

// updateReview routes a keypress while the review panel is open. It keeps the
// panel read-only: navigation and the open_in_browser escape hatch never modify
// the repo or the live run (issue #90 AC4). The open panel lives on the owned
// Transcript surface (issue #246/#248).
func (m Model) updateReview(msgi tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msgi.String()
	if len(m.tx.review.files) == 0 {
		// No files: esc / ctrl+d return to the transcript; everything else no-ops.
		if key == "esc" || key == "ctrl+d" {
			m.tx.review = nil
			m.syncComposerRail()
		}
		return m, nil
	}
	switch key {
	case "esc", "ctrl+d":
		m.tx.review = nil
		m.syncComposerRail()
	case "up":
		m.tx.review.move(-1)
	case "down":
		m.tx.review.move(1)
	case "enter", "tab", "i":
		// Toggle the focused file's inline diff in place (no alt-screen).
		if m.tx.review.files[m.tx.review.cursor].hunks == nil {
			m.tx.review.computeHunks(m.tx.review.cursor)
		}
		m.tx.review.expanded = !m.tx.review.expanded
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

// reviewEntryFromTool derives a review entry from a completed file-mutating
// tool entry's captured before/after/path content (issue #90): the status
// (modified/added/deleted) is derived here from content, not line counts, and
// toolLog.Review() reuses this helper for its per-entry projection so status
// derivation lives in one place. The expanded tool card's diff renderer (issue
// #275) builds on the same projection the review panel lists.
func reviewEntryFromTool(te toolEntry) reviewEntry {
	status := "modified"
	switch {
	case te.before == "" && te.after != "":
		status = "added"
	case te.before != "" && te.after == "":
		status = "deleted"
	}
	return reviewEntry{
		path: te.path, before: te.before, after: te.after,
		status: status, added: te.added, removed: te.removed,
	}
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
	if m.deps.OpenInBrowser == nil || m.tx.review == nil || len(m.tx.review.files) == 0 {
		return
	}
	if err := m.deps.OpenInBrowser(context.Background(), m.tx.review.focused().path); err != nil {
		m.tx.review.openErr = err.Error()
	}
}

// renderDiff renders a changed file's inline hunks as a terminal diff with the
// git-style @@ header plus +/-/context lines, styled distinctly from the
// transcript through the theme's diff tokens (ok/error hue on a dimmed
// same-hue fill). A file with no content-diff (e.g. a pure flag change the
// engine couldn't snapshot) falls back to the count summary.
func renderDiff(f reviewEntry, th Theme) string {
	if len(f.hunks) == 0 {
		return renderCountSummary(f, th)
	}
	var sb strings.Builder
	for _, h := range f.hunks {
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for i := 0; i < len(h.Lines); i++ {
			l := h.Lines[i]
			switch l.Type {
			case '+':
				sb.WriteString(th.diffAddStyle.Render("+" + l.Text))
			case '-':
				// A removed line followed by an added line is the paired half of a
				// modification: render both on their own rows with word-level
				// emphasis (bold on the changed words). Standalone additions and
				// removals render whole-line.
				if i+1 < len(h.Lines) && h.Lines[i+1].Type == '+' {
					oldToks, newToks := wordDiff(l.Text, h.Lines[i+1].Text)
					sb.WriteString(renderWordDiff(oldToks, th.diffDelStyle, "-"))
					sb.WriteString("\n")
					sb.WriteString(renderWordDiff(newToks, th.diffAddStyle, "+"))
					i++ // consume the paired addition
				} else {
					sb.WriteString(th.diffDelStyle.Render("-" + l.Text))
				}
			default:
				sb.WriteString(" " + l.Text)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderCountSummary renders a changed file's [+N, −M] count-summary line — the
// fallback body for a path with no diffable content. It is shared by the review
// panel's renderDiff and the expanded tool card's renderToolCardDiff (issue
// #275) so the no-diff fallback framing lives in one place.
func renderCountSummary(f reviewEntry, th Theme) string {
	return th.statusStyle.Render("  "+f.path+" "+deltaTag(f.added, f.removed)) + "\n"
}
