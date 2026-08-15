package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// render.go is the value-only rendering surface (issue #208, module 2): the
// pure text derivations and formatters the transcript consumes. Every function
// here is a closed data-in → string-out mapping — it takes a value (a toolEntry,
// a time.Duration, an int, a string) and never a *Model — so the only way a
// render bug can arise is inside the helper, never in a call site's hidden
// coupling to live Model state.

// busyLine renders the in-progress working indicator: the animated braille
// spinner with a "working" label when motion is enabled, the static "… thinking"
// line otherwise. The label stays plain so a monochrome terminal still reads it.
func busyLine(idx int) string {
	if !motionEnabled() || len(busySpinnerFrames) == 0 {
		return "… thinking"
	}
	return string(busySpinnerFrames[idx%len(busySpinnerFrames)]) + " working"
}

// renderToolEntry renders one tool-call entry as a compact, glanceable line —
// `⊕ tool  args` — with the result collapsed by default to a summary, never a
// raw dump into the scroll (issue #84). A file-mutating edit carries a [+N,-M]
// line-delta tag, and a compressed result carries an explicit "+N more" tail
// marker. When expanded (showToolResult), the full inline result is rendered so
// nothing is silently truncated — every collapse has an expand path.
func renderToolEntry(th Theme, te toolEntry, expanded bool, now time.Time, width int) string {
	var b strings.Builder
	// The ⊕ tool glyph is constant; a delivered result tags the entry with a
	// ✓/✗ outcome marker (issue #122 AC2) so success and failure are
	// glanceable without expanding the collapsed summary. The entry line
	// itself renders in the tool's category hue (shell/file/web/skill, issue
	// #181 AC1), with the glyph + color pair keeping meaning from ever
	// depending on color alone (issue #181 AC5).
	outcome := ""
	if te.complete {
		if isToolFailure(te.result) {
			outcome = " " + th.outcomeErrStyle.Render(g("✗", "X"))
		} else {
			outcome = " " + th.outcomeOKStyle.Render(g("✓", "ok"))
		}
	}
	// The entry head splits into the category-colored ⊕ tool label and the
	// dimmed command detail (args/range/delta): color marks the tool kind, the
	// detail recedes so a busy session reads calmly (benchmark §4.1 tool-cards:
	// label + dimmed path). Long details truncate to the pane width with an
	// ellipsis so a huge URL or command never cuts abruptly at the edge; the
	// full arguments stay in the clipboard copy and the expanded result.
	label := toolEntryLabel(te)
	args := toolEntryArgs(te)
	budget := width - lipgloss.Width(label) - 8 // room for the outcome + timer
	if budget > 1 && lipgloss.Width(args) > budget {
		args = truncateWidth(args, budget-1) + g("…", "...")
	}
	head := th.toolCategoryStyle(toolCategoryOf(te.name)).Render(label)
	if args != "" {
		head += th.statusStyle.Render(args)
	}
	b.WriteString(head + outcome)
	// Elapsed-time readout on the entry head (benchmark §4.1): sub-second tools
	// stay silent — only a tool worth waiting on earns a timer. Completed tools
	// freeze the span; a running tool (non-zero now, e.g. while the busy
	// spinner ticks) shows the live elapsed.
	if !te.startedAt.IsZero() {
		var d time.Duration
		if te.complete && !te.doneAt.IsZero() {
			d = te.doneAt.Sub(te.startedAt)
		} else if !now.IsZero() {
			d = now.Sub(te.startedAt)
		}
		if d >= time.Second {
			b.WriteString(" " + th.statusStyle.Render(formatElapsed(d)))
		}
	}
	b.WriteString("\n")

	if !expanded {
		// Collapsed summary: line count + explicit "+N more" tail marker when
		// the result was compressed (docs/spec.md §5). Never a raw dump.
		if te.lines > 0 || te.dropped > 0 {
			summary := fmt.Sprintf("%d line%s", te.lines, plural(te.lines))
			if te.compressed && te.dropped > 0 {
				summary += fmt.Sprintf(" (+%d more)", te.dropped)
			}
			b.WriteString(th.statusStyle.Render("  " + summary))
			b.WriteString("\n")
		}
		return b.String()
	}

	// Expanded: the full result framed as a card — a left border in the
	// entry's category hue with the content plain, so an expanded tool reads
	// as one designed block instead of a raw text dump (benchmark §4.1: tool
	// cards; the border color repeats the label's category color).
	if te.result != "" {
		frame := lipgloss.NewStyle().
			Border(lipgloss.Border{Left: g("│", "|")}).
			BorderLeft(true).
			PaddingLeft(1).
			BorderForeground(th.toolCategoryStyle(toolCategoryOf(te.name)).GetForeground())
		b.WriteString(frame.Render(strings.TrimSuffix(te.result, "\n")))
		b.WriteString("\n")
	}
	return b.String()
}

// formatElapsed renders a duration in the tool-timer vocabulary (Codex-style):
// seconds under a minute, minutes+seconds under an hour, hours+minutes beyond.
func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm %02ds", m, s%60)
	}
	return fmt.Sprintf("%dh %02dm", m/60, m%60)
}

