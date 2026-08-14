package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is the styling surface for the TUI chrome (issue #178): a palette
// registry of named colors plus the derived styles that draw from them. The
// default theme carries exactly the pre-seam T4 palette (issue #122), so the
// rendered surface is unchanged; a second palette is a new Theme value built
// from the same constructor pattern, and no consumer code changes because
// every chrome consumer renders through the model's theme field rather than
// package globals.
//
// Every color is a hex value: lipgloss adapts hex to the terminal's active
// color profile, so the surface degrades safely to ANSI-256 (or fewer) colors
// on a non-truecolor terminal (issue #122 AC5).
type Theme struct {
	// Palette entries.
	accent color.Color // the single agent accent used across the surface
	error  color.Color // semantic color for failures (⚠ errors, ✗ tool outcomes)
	ok     color.Color // semantic color for successful tool outcomes (✓)

	// Derived styles, drawn from the palette entries.
	headerStyle        lipgloss.Style // bold section header (settings title, prompts)
	statusStyle        lipgloss.Style // faint secondary text (strips, hints, tool lines)
	agentPaneStyle     lipgloss.Style // left-bordered pane framing assistant answers
	errorPaneStyle     lipgloss.Style // the same pane with the error-colored border
	thinkingStyle      lipgloss.Style // the 🤔 collapsed reasoning hint
	toolStyle          lipgloss.Style // the ⊕ tool-entry line
	outcomeOKStyle     lipgloss.Style // the ✓ tool-outcome tag
	outcomeErrStyle    lipgloss.Style // the ✗ tool-outcome tag
	slashSelectStyle   lipgloss.Style // the selected slash-completion candidate
	bandSeparatorStyle lipgloss.Style // the separator row framing the bottom band
}

// defaultTheme is the default (dark) theme: exactly the pre-seam palette and
// derived styles (issue #178 AC1). Every model starts here; alternate palettes
// are new Theme values on the same constructor pattern.
var defaultTheme = newDefaultTheme()

// newDefaultTheme builds the default theme: the T4 styling identity (issue
// #122) — a restrained dark palette with a single agent accent — as a palette
// registry plus the derived styles that draw from it. It is the only place hex
// values may live (issue #178 AC2: no hardcoded hex outside the palette
// registry).
func newDefaultTheme() Theme {
	return newTheme(
		lipgloss.Color("#7AA2F7"), // accent
		lipgloss.Color("#F7768E"), // error
		lipgloss.Color("#9ECE6A"), // ok
	)
}

// newDraculaTheme is the second curated chrome palette (issue #179 AC3): the
// canonical dracula hues — purple accent, red error, green ok — built on the
// same constructor pattern as the default, proving a new palette is a registry
// addition with no consumer change.
func newDraculaTheme() Theme {
	return newTheme(
		lipgloss.Color("#BD93F9"), // accent
		lipgloss.Color("#FF5555"), // error
		lipgloss.Color("#50FA7B"), // ok
	)
}

// themeFor maps a config theme name to its chrome palette (issue #179): the
// Markdown render theme selection now also selects the TUI chrome palette, so
// choosing a theme re-skins the whole surface, not just the Markdown body.
// "dracula" selects the second curated palette; every other supported render
// name (dark, light, tokyo-night, pink, notty, auto) keeps the default
// palette, and an unknown value falls back to default — exactly the
// renderer's fallback behavior (issue #179 AC4), so the chrome and Markdown
// never disagree about a theme.
func themeFor(name string) Theme {
	if name == "dracula" {
		return newDraculaTheme()
	}
	return defaultTheme
}

// newTheme builds a Theme from its three palette entries; the derived styles
// draw from them. It is the only place derived styles are constructed: every
// palette (default, dracula, future ones) shares the same style wiring, so
// palettes differ by hue alone and can never drift apart structurally.
func newTheme(accent, err, ok color.Color) Theme {
	return Theme{
		accent: accent,
		error:  err,
		ok:     ok,

		headerStyle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		statusStyle: lipgloss.NewStyle().Faint(true),
		// agentPaneStyle frames assistant answers as a left-bordered pane
		// (issue #122 AC1); errorPaneStyle is the same pane with the
		// error-colored border for failing turns so errors read as distinctly
		// as answers.
		agentPaneStyle: borderedPane(accent),
		errorPaneStyle: borderedPane(err),
		// thinkingStyle renders the 🤔 collapsed reasoning hint (issue #122
		// AC2); toolStyle renders the ⊕ tool-entry line.
		thinkingStyle: lipgloss.NewStyle().Faint(true).Foreground(accent),
		toolStyle:     lipgloss.NewStyle().Faint(true),
		// outcomeOKStyle / outcomeErrStyle render the ✓/✗ tool-outcome tags.
		outcomeOKStyle:  lipgloss.NewStyle().Foreground(ok),
		outcomeErrStyle: lipgloss.NewStyle().Foreground(err),
		// slashSelectStyle highlights the selected slash-completion candidate.
		slashSelectStyle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		// bandSeparatorStyle draws the separator row that frames the fixed
		// bottom band (status strip + slash completion + composer) as one
		// coherent region (issue #122 AC3). It is a plain separator line, not
		// a lipgloss border: a border pads every band line to the widest row,
		// which would re-pad the composer's own rows and break the band's
		// bottom-pinned layout.
		bandSeparatorStyle: lipgloss.NewStyle().Foreground(accent),
	}
}

// Historical package-level aliases of the default theme's palette and styles,
// kept under their original names so the existing styling tests keep asserting
// the default look unmodified (issue #178 AC3). New code reads through the
// model's theme field instead — these are the pre-seam globals, not the seam.
var (
	accentColor    = defaultTheme.accent
	errorColor     = defaultTheme.error
	okColor        = defaultTheme.ok
	agentPaneStyle = defaultTheme.agentPaneStyle
	errorPaneStyle = defaultTheme.errorPaneStyle
)

// borderedPane builds a left-bordered pane with the given border color — the
// shared frame for assistant answers (agent accent) and failing turns (error
// color), keeping the two pane styles from diverging.
func borderedPane(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		PaddingLeft(1).
		BorderForeground(c)
}

// isToolFailure reports whether a delivered tool result is error-shaped: the
// engine surfaces tool failures as plain-text result strings with these
// prefixes (internal/engine/engine.go), so the TUI can tag them ✗ without
// coupling to the engine package's error types.
func isToolFailure(result string) bool {
	return strings.HasPrefix(result, "error executing tool:") ||
		strings.HasPrefix(result, "invalid tool arguments:")
}
