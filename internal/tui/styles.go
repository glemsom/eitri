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
	shell  color.Color // semantic color for shell tool entries (bash, ⊕)
	file   color.Color // semantic color for file tool entries (read/write/edit, ⊕)
	web    color.Color // semantic color for web tool entries (web_fetch, ⊕)
	skill  color.Color // semantic color for skill tool entries (skill, ⊕)

	// Derived styles, drawn from the palette entries.
	headerStyle        lipgloss.Style // bold section header (settings title, prompts)
	statusStyle        lipgloss.Style // faint secondary text (strips, hints, tool lines)
	agentPaneStyle     lipgloss.Style // left-bordered pane framing assistant answers
	errorPaneStyle     lipgloss.Style // the same pane with the error-colored border
	thinkingStyle      lipgloss.Style // the 🤔 collapsed reasoning hint
	toolStyle          lipgloss.Style // the ⊕ tool-entry line (uncategorized fallback)
	toolShellStyle     lipgloss.Style // the ⊕ tool-entry line, shell category
	toolFileStyle      lipgloss.Style // the ⊕ tool-entry line, file category
	toolWebStyle       lipgloss.Style // the ⊕ tool-entry line, web category
	toolSkillStyle     lipgloss.Style // the ⊕ tool-entry line, skill category
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
// registry plus the derived styles that draw from it. Palette constructors are
// the only place hex values may live (issue #178 AC2: no hardcoded hex outside
// the palette registry).
func newDefaultTheme() Theme {
	return newTheme(
		lipgloss.Color("#7AA2F7"), // accent
		lipgloss.Color("#F7768E"), // error
		lipgloss.Color("#9ECE6A"), // ok
		lipgloss.Color("#E0AF68"), // shell
		lipgloss.Color("#7DCFFF"), // file
		lipgloss.Color("#BB9AF7"), // web
		lipgloss.Color("#FF87D7"), // skill
	)
}

// newDraculaTheme is the second curated chrome palette (issue #179 AC3): the
// canonical dracula hues — purple accent, red error, green ok — built on the
// same constructor pattern as the default, proving a new palette is a registry
// addition with no consumer change. The tool categories use the dracula
// secondary hues (orange, cyan, pink, yellow) so every category stays distinct
// from the accent/error/ok trio.
func newDraculaTheme() Theme {
	return newTheme(
		lipgloss.Color("#BD93F9"), // accent
		lipgloss.Color("#FF5555"), // error
		lipgloss.Color("#50FA7B"), // ok
		lipgloss.Color("#FFB86C"), // shell
		lipgloss.Color("#8BE9FD"), // file
		lipgloss.Color("#FF79C6"), // web
		lipgloss.Color("#F1FA8C"), // skill
	)
}

// newTokyoNightTheme is the curated tokyo-night chrome palette (issue #180):
// the canonical tokyo-night hues — purple accent (glamour's heading color for
// the theme), red error, green ok — so choosing tokyo-night for Markdown also
// re-skins the chrome with the same family instead of inheriting the default.
// The tool categories use the tokyo-night secondary hues (orange, light blue,
// teal); web deliberately reuses the purple accent, matching the family.
func newTokyoNightTheme() Theme {
	return newTheme(
		lipgloss.Color("#BB9AF7"), // accent
		lipgloss.Color("#F7768E"), // error
		lipgloss.Color("#9ECE6A"), // ok
		lipgloss.Color("#FF9E64"), // shell
		lipgloss.Color("#7DCFFF"), // file
		lipgloss.Color("#BB9AF7"), // web
		lipgloss.Color("#73DACA"), // skill
	)
}

// newPinkTheme is the curated pink chrome palette (issue #180): the glamour
// pink theme's hot-pink heading hue as the accent, with a crimson error and a
// soft green ok that keep ✓/✗ outcomes and the error pane distinguishable
// from the pink accent. The tool categories use hues readable against the pink
// family (amber, cyan, violet, blue) so category color never collides with the
// accent.
func newPinkTheme() Theme {
	return newTheme(
		lipgloss.Color("#FF87D7"), // accent
		lipgloss.Color("#E5484D"), // error
		lipgloss.Color("#69DB8C"), // ok
		lipgloss.Color("#FFB224"), // shell
		lipgloss.Color("#39C0ED"), // file
		lipgloss.Color("#A78BFA"), // web
		lipgloss.Color("#60A5FA"), // skill
	)
}

