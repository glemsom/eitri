package tui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// Theme is the styling surface for the TUI chrome: a palette registry of named colors plus the derived styles that draw from them.
type Theme struct {
	accent      color.Color // the single agent accent used across the surface
	error       color.Color // semantic color for failures (⚠ errors, ✗ tool outcomes)
	ok          color.Color // semantic color for successful tool outcomes (✓)
	shell       color.Color // semantic color for shell tool entries (bash, 🐚)
	file        color.Color // secondary hue (markdown links, 🧰 fallback)
	web         color.Color // semantic color for web tool entries (open_in_browser, 🌐)
	skill       color.Color // secondary hue (markdown images, ✨ fallback)
	thinking    color.Color // de-emphasized chain-of-thought hue (reasoning pane border, ≡ marker)
	text        color.Color // body text hue for markdown content (mapped from glamour's base 256-color indices)
	bubble      color.Color
	selectionBg color.Color // the fill marking a drag selection (indicated by background color change)

	railHues [3]color.Color

	headerStyle                lipgloss.Style // bold section header (settings title, prompts)
	statusStyle                lipgloss.Style // faint secondary text (strips, hints, tool lines)
	agentPaneStyle             lipgloss.Style // left-bordered pane framing assistant answers
	thinkingPaneStyle          lipgloss.Style // left-bordered pane framing reasoning (muted reasoning hue, italic)
	streamingThinkingPaneStyle lipgloss.Style // muted-border italic variant; the style-free live reasoning body is de-emphasized by the pane border, never body SGR
	errorPaneStyle             lipgloss.Style
	stoppedPaneStyle           lipgloss.Style
	streamingPaneStyle         lipgloss.Style
	streamingErrorPaneStyle    lipgloss.Style
	userBubbleStyle            lipgloss.Style // the carded background fill for user prompts
	thinkingStyle              lipgloss.Style
	toolStyle                  lipgloss.Style
	toolShellStyle             lipgloss.Style
	toolWebStyle               lipgloss.Style
	toolSkillStyle             lipgloss.Style
	outcomeOKStyle             lipgloss.Style
	outcomeErrStyle            lipgloss.Style
	slashSelectStyle           lipgloss.Style
	focusStyle                 lipgloss.Style
	bandSeparatorStyle         lipgloss.Style
	bandStatusStyle            lipgloss.Style

	railHeaderStyles [3]lipgloss.Style
	railBodyStyles   [3]lipgloss.Style
}

var defaultTheme = newDefaultTheme()

