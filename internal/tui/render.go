package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// busyLine renders the in-progress working indicator: the animated braille spinner with the stage verb of the derived Phase (issues #363/#365) — Reasoning / Working / Answering — when motion is enabled, the static "… thinking" line otherwise.
func busyLine(idx int, p Phase) string {
	if !motionEnabled() || len(busySpinnerFrames) == 0 {
		return "… thinking"
	}
	return string(busySpinnerFrames[idx%len(busySpinnerFrames)]) + " " + phaseVerb(p)
}

// formatElapsed renders a duration in the tool-timer vocabulary (Codex-style): seconds under a minute, minutes+seconds under an hour, hours+minutes beyond.
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

// plural returns the English plural suffix for a count: "" for one, "s" otherwise ("1 line", "3 lines").
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// truncateWidth keeps the longest rune prefix of s whose display width is at most w (the caller appends the ellipsis).
func truncateWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	var sb strings.Builder
	cw := 0
	for _, r := range s {
		cw += ansi.StringWidth(string(r))
		if cw > w {
			break
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// lineCount reports how many rendered terminal rows a region string occupies, i.e. the number of newline-separated lines (a trailing newline does not add an extra row).
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

// tokenEstimate estimates a reasoning stream's token count from its assembled text length, using the conventional ~4 chars/token yardstick.
func tokenEstimate(s string) int {
	return len([]rune(s)) / 4
}

// idleWelcome renders the empty-transcript welcome block: the brand mark in the accent hue plus faint capability + keybinding hints, so the first launch reads as a designed surface.
func idleWelcome(th Theme) string {
	return th.headerStyle.Render(hr()) + "\n" +
		th.headerStyle.Render(brandMark()+" Eitri") + th.statusStyle.Render(g(" — ", " - ")+"your terminal coding agent") + "\n" +
		th.headerStyle.Render(hr()) + "\n" +
		th.statusStyle.Render("  "+promptHint()+" ask me to fix a bug, refactor code, explain a system, or run the tests") + "\n" +
		th.statusStyle.Render("  "+keyHint()+" ctrl+s settings · /help for commands & keybindings") + "\n"
}

// promptView renders the interactive max-turns continuation prompt.
func promptView(th Theme) string {
	return th.headerStyle.Render("run paused at the max-turns cap") + "\n\n" +
		"  Continue the run with more turns?\n" +
		"  " + th.statusStyle.Render("y") + " continue" + g(" · ", " . ") + th.statusStyle.Render("n") + " stop" + g(" · ", " . ") + th.statusStyle.Render("esc") + " cancel\n"
}

// newConfirmView renders the `/new` confirmation overlay (issue #613): the
// existing continuation-prompt overlay, re-worded to confirm a fresh session.
func newConfirmView(th Theme) string {
	return th.headerStyle.Render("start a fresh session") + "\n\n" +
		"  Clear this conversation and start fresh?\n" +
		"  " + th.statusStyle.Render("y") + " new session" + g(" · ", " . ") + th.statusStyle.Render("n") + " keep" + g(" · ", " . ") + th.statusStyle.Render("esc") + " cancel\n"
}

// thinkingHeader renders a turn's collapsible reasoning block header.
func thinkingHeader(th Theme, reasoning, effort string) string {
	hint := fmt.Sprintf("%s %s tok", g("🤔", "?"), formatTokens(tokenEstimate(reasoning)))
	if effort != "" {
		hint += g(" · ", " . ") + effort
	}
	return th.thinkingStyle.Render(hint) + "\n"
}

// bandHints returns the keybinding hint strip for the status row.
func bandHints() string {
	return strings.Join([]string{"ctrl+s settings", "ctrl+o copy", "e expand", "E collapse", "shift+enter newline"}, g(" · ", " . "))
}
