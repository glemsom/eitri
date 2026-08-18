package tui

import (
	"fmt"
	"strings"

	"github.com/glemsom/eitri/internal/diff"
)

// reviewEntry is a file-mutating tool entry's before/after projection: the
// host path, the before/after full content the engine captured across the
// edit/write call (pure telemetry, never affecting the run), and the [+N,-M]
// line delta.
//
// This record backs the Ctrl+E expanded card's inline diff, the no-diff
// fallback framing, and the tool log's Review projection.
type reviewEntry struct {
	path    string
	before  string
	after   string
	added   int
	removed int
	hunks   []diff.Hunk
}

// reviewEntryFromTool derives a review entry from a completed file-mutating
// tool entry's captured before/after/path content. It is shared by
// toolLog.Review() for its per-entry projection and by the expanded tool card's
// diff renderer, so the projection lives in one place.
func reviewEntryFromTool(te toolEntry) reviewEntry {
	return reviewEntry{
		path: te.path, before: te.before, after: te.after,
		added: te.added, removed: te.removed,
	}
}

// renderDiff renders a changed file's inline hunks as a terminal diff with the
// git-style @@ header plus +/-/context lines, styled distinctly from the
// transcript through the theme's diff tokens (ok/error hue on a dimmed
// same-hue fill). A file with no content-diff (e.g. a pure flag change the
// engine couldn't snapshot) falls back to the count summary.
//
// It is the card path's diff body: renderToolCardDiff calls it inside the
// expanded tool card's category-colored frame.
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

// renderCountSummary renders a changed file's [+N, −M] count-summary line —
// the no-diff fallback body for a path with no diffable content. It is shared
// by the expanded tool card's renderToolCardDiff and the diff engine wiring on
// the card path, so the no-diff fallback framing lives in one place.
func renderCountSummary(f reviewEntry, th Theme) string {
	return th.statusStyle.Render("  "+f.path+" "+deltaTag(f.added, f.removed)) + "\n"
}