func newDefaultTheme() Theme {
	return newTheme(
		lipgloss.Color("#7AA2F7"),
		lipgloss.Color("#F7768E"),
		lipgloss.Color("#9ECE6A"),
		lipgloss.Color("#E0AF68"),
		lipgloss.Color("#7DCFFF"),
		lipgloss.Color("#BB9AF7"),
		lipgloss.Color("#FF87D7"),
		lipgloss.Color("#565F89"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#C0CAF5"), // text (light blue-gray, readable on dark bg)
		lipgloss.Color("#2A2F3A"), // bubble tint (near-background gray-blue)
		lipgloss.Color("#3B4261"), // selectionBackground (dim blue-gray)
		[3]color.Color{
			lipgloss.Color("#E0AF68"),
			lipgloss.Color("#7DCFFF"),
			lipgloss.Color("#9ECE6A"),
		},
	)
}

func newDraculaTheme() Theme {
	return newTheme(
		lipgloss.Color("#BD93F9"),
		lipgloss.Color("#FF5555"),
		lipgloss.Color("#50FA7B"),
		lipgloss.Color("#FFB86C"),
		lipgloss.Color("#8BE9FD"),
		lipgloss.Color("#FF79C6"),
		lipgloss.Color("#F1FA8C"),
		lipgloss.Color("#6272A4"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#F8F8F2"), // text (dracula foreground, readable on dark bg)
		lipgloss.Color("#3D3F51"), // bubble tint (dracula comment family)
		lipgloss.Color("#44475A"), // selectionBackground (dracula current-line)
		[3]color.Color{
			lipgloss.Color("#FFB86C"),
			lipgloss.Color("#8BE9FD"),
			lipgloss.Color("#50FA7B"),
		},
	)
}

// newTokyoNightTheme matches the glamour tokyo-night heading hue, so choosing that Markdown theme re-skins the chrome with the same family instead of inheriting the default.
func newTokyoNightTheme() Theme {
	return newTheme(
		lipgloss.Color("#BB9AF7"),
		lipgloss.Color("#F7768E"),
		lipgloss.Color("#9ECE6A"),
		lipgloss.Color("#FF9E64"),
		lipgloss.Color("#7DCFFF"),
		lipgloss.Color("#2AC3DE"),
		lipgloss.Color("#73DACA"),
		lipgloss.Color("#565F89"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#A9B1D6"), // text (tokyo-night foreground, readable on dark bg)
		lipgloss.Color("#292E42"), // bubble tint (tokyo-night bg-adjacent)
		lipgloss.Color("#33467C"), // selectionBackground (tokyo-night selection)
		[3]color.Color{
			lipgloss.Color("#FF9E64"),
			lipgloss.Color("#7DCFFF"),
			lipgloss.Color("#9ECE6A"),
		},
	)
}

// newPinkTheme uses the glamour pink heading hue as accent, with a crimson error and soft green ok that keep ✓/✗ outcomes distinguishable from the pink accent.
func newPinkTheme() Theme {
	return newTheme(
		lipgloss.Color("#FF87D7"),
		lipgloss.Color("#E5484D"),
		lipgloss.Color("#69DB8C"),
		lipgloss.Color("#FFB224"),
		lipgloss.Color("#39C0ED"),
		lipgloss.Color("#A78BFA"),
		lipgloss.Color("#60A5FA"),
		lipgloss.Color("#7A6E8A"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#F5E0DC"), // text (light pink-white, readable on dark bg)
		lipgloss.Color("#33202E"), // bubble tint (pink-family dark)
		lipgloss.Color("#4A2F3F"), // selectionBackground (pink-family taupe)
		[3]color.Color{
			lipgloss.Color("#FFB224"),
			lipgloss.Color("#39C0ED"),
			lipgloss.Color("#69DB8C"),
		},
	)
}

// newLightTheme is contrast-checked against a white background (≥ 4.5:1) for accent, error, and ok.
func newLightTheme() Theme {
	return newTheme(
		lipgloss.Color("#005FFF"),
		lipgloss.Color("#C92A2A"),
		lipgloss.Color("#00875F"),
		lipgloss.Color("#B45309"),
		lipgloss.Color("#0E7490"),
		lipgloss.Color("#6D28D9"),
		lipgloss.Color("#A21CAF"),
		lipgloss.Color("#5F6B7A"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#1C1C1C"), // text (dark gray, readable on white)
		lipgloss.Color("#EAEAEF"), // bubble tint (near-white gray)
		lipgloss.Color("#C7D8F7"), // selectionBackground (light blue highlight)
		[3]color.Color{
			lipgloss.Color("#B45309"),
			lipgloss.Color("#0E7490"),
			lipgloss.Color("#00875F"),
		},
	)
}

func newNordTheme() Theme {
	return newTheme(
		lipgloss.Color("#88C0D0"), // accent (frost)
		lipgloss.Color("#BF616A"), // error (aurora red)
		lipgloss.Color("#A3BE8C"), // ok (aurora green)
		lipgloss.Color("#EBCB8B"), // shell (aurora yellow)
		lipgloss.Color("#81A1C1"), // file (frost blue)
		lipgloss.Color("#B48EAD"), // web (aurora purple)
		lipgloss.Color("#D08770"), // skill (aurora orange)
		lipgloss.Color("#6E7B96"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#D8DEE9"), // text (nord snow storm, readable on dark bg)
		lipgloss.Color("#2E3440"), // bubble tint (polar-night bg)
		lipgloss.Color("#434C5E"), // selectionBackground (nord dark4)
		[3]color.Color{
			lipgloss.Color("#EBCB8B"),
			lipgloss.Color("#81A1C1"),
			lipgloss.Color("#A3BE8C"),
		},
	)
}

func newGruvboxTheme() Theme {
	return newTheme(
		lipgloss.Color("#83A598"), // accent (gruv blue)
		lipgloss.Color("#FB4934"), // error (bright red)
		lipgloss.Color("#B8BB26"), // ok (bright green)
		lipgloss.Color("#FABD2F"), // shell (bright yellow)
		lipgloss.Color("#8EC07C"), // file (aqua)
		lipgloss.Color("#D3869B"), // web (purple)
		lipgloss.Color("#FE8019"), // skill (orange)
		lipgloss.Color("#928374"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#EBDBB2"), // text (gruvbox fg, readable on dark bg)
		lipgloss.Color("#3C3836"), // bubble tint (bg1)
		lipgloss.Color("#504945"), // selectionBackground (gruvbox selection gray)
		[3]color.Color{
			lipgloss.Color("#FABD2F"),
			lipgloss.Color("#8EC07C"),
			lipgloss.Color("#B8BB26"),
		},
	)
}

func newSolarizedTheme() Theme {
	return newTheme(
		lipgloss.Color("#268BD2"), // accent (blue)
		lipgloss.Color("#DC322F"), // error (red)
		lipgloss.Color("#859900"), // ok (green)
		lipgloss.Color("#B58900"), // shell (yellow)
		lipgloss.Color("#2AA198"), // file (cyan)
		lipgloss.Color("#6C71C4"), // web (violet)
		lipgloss.Color("#D33682"), // skill (magenta)
		lipgloss.Color("#93A1A1"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#93A1A1"), // text (solarized base1, readable on dark bg)
		lipgloss.Color("#073642"), // bubble tint (bg)
		lipgloss.Color("#465B70"), // selectionBackground (solarized selection)
		[3]color.Color{
			lipgloss.Color("#B58900"),
			lipgloss.Color("#2AA198"),
			lipgloss.Color("#859900"),
		},
	)
}

// newDarkDaltonizedTheme is built on the Okabe-Ito colorblind-safe set: error is a vermillion orange and ok a bluish green, so ✓/✗ outcomes and the diff added/removed fills stay distinguishable without relying on the red-green pairing deuteranopes and protanopes most commonly confuse.
func newDarkDaltonizedTheme() Theme {
	return newTheme(
		lipgloss.Color("#56B4E9"), // accent (sky blue)
		lipgloss.Color("#D55E00"), // error (vermillion orange)
		lipgloss.Color("#009E73"), // ok (bluish green)
		lipgloss.Color("#F0E442"), // shell (yellow)
		lipgloss.Color("#5796D8"), // file (bright blue)
		lipgloss.Color("#CC79A7"), // web (reddish purple)
		lipgloss.Color("#E69F00"), // skill (golden orange)
		lipgloss.Color("#7D8AA0"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#E0E0E0"), // text (light neutral, readable on dark bg)
		lipgloss.Color("#232A36"), // bubble tint (neutral blue-gray)
		lipgloss.Color("#2F3A4C"), // selectionBackground (neutral blue-gray)
		[3]color.Color{
			lipgloss.Color("#F0E442"),
			lipgloss.Color("#5796D8"),
			lipgloss.Color("#009E73"),
		},
	)
}

// newLightDaltonizedTheme keeps the Okabe-Ito hues (already ≥4.5:1 on white for the semantic pair) with a near-white bubble tint.
func newLightDaltonizedTheme() Theme {
	return newTheme(
		lipgloss.Color("#0072B2"), // accent (Okabe-Ito blue, dark on light)
		lipgloss.Color("#D55E00"), // error (vermillion orange)
		lipgloss.Color("#009E73"), // ok (bluish green)
		lipgloss.Color("#B58900"), // shell (dark yellow, readable on white)
		lipgloss.Color("#0E7490"), // file (dark cyan)
		lipgloss.Color("#6D28D9"), // web (violet)
		lipgloss.Color("#A21CAF"), // skill (magenta)
		lipgloss.Color("#5F6B7A"), // thinking (muted chain-of-thought hue)
		lipgloss.Color("#1C1C1C"), // text (dark gray, readable on white)
		lipgloss.Color("#E9E9EF"), // bubble tint (near-white)
		lipgloss.Color("#C9D4E8"), // selectionBackground (light blue highlight)
		[3]color.Color{
			lipgloss.Color("#B58900"),
			lipgloss.Color("#0E7490"),
			lipgloss.Color("#009E73"),
		},
	)
}

// themeFor maps a config theme name to its chrome palette, re-skinning the
// whole surface with the same theme the Markdown renderer applied. "auto"
// follows the terminal background; any other unknown name falls back to
// default — mirroring the renderer's fallback so the chrome and Markdown never
// disagree. "notty" is no longer selectable but stored configs fall back to
// default.
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
	return newDefaultTheme()
}

func newTheme(accent, err, ok, shell, file, web, skill, thinking, text, bubble, selectionBg color.Color, rail [3]color.Color) Theme {
	// Live panes use a neutral muted color so their contrast is consistent
	// across accent palettes and remains readable against dark backgrounds.
	streaming := lipgloss.Color("#667085")
	th := Theme{
		accent:      accent,
		error:       err,
		ok:          ok,
		shell:       shell,
		file:        file,
		web:         web,
		skill:       skill,
		thinking:    thinking,
		text:        text,
		bubble:      bubble,
		selectionBg: selectionBg,
		railHues:    rail,

		headerStyle: lipgloss.NewStyle().Bold(true).Foreground(accent),
		statusStyle: lipgloss.NewStyle().Faint(true),
		userBubbleStyle: lipgloss.NewStyle().
			Background(bubble).
			PaddingLeft(2).PaddingRight(2).PaddingTop(1).PaddingBottom(1),
		agentPaneStyle:             answerPane(accent),
		thinkingPaneStyle:          borderedPane(thinking).Italic(true),
		streamingThinkingPaneStyle: borderedPane(streaming).Italic(true),
		errorPaneStyle:             answerPane(err),
		stoppedPaneStyle:           answerPane(dimmed(accent, 0.6)),
		streamingPaneStyle:         answerPane(streaming),
		streamingErrorPaneStyle:    answerPane(streaming),
		thinkingStyle:              lipgloss.NewStyle().Italic(true).Foreground(thinking),
		toolStyle:                  lipgloss.NewStyle().Faint(true),
		toolShellStyle:             lipgloss.NewStyle().Foreground(shell),
		toolWebStyle:               lipgloss.NewStyle().Foreground(web),
		toolSkillStyle:             lipgloss.NewStyle().Foreground(skill),
		outcomeOKStyle:             lipgloss.NewStyle().Foreground(ok),
		outcomeErrStyle:            lipgloss.NewStyle().Foreground(err),
		slashSelectStyle:           lipgloss.NewStyle().Bold(true).Foreground(accent),
		focusStyle:                 lipgloss.NewStyle().Bold(true).Foreground(accent),
		bandSeparatorStyle:         lipgloss.NewStyle().Foreground(accent),
		bandStatusStyle:            lipgloss.NewStyle().Foreground(accent),
	}
	for i, c := range rail {
		th.railHeaderStyles[i] = lipgloss.NewStyle().Bold(true).Foreground(c)
		th.railBodyStyles[i] = lipgloss.NewStyle().Foreground(c)
	}
	return th
}

// railSection indexes the right context rail's three sections so the per-section hues and styles stay positionally consistent between the theme registry and the rail renderer.
type railSection int

const (
	railStats railSection = iota
	railContext
	railModel
)

func (th Theme) railHeader(s railSection, text string) string {
	icon := ""
	switch s {
	case railStats:
		icon = lookup("railStats") + " "
	case railContext:
		icon = lookup("railContext") + " "
	case railModel:
		icon = lookup("railModel") + " "
	}
	return th.railHeaderStyles[s].Render(icon + text)
}

func (th Theme) railBody(s railSection, text string) string {
	return th.railBodyStyles[s].Render(text)
}

// selectionBgSGR returns the marking background as a `48;2;r;g;b` SGR for a
// drag selection, so highlighted cells change background color instead of
// relying on reverse video.
func (th Theme) selectionBgSGR() string {
	r, g, b, _ := th.selectionBg.RGBA() // 16-bit channels per image/color
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

// dimmed scales a color's RGB toward black by the given factor, for use as a same-hue background fill (diff lines, subtle cards).
func dimmed(c color.Color, f float64) color.Color {
	r, g, b, _ := c.RGBA() // 16-bit channels per image/color
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		uint8(float64(r>>8)*f), uint8(float64(g>>8)*f), uint8(float64(b>>8)*f)))
}

// answerPane deliberately adds no border glyphs: a drag-selected answer must copy out without stray box-drawing characters.
func answerPane(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(c).
		BorderForeground(c).
		PaddingLeft(1)
}

func borderedPane(c color.Color) lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.Border{Left: "│"}).
		BorderLeft(true).
		BorderForeground(c).
		PaddingLeft(1)
}

func (th Theme) toolCategoryStyle(cat toolCategory) lipgloss.Style {
	switch cat {
	case catShell:
		return th.toolShellStyle
	case catWeb:
		return th.toolWebStyle
	case catSkill:
		return th.toolSkillStyle
	}
	return th.toolStyle
}
