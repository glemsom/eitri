package tui

import (
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"charm.land/bubbletea/v2"
	"image/color"

	"charm.land/lipgloss/v2"
)

// The launch splash: a Matrix-style falling glyph rain that converges into a
// lolcat rainbow-gradient EITRI wordmark, shown for roughly two seconds on an
// empty transcript before settling into the static idleWelcome. Any keypress
// skips it instantly. Reduced-motion and non-UTF-8 locales never see the
// splash at all — they go straight to idleWelcome.

const (
	// splashTickInterval is the animation cadence; splashTotalFrames × interval ≈ 2 s.
	splashTickInterval = 40 * time.Millisecond
	splashTotalFrames  = 50
	// splashRainStartFrame is when the wordmark begins resolving out of the rain.
	splashWordmarkStartFrame = 22
	// splashRainEndFrame is when the last rain glyphs fade away.
	splashRainEndFrame = 46
)

// splashState tracks the splash's animation progress; a nil pointer on Model means the splash is over (or never ran).
type splashState struct {
	frame int
	// kitty mirrors the model's Kitty graphics capability so the splash's own render path can gate image embedding without reaching back into the model.
	kitty bool
}

// done reports whether the splash has played through its full duration.
func (s *splashState) done() bool { return s.frame >= splashTotalFrames }

// advance moves the animation one frame forward.
func (s *splashState) advance() {
	if s.frame < splashTotalFrames {
		s.frame++
	}
}

// splashWindowTitle is the branding title the splash installs via OSC 0 while it plays.
const splashWindowTitle = "⚒ Eitri — forging agents"

// oscSetTitle returns the OSC 0 (icon-and-window-title) sequence for title.
// Terminals without title support ignore the escape, so emitting it is always
// harmless.
func oscSetTitle(title string) string { return "\x1b]0;" + title + "\x07" }

// splashTitleCmd returns a command that sets the terminal window title via an OSC 0 escape written to w. Terminals without title support ignore the escape, so emission is always harmless.
func splashTitleCmd(w io.Writer, title string) tea.Cmd {
	return func() tea.Msg {
		_, _ = io.WriteString(w, oscSetTitle(title))
		return nil
	}
}

// splashTickMsg advances the splash by one frame.
type splashTickMsg struct{}

// splashTick returns the command that delivers the next splash frame.
func splashTick() tea.Cmd {
	return tea.Tick(splashTickInterval, func(time.Time) tea.Msg { return splashTickMsg{} })
}

// eitriWordmark is the solid-block EITRI banner the rain resolves into. It
// uses only █ and spaces — half-block box-drawing glyphs (╗ ═ ║ …) render
// inconsistently across terminal fonts and made the mark misread as "Fitri".
// Letters are 6 cells wide with 3-cell gaps so each glyph reads distinctly:
// E I T R I, where both I letters carry top/bottom serifs.
var eitriWordmark = []string{
	`██████   ██████   ██████   ██████   ██████`,
	`██         ██       ██     ██  ██     ██  `,
	`█████      ██       ██     █████      ██  `,
	`██         ██       ██     ██ ██      ██  `,
	`██████   ██████     ██     ██  ██   ██████`,
}

// splashTagline is the emoji accent strip under the wordmark.
func splashTagline() string {
	return g("🔨 forging agents ⚡ ✨", "# forging agents + *")
}

// splashWordPalette is a true-color cyan-to-blue gradient sweep for the
// wordmark: #00FFC8 → #00AAFF interpolated into stops that splashColor cycles
// per column (with row/frame drift) so a luminous glimmer glides across the
// letters. lipgloss renders these as 24-bit SGR; terminals without truecolor
// get an automatically downsampled 256/16-color version at output time.
var splashWordPalette = splashGradient("#00FFC8", "#00AAFF", 12)

// splashGradient interpolates n evenly spaced stops between two hex colors,
// inclusive of both endpoints, as lipgloss colors.
func splashGradient(from, to string, n int) []color.Color {
	if n < 2 {
		n = 2
	}
	fr, fg, fb := hexRGB(from)
	tr, tg, tb := hexRGB(to)
	palette := make([]color.Color, n)
	for i := range palette {
		t := float64(i) / float64(n-1)
		palette[i] = lipgloss.Color(fmt.Sprintf("#%02X%02X%02X",
			uint8(math.Round(float64(fr)+(float64(tr-fr))*t)),
			uint8(math.Round(float64(fg)+(float64(tg-fg))*t)),
			uint8(math.Round(float64(fb)+(float64(tb-fb))*t))))
	}
	return palette
}

// hexRGB decodes a #RRGGBB string into its component bytes; invalid input yields black.
func hexRGB(s string) (r, g, b uint8) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0
	}
	parse := func(hi, lo byte) uint8 {
		nib := func(c byte) uint8 {
			switch {
			case c >= '0' && c <= '9':
				return c - '0'
			case c >= 'A' && c <= 'F':
				return c - 'A' + 10
			case c >= 'a' && c <= 'f':
				return c - 'a' + 10
			}
			return 0
		}
		return nib(hi)<<4 | nib(lo)
	}
	return parse(s[1], s[2]), parse(s[3], s[4]), parse(s[5], s[6])
}

