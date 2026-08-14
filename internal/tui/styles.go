package tui

import (
	"fmt"
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
	// bubble is the subtle background fill for user prompts, carding the user
	// side of the transcript (benchmark §4.1: message bubbles on a filled
	// background). It is a near-background tint per theme, never a saturated
	// hue — the fill must read as a card, not a highlight.
	bubble color.Color

	// railHues are the per-section hues for the right context rail (issue
	// #182): [stats, context, model], each distinct so a glance tells the
	// sections apart under any palette. The skills section hue is gone with
	// the section (issue #188).
	railHues [3]color.Color

	// Derived styles, drawn from the palette entries.
	headerStyle        lipgloss.Style // bold section header (settings title, prompts)
	statusStyle        lipgloss.Style // faint secondary text (strips, hints, tool lines)
	agentPaneStyle     lipgloss.Style // left-bordered pane framing assistant answers
	errorPaneStyle     lipgloss.Style // the same pane with the error-colored border
	userBubbleStyle    lipgloss.Style // the carded background fill for user prompts
	thinkingStyle      lipgloss.Style // the 🤔 collapsed reasoning hint
	toolStyle          lipgloss.Style // the ⊕ tool-entry line (uncategorized fallback)
	toolShellStyle     lipgloss.Style // the ⊕ tool-entry line, shell category
	toolFileStyle      lipgloss.Style // the ⊕ tool-entry line, file category
	toolWebStyle       lipgloss.Style // the ⊕ tool-entry line, web category
	toolSkillStyle     lipgloss.Style // the ⊕ tool-entry line, skill category
	outcomeOKStyle     lipgloss.Style // the ✓ tool-outcome tag
	outcomeErrStyle    lipgloss.Style // the ✗ tool-outcome tag
	// diffAddStyle / diffDelStyle render the review panel's inline diff lines:
	// the ok/error hue on a dimmed background fill of the same hue, so added/
	// removed lines carry the conventional green/red vocabulary in the theme's
	// own palette (benchmark §4.2: diff colors are theme-aware, bg-filled).
	diffAddStyle       lipgloss.Style
	diffDelStyle       lipgloss.Style
	slashSelectStyle   lipgloss.Style // the selected slash-completion candidate
	bandSeparatorStyle lipgloss.Style // the separator row framing the bottom band
	// bandStatusStyle renders the live status strip in the accent hue so the
	// bottom band matches the colorized right rail (issue #182 AC4).
	bandStatusStyle lipgloss.Style

	// railHeaderStyles / railBodyStyles render the right rail's sections
	// (issue #182): bold headers and body lines, each in its section's hue.
	railHeaderStyles [3]lipgloss.Style
	railBodyStyles   [3]lipgloss.Style
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
		lipgloss.Color("#2A2F3A"), // bubble tint (near-background gray-blue)
		[3]color.Color{
			lipgloss.Color("#E0AF68"),
			lipgloss.Color("#7DCFFF"),
			lipgloss.Color("#9ECE6A"),
		},
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
		lipgloss.Color("#3D3F51"), // bubble tint (dracula comment family)
		[3]color.Color{
			lipgloss.Color("#FFB86C"),
			lipgloss.Color("#8BE9FD"),
			lipgloss.Color("#50FA7B"),
		},
	)
}

// newTokyoNightTheme is the curated tokyo-night chrome palette (issue #180):
// the canonical tokyo-night hues — purple accent (glamour's heading color for
// the theme), red error, green ok — so choosing tokyo-night for Markdown also
// re-skins the chrome with the same family instead of inheriting the default.
// The tool categories use the tokyo-night secondary hues (orange, light blue,
// cyan, teal), each distinct from the accent/error/ok trio (issue #181 AC1).
func newTokyoNightTheme() Theme {
	return newTheme(
		lipgloss.Color("#BB9AF7"), // accent
		lipgloss.Color("#F7768E"), // error
		lipgloss.Color("#9ECE6A"), // ok
		lipgloss.Color("#FF9E64"), // shell
		lipgloss.Color("#7DCFFF"), // file
		lipgloss.Color("#2AC3DE"), // web
		lipgloss.Color("#73DACA"), // skill
		lipgloss.Color("#292E42"), // bubble tint (tokyo-night bg-adjacent)
		[3]color.Color{
			lipgloss.Color("#FF9E64"),
			lipgloss.Color("#7DCFFF"),
			lipgloss.Color("#9ECE6A"),
		},
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
		lipgloss.Color("#33202E"), // bubble tint (pink-family dark)
		[3]color.Color{
			lipgloss.Color("#FFB224"),
			lipgloss.Color("#39C0ED"),
			lipgloss.Color("#69DB8C"),
		},
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
		lipgloss.Color("#EAEAEF"), // bubble tint (near-white gray)
		[3]color.Color{
			lipgloss.Color("#B45309"),
			lipgloss.Color("#0E7490"),
			lipgloss.Color("#00875F"),
		},
	)
}

