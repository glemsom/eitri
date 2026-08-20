package tui

import (
	"fmt"
	"strings"

	"github.com/glemsom/eitri/internal/diff"
)

// reviewEntry is a file-mutating tool entry's before/after projection: the host path, the before/after full content the engine captured across the edit/write call (pure telemetry, never affecting the run), and the [+N,-M] line delta.
type reviewEntry struct {
	path    string
	before  string
	after   string
	added   int
	removed int
	hunks   []diff.Hunk
}

// reviewEntryFromTool derives a review entry from a completed file-mutating tool entry's captured before/after/path content.
func reviewEntryFromTool(te toolEntry) reviewEntry {
	return reviewEntry{
		path: te.path, before: te.before, after: te.after,
		added: te.added, removed: te.removed,
	}
}

// renderDiff renders a changed file's inline hunks as a terminal diff with the git-style @@ header plus +/-/context lines, styled distinctly from the transcript through the theme's diff tokens (ok/error hue on a dimmed same-hue fill).
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

// renderCountSummary renders a changed file's [+N, −M] count-summary line — the no-diff fallback body for a path with no diffable content.
func renderCountSummary(f reviewEntry, th Theme) string {
	return th.statusStyle.Render("  "+f.path+" "+deltaTag(f.added, f.removed)) + "\n"
}