// splashRainPalette is the dimmer frost tones used for falling rain glyphs.
var splashRainPalette = []color.Color{
	lipgloss.Color("60"), lipgloss.Color("67"), lipgloss.Color("103"),
	lipgloss.Color("66"), lipgloss.Color("24"),
}

// splashColor picks the cold hue for a column at a frame from the given
// palette: tone sweeps left-to-right and drifts with the frame for shimmer.
func splashColor(palette []color.Color, col, frame int) color.Color {
	return palette[((col+frame)%len(palette)+len(palette))%len(palette)]
}

// splashHash is a tiny deterministic hash so the rain looks random yet renders identically across calls — the tests rely on that stability.
func splashHash(a, b, c int) int {
	h := a*374761393 + b*668265263 + c*2147483647
	h = (h ^ (h >> 13)) * 1274126177
	h = h ^ (h >> 16)
	if h < 0 {
		h = -h
	}
	return h
}

// splashGlyphs is the falling character repertoire: Old Norse runic futhark
// leading, with a few matrix katakana kept for texture.
const splashGlyphs = "ᚠᚢᚦᚨᚱᚲᚷᚹᚺᚾᛁᛃᛇᛈᛉᛊᛏᛒᛖᛗᛚᛜᛞᛟᚱᚨｱｲｳｿ"

// splashGlyphAt picks the rain glyph for a cell.
func splashGlyphAt(col, row, frame int) rune {
	runes := []rune(splashGlyphs)
	return runes[splashHash(col, row, frame)%len(runes)]
}

// wordmarkVisible reports whether wordmark row r has resolved out of the rain by the given frame: rows appear top-down once the convergence phase starts.
func wordmarkVisible(frame, row, rows int) bool {
	if frame < splashWordmarkStartFrame {
		return false
	}
	revealed := (frame-splashWordmarkStartFrame)*(rows+1)/(splashTotalFrames-splashWordmarkStartFrame) + 1
	return row < revealed
}

// rainDensity returns the fraction of cells raining at a frame: full storm until the wordmark starts, thinning to nothing by splashRainEndFrame.
func rainDensity(frame int) float64 {
	switch {
	case frame <= splashWordmarkStartFrame:
		return 0.28
	case frame >= splashRainEndFrame:
		return 0
	default:
		return 0.28 * float64(splashRainEndFrame-frame) / float64(splashRainEndFrame-splashWordmarkStartFrame)
	}
}

// renderSplash draws the full-screen splash frame: rain streaks converging into the rainbow-lit EITRI wordmark centered in the terminal.
func renderSplash(s *splashState, w, h int) string {
	if w <= 0 {
		w = presizeTerminalWidth
	}
	if h <= 0 {
		h = 24
	}
	var b strings.Builder

	markRows := len(eitriWordmark)
	markWidth := len([]rune(eitriWordmark[0]))
	// The wordmark block sits centered with room for the tagline beneath it;
	// rain covers every cell of the screen around it.
	topBlank := (h - markRows - 3) / 2
	if topBlank < 1 {
		topBlank = 1
	}
	markTop := topBlank
	markLeft := (w - markWidth) / 2
	if markLeft < 0 {
		markLeft = 0
	}

	stormCell := func(r, c int) string {
		mr := r - markTop
		mc := c - markLeft
		if mr >= 0 && mr < markRows && mc >= 0 && mc < markWidth {
			runes := []rune(eitriWordmark[mr])
			var ch rune
			if mc < len(runes) {
				ch = runes[mc]
			}
			if wordmarkVisible(s.frame, mr, markRows) && ch != ' ' && ch != 0 {
				// Diagonal tone sweep (column + row drift) reads as light
				// gliding across the mark instead of a flat fill.
				return lipgloss.NewStyle().Foreground(splashColor(splashWordPalette, mc+mr*2, s.frame)).Render(string(ch))
			}
		}
		if rainDrop(r, c, s.frame) {
			return lipgloss.NewStyle().Foreground(splashColor(splashRainPalette, -c, -s.frame)).Render(string(splashGlyphAt(c, r, s.frame)))
		}
		return " "
	}
	for r := 0; r < h-2; r++ {
		for c := 0; c < w; c++ {
			b.WriteString(stormCell(r, c))
		}
		b.WriteString("\n")
	}

	if s.frame >= splashWordmarkStartFrame+markRows+1 {
		tag := splashTagline()
		tagPad := (w - len([]rune(tag))) / 2
		if tagPad > 0 {
			b.WriteString(strings.Repeat(" ", tagPad))
		}
		b.WriteString(lipgloss.NewStyle().Foreground(splashColor(splashWordPalette, w/2, s.frame)).Render(tag))
	}
	b.WriteString("\n")
	return b.String()
}

// rainDrop decides whether cell (row,col) rains at a frame: a per-cell pseudo-random gate thinned by the frame's density, with vertical streak alignment so the fall reads as columns.
func rainDrop(row, col, frame int) bool {
	d := rainDensity(frame)
	if d <= 0 {
		return false
	}
	return splashHash(col, row, frame/6)%100 < int(d*100)
}

// splashFor returns the initial splash state when enabled, or nil when the environment should skip straight to the static welcome (splash off, reduced motion, non-UTF-8 locale).
func splashFor(enabled bool) *splashState {
	if !enabled || !motionEnabled() || !localeSupportsUTF8() {
		return nil
	}
	return &splashState{}
}