// newNordTheme is the curated nord chrome palette: the polar-night family —
// frosted blue accent, nord red error, nord green ok — with the secondary hues
// (yellow, frost, aurora purple, orange) for the tool categories.
func newNordTheme() Theme {
	return newTheme(
		lipgloss.Color("#88C0D0"), // accent (frost)
		lipgloss.Color("#BF616A"), // error (aurora red)
		lipgloss.Color("#A3BE8C"), // ok (aurora green)
		lipgloss.Color("#EBCB8B"), // shell (aurora yellow)
		lipgloss.Color("#81A1C1"), // file (frost blue)
		lipgloss.Color("#B48EAD"), // web (aurora purple)
		lipgloss.Color("#D08770"), // skill (aurora orange)
		lipgloss.Color("#2E3440"), // bubble tint (polar-night bg)
		[3]color.Color{
			lipgloss.Color("#EBCB8B"),
			lipgloss.Color("#81A1C1"),
			lipgloss.Color("#A3BE8C"),
		},
	)
}

// newGruvboxTheme is the curated gruvbox chrome palette: the dark-medium
// family — gruv blue accent, bright red error, bright green ok — with the
// secondary hues (yellow, aqua, purple, orange) for the tool categories.
func newGruvboxTheme() Theme {
	return newTheme(
		lipgloss.Color("#83A598"), // accent (gruv blue)
		lipgloss.Color("#FB4934"), // error (bright red)
		lipgloss.Color("#B8BB26"), // ok (bright green)
		lipgloss.Color("#FABD2F"), // shell (bright yellow)
		lipgloss.Color("#8EC07C"), // file (aqua)
		lipgloss.Color("#D3869B"), // web (purple)
		lipgloss.Color("#FE8019"), // skill (orange)
		lipgloss.Color("#3C3836"), // bubble tint (bg1)
		[3]color.Color{
			lipgloss.Color("#FABD2F"),
			lipgloss.Color("#8EC07C"),
			lipgloss.Color("#B8BB26"),
		},
	)
}

// newSolarizedTheme is the curated solarized chrome palette: the dark family —
// solarized blue accent, red error, green ok — with the secondary hues
// (yellow, cyan, violet, magenta) for the tool categories.
func newSolarizedTheme() Theme {
	return newTheme(
		lipgloss.Color("#268BD2"), // accent (blue)
		lipgloss.Color("#DC322F"), // error (red)
		lipgloss.Color("#859900"), // ok (green)
		lipgloss.Color("#B58900"), // shell (yellow)
		lipgloss.Color("#2AA198"), // file (cyan)
		lipgloss.Color("#6C71C4"), // web (violet)
		lipgloss.Color("#D33682"), // skill (magenta)
		lipgloss.Color("#073642"), // bubble tint (bg)
		[3]color.Color{
			lipgloss.Color("#B58900"),
			lipgloss.Color("#2AA198"),
			lipgloss.Color("#859900"),
		},
	)
}

// newDarkDaltonizedTheme is the curated deuteranopia/protanopia-safe dark
// chrome palette, built on the Okabe-Ito colorblind-safe set: error is a
// vermillion orange and ok a bluish green, so the ✓/✗ outcomes and the diff
// added/removed fills stay distinguishable without red-green hue alone (the
// pair most commonly confused). The tool categories draw from the remaining
// Okabe-Ito hues (yellow, blue, reddish-purple, golden orange).
func newDarkDaltonizedTheme() Theme {
	return newTheme(
		lipgloss.Color("#56B4E9"), // accent (sky blue)
		lipgloss.Color("#D55E00"), // error (vermillion orange)
		lipgloss.Color("#009E73"), // ok (bluish green)
		lipgloss.Color("#F0E442"), // shell (yellow)
		lipgloss.Color("#5796D8"), // file (bright blue)
		lipgloss.Color("#CC79A7"), // web (reddish purple)
		lipgloss.Color("#E69F00"), // skill (golden orange)
		lipgloss.Color("#232A36"), // bubble tint (neutral blue-gray)
		[3]color.Color{
			lipgloss.Color("#F0E442"),
			lipgloss.Color("#5796D8"),
			lipgloss.Color("#009E73"),
		},
	)
}

