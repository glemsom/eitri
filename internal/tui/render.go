package tui

import (
	"fmt"
	"strings"
	"time"
)

// render.go is the value-only rendering surface: the pure text derivations
// and formatters the transcript consumes. Every function here is a closed
// data-in → string-out mapping — it takes a value (a toolEntry, a
// time.Duration, an int, a string) and never a *Model — so the only way a
// render bug can arise is inside the helper, never in a call site's hidden
// coupling to live Model state.

// busyLine renders the in-progress working indicator: the animated braille
// spinner with the stage verb of the derived Phase (issues #363/#365) —
// Reasoning / Working / Answering — when motion is enabled, the static
// "… thinking" line otherwise. The verb maps off the derived Phase; the
// label stays plain so a monochrome terminal still reads it.
func busyLine(idx int, p Phase) string {
	if !motionEnabled() || len(busySpinnerFrames) == 0 {
		return "… thinking"
	}
	return string(busySpinnerFrames[idx%len(busySpinnerFrames)]) + " " + phaseVerb(p)
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
// reasoning cost at a glance.
func tokenEstimate(s string) int {
	return len([]rune(s)) / 4
}

// idleWelcome renders the empty-transcript welcome block: the brand mark in
// the accent hue plus faint capability + keybinding hints, so the first
// launch reads as a designed surface. One accent, no decoration — the
// restrained brand treatment, not a logo wall.
func idleWelcome(th Theme) string {
	return th.headerStyle.Render(hr()) + "\n" +
		th.headerStyle.Render(brandMark() + " Eitri") + th.statusStyle.Render(g(" — ", " - ")+"your terminal coding agent") + "\n" +
		th.headerStyle.Render(hr()) + "\n" +
		th.statusStyle.Render("  " + promptHint() + " ask me to fix a bug, refactor code, explain a system, or run the tests") + "\n" +
		th.statusStyle.Render("  " + keyHint() + " ctrl+s settings · /help for commands & keybindings") + "\n"
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
// ("🤔 1.4k tok · medium"); the block renders distinctly from the answer so
// reasoning is recognizable but secondary, and settles back to this hint when
// the turn's answer lands. reasoning is the accumulated thinking text; effort
// is the run's reasoning-effort tier (empty drops the suffix).
func thinkingHeader(th Theme, reasoning, effort string) string {
	hint := fmt.Sprintf("%s %s tok", g("🤔", "?"), formatTokens(tokenEstimate(reasoning)))
	if effort != "" {
		hint += g(" · ", " . ") + effort
	}
	return th.thinkingStyle.Render(hint) + "\n"
}

// bandHints returns the keybinding hint strip for the status row. Hints are
// the real, wired bindings — never advertised keys that no-op. It sits on the
// value-only render surface so the hint set stays table-testable without a live
// model; model.go's renderBand is the only *Model-free site.
func bandHints() string {
	return strings.Join([]string{"ctrl+s settings", "ctrl+o copy", "ctrl+e expand", "shift+enter newline"}, g(" · ", " . "))
}