// newLightTheme is the curated light chrome palette (issue #180): hues
// readable on a light terminal background — the glamour light theme's heading
// blue as the accent, with a dark red error and a dark teal-green ok, each
// contrast-checked against white (≥ 4.5:1). The tool categories follow the
// same constraint: dark amber, dark cyan, dark violet and dark fuchsia, each
// ≥ 4.5:1 against white, so category colors stay readable on light terminals.
func newLightTheme() Theme {
	return newTheme(
		lipgloss.Color("#005FFF"), // accent
		lipgloss.Color("#C92A2A"), // error
		lipgloss.Color("#00875F"), // ok
		lipgloss.Color("#B45309"), // shell
		lipgloss.Color("#0E7490"), // file
		lipgloss.Color("#6D28D9"), // web
		lipgloss.Color("#A21CAF"), // skill
	)
}

// themeFor maps a config theme name to its chrome palette (issue #179,
// extended by issue #180): the Markdown render theme selection also selects
// the TUI chrome palette, so choosing a theme re-skins the whole surface, not
// just the Markdown body. "dracula", "tokyo-night", "pink" and "light"
// select their curated palettes; "auto" resolves to light or dark by the
// terminal background, mirroring the renderer's own auto resolution; "notty"
// keeps the default palette deliberately (the TUI never runs under notty —
// the boot guard refuses non-interactive contexts); an unknown value falls
// back to default — exactly the renderer's fallback behavior (issue #179
// AC4), so the chrome and Markdown never disagree about a theme.
func themeFor(name string) Theme {
	if name == "auto" {
		return themeFor(autoTheme())
	}
	switch name {
	case "dracula":
		return newDraculaTheme()
	case "tokyo-night":
		return newTokyoNightTheme()
	case "pink":
		return newPinkTheme()
	case "light":
		return newLightTheme()
	}
	return defaultTheme
}

// newTheme builds a Theme from its seven palette entries; the derived styles
// draw from them. It is the only place derived styles are constructed: every
// palette (default, dracula, future ones) shares the same style wiring, so
// palettes differ by hue alone and can never drift apart structurally.
func newTheme(accent, err, ok, shell, file, web, skill color.Color) Theme {
	return Theme{
		accent: accent,
		error:  err,
		ok:     ok,
		shell:  shell,
		file:   file,
		web:    web,
		skill:  skill,

		headerStyle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		statusStyle: lipgloss.NewStyle().Faint(true),
		// agentPaneStyle frames assistant answers as a left-bordered pane
		// (issue #122 AC1); errorPaneStyle is the same pane with the
		// error-colored border for failing turns so errors read as distinctly
		// as answers.
		agentPaneStyle: borderedPane(accent),
		errorPaneStyle: borderedPane(err),
		// thinkingStyle renders the 🤔 collapsed reasoning hint (issue #122
		// AC2); italic sets it apart from the answer body so the hint reads as
		// a distinct treatment, not just a colored line (issue #181 AC2).
		thinkingStyle: lipgloss.NewStyle().Faint(true).Italic(true).Foreground(accent),
		// toolStyle renders the ⊕ tool-entry line (issue #122); the category
		// variants colorize the line by the work the tool does (issue #181
		// AC1) — shell vs file vs web vs skill — each drawing from its own
		// palette entry, with toolStyle kept as the faint fallback for tools
		// no category recognizes.
		toolStyle:      lipgloss.NewStyle().Faint(true),
		toolShellStyle: lipgloss.NewStyle().Foreground(shell),
		toolFileStyle:  lipgloss.NewStyle().Foreground(file),
		toolWebStyle:   lipgloss.NewStyle().Foreground(web),
		toolSkillStyle: lipgloss.NewStyle().Foreground(skill),
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

// toolCategory groups tool entries by the work the tool does (issue #181 AC1)
// so the transcript can colorize a long session by category: shell commands,
// file reads/writes/edits, web fetches and browser opens, and skill
// activations. Tools no category recognizes fall back to the generic faint
// entry — color is a layer on top of the persistent ⊕ glyph (issue #181 AC5),
// never the only signal.
type toolCategory int

const (
	catOther toolCategory = iota
	catShell
	catFile
	catWeb
	catSkill
)

// toolCategoryOf maps a tool name to its transcript category (issue #181 AC1).
// Unknown names (future tools) report catOther so they keep the generic faint
// tool line instead of inventing a hue.
func toolCategoryOf(name string) toolCategory {
	switch name {
	case "bash":
		return catShell
	case "read", "write", "edit":
		return catFile
	case "web_fetch", "open_in_browser":
		return catWeb
	case "skill":
		return catSkill
	}
	return catOther
}

// toolCategoryStyle returns the theme style for a tool category: the
// per-category hue for shell/file/web/skill, and the generic faint tool line
// for anything else (issue #181 AC1/AC5).
func (th Theme) toolCategoryStyle(cat toolCategory) lipgloss.Style {
	switch cat {
	case catShell:
		return th.toolShellStyle
	case catFile:
		return th.toolFileStyle
	case catWeb:
		return th.toolWebStyle
	case catSkill:
		return th.toolSkillStyle
	}
	return th.toolStyle
}