// plural returns the English plural suffix for a count: "" for one, "s"
// otherwise ("1 line", "3 lines").
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncateWidth keeps the longest rune prefix of s whose display width is at
// most w (the caller appends the ellipsis). It is the width-aware truncation
// shared by the tool-entry args and any other fixed-width single-line detail.
func truncateWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var sb strings.Builder
	cw := 0
	for _, r := range s {
		if cw+1 > w {
			break
		}
		sb.WriteRune(r)
		cw++
	}
	return sb.String()
}

// clipReviewRegion keeps the first n rows of the rendered review region and
// discards the tail, so an over-height diff clips at the review region boundary
// (issue T06 AC1) instead of flowing over the history/band. A trailing newline
// is preserved so the region stays cleanly separated from the scroll region.
func clipReviewRegion(content string, n int) string {
	if n < 0 {
		n = 0
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if n < len(lines) {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n") + "\n"
}

// bottomSlice returns the bottom-anchored slice of the history content for a
// viewport of the given height — the fallback used when the model has no
// persisted viewport component (should not occur via NewModelCfg). It keeps the
// newest lines, dropping the head when the history overflows the viewport.
func bottomSlice(content string, vh int) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) <= vh {
		return content
	}
	if vh < 0 {
		vh = 0
	}
	return strings.Join(lines[len(lines)-vh:], "\n")
}

// lineCount reports how many rendered terminal rows a region string occupies,
// i.e. the number of newline-separated lines (a trailing newline does not add
// an extra row). It is used to compute how much of the terminal height the
// fixed bottom band consumes so the history viewport can clamp to the rest.
func lineCount(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if strings.HasSuffix(s, "\n") {
		n--
	}
	return n + 1
}

// tokenEstimate estimates a reasoning stream's token count from its assembled
// text length, using the conventional ~4 chars/token yardstick. It backs the
// collapsed thinking hint's token readout so the user can gauge the turn's
// reasoning cost at a glance (issue #85 AC2).
func tokenEstimate(s string) int {
	return len([]rune(s)) / 4
}

// idleWelcome renders the empty-transcript welcome block (issue #212): the
// brand mark in the accent hue plus faint capability + keybinding hints, so
// the first launch reads as a designed surface. One accent, no decoration —
// the restrained brand treatment, not a logo wall.
func idleWelcome(th Theme) string {
	return th.headerStyle.Render("Eitri") + th.statusStyle.Render(g(" — ", " - ")+"your terminal coding agent") + "\n" +
		th.statusStyle.Render("  ask me to fix a bug, refactor code, explain a system, or run the tests") + "\n" +
		th.statusStyle.Render("  ctrl+s settings · ctrl+b rail · / for commands") + "\n"
}

// promptView renders the interactive max-turns continuation prompt.
func promptView(th Theme) string {
	// The max-turns continuation decision, framed like the other overlays: an
	// accent title, the question, and the honest y/n/esc bindings from
	// updatePrompt.
	return th.headerStyle.Render("run paused at the max-turns cap") + "\n\n" +
		"  Continue the run with more turns?\n" +
		"  " + th.statusStyle.Render("y") + " continue" + g(" · ", " . ") + th.statusStyle.Render("n") + " stop" + g(" · ", " . ") + th.statusStyle.Render("esc") + " cancel\n"
}

// thinkingHeader renders a turn's collapsible reasoning block header. Collapsed
// it is a one-line hint carrying a token estimate and the reasoning-effort tier
// (issue #85 AC2: "🤔 1.4k tok · medium"); the block renders distinctly from the
// answer so reasoning is recognizable but secondary, and settles back to this
// hint when the turn's answer lands. reasoning is the accumulated thinking text;
// effort is the run's reasoning-effort tier (empty drops the suffix).
func thinkingHeader(th Theme, reasoning, effort string) string {
	hint := fmt.Sprintf("%s %s tok", g("🤔", "?"), formatTokens(tokenEstimate(reasoning)))
	if effort != "" {
		hint += g(" · ", " . ") + effort
	}
	return th.thinkingStyle.Render(hint) + "\n"
}

// bandHints returns the right-aligned keybinding hint strip for the status
// row (benchmark §4.4: one consistent hint system from the central keymap).
// Hints are the real, wired bindings — never advertised keys that no-op. It is
// a pure value-in → string-out function on the surface state that drives the
// hint set (vim mode, review panel open), never the Model itself.
func bandHints(vimNormal, reviewOpen bool) string {
	if vimNormal {
		return strings.Join([]string{"h j k l move", "w b word", "0 $ line", "i insert", "esc exit"}, g(" · ", " . "))
	}
	hints := []string{"ctrl+s settings", "ctrl+b rail", "ctrl+d review", "ctrl+o copy"}
	if reviewOpen {
		hints = []string{"enter diff", "o browser", "ctrl+d close"}
	}
	return strings.Join(hints, g(" · ", " . "))
}