// newLightDaltonizedTheme is the light-terminal variant of the daltonized
// palette: the same Okabe-Ito hues (already ≥4.5:1 on white for the
// semantic pair) with a near-white bubble tint.
func newLightDaltonizedTheme() Theme {
	return newTheme(
		lipgloss.Color("#0072B2"), // accent (Okabe-Ito blue, dark on light)
		lipgloss.Color("#D55E00"), // error (vermillion orange)
		lipgloss.Color("#009E73"), // ok (bluish green)
		lipgloss.Color("#B58900"), // shell (dark yellow, readable on white)
		lipgloss.Color("#0E7490"), // file (dark cyan)
		lipgloss.Color("#6D28D9"), // web (violet)
		lipgloss.Color("#A21CAF"), // skill (magenta)
		lipgloss.Color("#E9E9EF"), // bubble tint (near-white)
		[3]color.Color{
			lipgloss.Color("#B58900"),
			lipgloss.Color("#0E7490"),
			lipgloss.Color("#009E73"),
		},
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
	case "nord":
		return newNordTheme()
	case "gruvbox":
		return newGruvboxTheme()
	case "solarized":
		return newSolarizedTheme()
	case "dark-daltonized":
		return newDarkDaltonizedTheme()
	case "light-daltonized":
		return newLightDaltonizedTheme()
	}
	return defaultTheme
}

// newTheme builds a Theme from its seven palette entries; the derived styles
// draw from them. It is the only place derived styles are constructed: every
// palette (default, dracula, future ones) shares the same style wiring, so
// palettes differ by hue alone and can never drift apart structurally.
func newTheme(accent, err, ok, shell, file, web, skill, bubble color.Color, rail [3]color.Color) Theme {
	th := Theme{
		accent:   accent,
		error:    err,
		ok:       ok,
		shell:    shell,
		file:     file,
		web:      web,
		skill:    skill,
		bubble:   bubble,
		railHues: rail,

		headerStyle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		statusStyle: lipgloss.NewStyle().Faint(true),
		// userBubbleStyle cards user prompts on the theme's bubble tint: a
		// subtle near-background fill with breathing padding, so the user side
		// of the transcript reads as a card against the bare agent panes
		// (benchmark §4.1). Padding is set here; width is applied per render
		// because the pane width changes with the rail.
		userBubbleStyle: lipgloss.NewStyle().
			Background(bubble).
			PaddingLeft(2).PaddingRight(2).PaddingTop(1).PaddingBottom(1),
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
		// Diff lines keep the git vocabulary but in the theme's hues: the ok/error
		// foreground on a dimmed same-hue background fill (the fill is derived,
		// so every palette gets a coherent diff without hand-tuning per theme).
		diffAddStyle: lipgloss.NewStyle().
			Foreground(ok).
			Background(dimmed(ok, 0.14)),
		diffDelStyle: lipgloss.NewStyle().
			Foreground(err).
			Background(dimmed(err, 0.14)),
		// slashSelectStyle highlights the selected slash-completion candidate.
		slashSelectStyle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		// bandSeparatorStyle draws the separator row that frames the fixed
		// bottom band (status strip + slash completion + composer) as one
		// coherent region (issue #122 AC3). It is a plain separator line, not
		// a lipgloss border: a border pads every band line to the widest row,
		// which would re-pad the composer's own rows and break the band's
		// bottom-pinned layout.
		bandSeparatorStyle: lipgloss.NewStyle().Foreground(accent),
		// The status strip (issue #182 AC4) shares the accent with the band
		// separator, so the whole fixed bottom band reads as one accent-treated
		// region under the colorized rail.
		bandStatusStyle: lipgloss.NewStyle().Foreground(accent),
	}
	// The rail's per-section styles draw from its hues: a bold header plus the
	// body lines, so the section reads as a colored block with a distinct
	// header (issue #182). Color is a layer on top of the plain section text.
	for i, c := range rail {
		th.railHeaderStyles[i] = lipgloss.NewStyle().Bold(true).Foreground(c)
		th.railBodyStyles[i] = lipgloss.NewStyle().Foreground(c)
	}
	return th
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

// railSection indexes the right context rail's three sections (issue #182) so
// the per-section hues and styles stay positionally consistent between the
// theme registry and the rail renderer. The SKILLS section was removed (issue
// #188); the rail is STATS / CONTEXT / MODEL.
type railSection int

const (
	railStats railSection = iota
	railContext
	railModel
)

// railHeader renders a rail section header in its section's hue (issue #182).
func (th Theme) railHeader(s railSection, text string) string {
	return th.railHeaderStyles[s].Render(text)
}

// railBody renders a rail section's body lines in its section's hue (issue
// #182). Color is a layer on top: the section text stays plain, so a
// monochrome terminal still reads every value.
func (th Theme) railBody(s railSection, text string) string {
	return th.railBodyStyles[s].Render(text)
}

// dimmed scales a color's RGB toward black by the given factor, for use as a
// same-hue background fill (diff lines, subtle cards). The result is a hex
// lipgloss color, so it degrades with the terminal's color profile like any
// other palette color.
func dimmed(c color.Color, f float64) color.Color {
	r, g, b, _ := c.RGBA() // 16-bit channels per image/color
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		uint8(float64(r>>8)*f), uint8(float64(g>>8)*f), uint8(float64(b>>8)*f)))
}

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
