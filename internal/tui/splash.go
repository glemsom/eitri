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

// The launch splash: a five-phase, 50-frame (2 s at 40 ms/frame) choreography
// — particle storm (0–10), dwarf emergence (10–20), shatter (20–22),
// wordmark convergence (22–27) and settling (27–50) — shown on an empty
// transcript before settling into the static idleWelcome. Any keypress skips
// it instantly. Reduced-motion and non-UTF-8 locales never see the splash at
// all — they go straight to idleWelcome.

const (
	// splashTickInterval is the animation cadence; splashTotalFrames × interval ≈ 2 s.
	splashTickInterval = 40 * time.Millisecond
	splashTotalFrames  = 50
	// splashRainStartFrame is when the wordmark begins resolving out of the rain.
	splashWordmarkStartFrame = 22
	// splashRainEndFrame is when the last rain glyphs fade away.
	splashRainEndFrame = 46
	// splashEmergenceStartFrame is when the dwarf face begins fading in and the
	// non-Kitty fallback starts intensifying the rain.
	splashEmergenceStartFrame = 10
	// splashEyeFlashFrame is when the dwarf's eyes flash bright green.
	splashEyeFlashFrame = 18
	// splashEyeFlashColor is the exact bright green the eyes flash with.
	splashEyeFlashColor = "#00FF88"
	// splashEmergencePeakDensity is the storm ceiling the non-Kitty path ramps
	// to during emergence, so terminals without graphics still get a crescendo.
	splashEmergencePeakDensity = 0.45
	// splashStormDensity is the baseline particle-storm density (frames 0–10).
	splashStormDensity = 0.28
	// splashFlashColor matches the palette's hottest stop so the ignition
	// flash reads as the gradient flaring up rather than a foreign color.
	splashFlashColor = "#00FFC8"
)

// Splash owns the launch-splash lifecycle end to end: whether it runs, the
// animation state, the tick cadence, the title/cursor side-effects, the
// keypress skip, and the early end when the transcript gains content. The
// Model tracks only whether the splash is active (a nil pointer) and routes
// every message through the module's single Handle entry point; the hot path
// carries no splash branch of its own.
type Splash struct {
	state     *splashState // nil once the splash settled, was skipped, or never ran
	prevTitle string       // the window title to restore when the splash hands the screen back
	titleOut  io.Writer    // where OSC 0 title sequences are written
	tx        *Transcript  // consulted for the transcript-gained-content early end
}

// splashResult reports what the Model must do after one message lands on the
// splash: handled marks the message consumed, ended marks the splash over
// (settled, skipped, or transcript content arrived) so the Model drops the
// module pointer, and cmd is any command to run next.
type splashResult struct {
	handled bool
	ended   bool
	cmd     tea.Cmd
}

// newSplash builds the launch splash when the dependency enables it and the
// environment allows (reduced-motion and non-UTF-8 locales go straight to the
// static welcome), mirroring the model's Kitty graphics capability into its
// own render path and capturing the title to restore on exit. Nil when no
// splash runs.
func newSplash(d Dependencies, tx *Transcript, kitty bool) *Splash {
	s := splashFor(d.Splash)
	if s == nil {
		return nil
	}
	s.kitty = kitty
	return &Splash{
		state:     s,
		prevTitle: previousTerminalTitle(d),
		titleOut:  d.titleOut(),
		tx:        tx,
	}
}

// Start returns the startup command batch: the first animation tick plus the
// title/cursor takeover, both issued exactly once when the splash opens.
func (s *Splash) Start() tea.Cmd {
	return tea.Batch(splashTick(), splashStartCmd(s.titleOut))
}

// Handle routes one message into the splash. While the splash owns the screen
// the animation tick advances it, any keypress skips it instantly, and a
// transcript that gained content ends it early; all three hand the screen
// back through the end command. Unrelated messages are ignored so the Model
// still processes them normally (e.g. a resize during the splash).
func (s *Splash) Handle(msg tea.Msg) splashResult {
	switch msg.(type) {
	case splashTickMsg:
		if s.state == nil || s.tx.hasContent() {
			s.state = nil
			return splashResult{handled: true, ended: true, cmd: s.end()}
		}
		s.state.advance()
		if s.state.done() {
			s.state = nil
			return splashResult{handled: true, ended: true, cmd: s.end()}
		}
		return splashResult{handled: true, cmd: splashTick()}
	case tea.KeyPressMsg:
		// Any keypress skips the launch splash instantly; the skip consumes
		// the keypress itself, so the composer never sees the skipping key.
		s.state = nil
		return splashResult{handled: true, ended: true, cmd: s.end()}
	}
	return splashResult{}
}

// View renders the current splash frame at the given size.
func (s *Splash) View(w, h int) string { return renderSplash(s.state, w, h) }

// end returns the command that hands the screen back: it restores the
// pre-splash title and re-shows the blinking cursor in one synchronous write.
func (s *Splash) end() tea.Cmd { return splashEndCmd(s.titleOut, s.prevTitle) }

// splashState tracks the splash's animation progress; a nil pointer on the
// Splash module means the splash is over (or never ran).
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

// splashCursorHide is DECTCEM off (CSI ? 25 l): the hardware cursor disappears while the splash owns the screen.
const splashCursorHide = "\x1b[?25l"

// splashCursorShow is DECTCEM on followed by ATT160 blink-on, so the restored cursor reappears blinking rather than static.
const splashCursorShow = "\x1b[?25h\x1b[?12h"

// splashStartCmd installs the branding title and hides the cursor in one synchronous write: both belong to the moment the splash takes over the screen.
func splashStartCmd(w io.Writer) tea.Cmd {
	return func() tea.Msg {
		_, _ = io.WriteString(w, oscSetTitle(splashWindowTitle)+splashCursorHide)
		return nil
	}
}

// splashEndCmd restores the pre-splash title and re-shows the blinking cursor in one synchronous write: both belong to the moment the splash hands the screen back.
func splashEndCmd(w io.Writer, prevTitle string) tea.Cmd {
	return func() tea.Msg {
		_, _ = io.WriteString(w, oscSetTitle(prevTitle)+splashCursorShow)
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

// Wordmark geometry shared by the staggered reveal: five 6-cell letters with
// 3-cell gaps, matching how eitriWordmark lays out E I T R I.
const (
	splashLetterCells = 6
	splashLetterGap   = 3
	splashLetterCount = 5
	// splashEntryDropRows is the vertical overshoot a letter enters with before settling upward.
	splashEntryDropRows = 2
)

// letterIndexAt maps a wordmark column to its letter index (0=E … 4=I);
// gap columns between letters return -1.
func letterIndexAt(col int) int {
	if col < 0 || col >= splashLetterCount*(splashLetterCells+splashLetterGap)-splashLetterGap {
		return -1
	}
	l := col / (splashLetterCells + splashLetterGap)
	if col%(splashLetterCells+splashLetterGap) >= splashLetterCells {
		return -1
	}
	return l
}

// letterEntryFrame is the frame letter enters on: one frame after its left
// neighbor, so the wordmark reads as assembling left-to-right out of the rain.
func letterEntryFrame(letter int) int { return splashWordmarkStartFrame + letter }

// letterDropRows reports how many rows below its final position letter sits at frame:
// the full overshoot on the entry frame, zero once settled.
func letterDropRows(frame, letter int) int {
	if frame == letterEntryFrame(letter) {
		return splashEntryDropRows
	}
	return 0
}

// rainDensity returns the fraction of cells raining at a frame: full storm until emergence, an intensification ramp for non-Kitty terminals until the wordmark starts, then thinning to nothing by splashRainEndFrame.
func rainDensity(frame int, kitty bool) float64 {
	switch {
	case frame <= splashEmergenceStartFrame:
		return splashStormDensity
	case frame <= splashWordmarkStartFrame && !kitty:
		// Non-Kitty crescendo: no face to reveal, so the rain itself builds.
		t := float64(frame-splashEmergenceStartFrame) / float64(splashWordmarkStartFrame-splashEmergenceStartFrame)
		return splashStormDensity + t*(splashEmergencePeakDensity-splashStormDensity)
	case frame >= splashRainEndFrame:
		return 0
	default:
		return splashStormDensity * float64(splashRainEndFrame-frame) / float64(splashRainEndFrame-splashWordmarkStartFrame)
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
	// On Kitty terminals the face fades in behind the rain during emergence
	// and dissolves with the shatter; the graphics escape rides at the top of
	// the frame so the rain cells render over it.
	if s.kitty {
		b.WriteString(kittyFaceEscape(s.frame, w, h))
	}

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

	// The convergence flash fires for exactly one frame at the moment the
	// first wordmark row resolves. It replaces only its own row so the rain
	// keeps collapsing around it — a full-screen blank would read as a
	// glitch, not an ignition.
	flashRow := markTop + markRows/2
	var flashBar string
	if s.frame == splashWordmarkStartFrame {
		flashBar = lipgloss.NewStyle().Background(lipgloss.Color(splashFlashColor)).Render(strings.Repeat(" ", w))
	}

	// The eyes flash bright green for exactly one frame at the peak of the
	// emergence ramp — the moment the face is fully revealed.
	eyeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(splashEyeFlashColor))
	var eyes [2]cellPos
	eyesFlash := false
	if s.kitty && s.frame == splashEyeFlashFrame {
		if place, ok := faceGeometry(w, h); ok {
			eyes = eyeFlashCells(place)
			eyesFlash = true
		}
	}
	isEyeCell := func(r, c int) bool {
		for _, e := range eyes {
			if e == (cellPos{r, c}) {
				return true
			}
		}
		return false
	}

	stormCell := func(r, c int) string {
		mr := r - markTop
		mc := c - markLeft
		// An entering letter sits splashEntryDropRows low, so its glyphs can
		// reach drop rows below the settled footprint — scan those too.
		if mr >= 0 && mr < markRows+splashEntryDropRows && mc >= 0 && mc < markWidth {
			letter := letterIndexAt(mc)
			srcRow := mr - letterDropRows(s.frame, letter)
			var ch rune
			if letter >= 0 && srcRow >= 0 && srcRow < markRows {
				runes := []rune(eitriWordmark[srcRow])
				if mc < len(runes) {
					ch = runes[mc]
				}
			}
			if letter >= 0 && s.frame >= letterEntryFrame(letter) && ch != ' ' && ch != 0 {
				// Diagonal tone sweep (column + row drift) reads as light
				// gliding across the mark instead of a flat fill.
				return lipgloss.NewStyle().Foreground(splashColor(splashWordPalette, mc+mr*2, s.frame)).Render(string(ch))
			}
		}
		if eyesFlash && isEyeCell(r, c) {
			return eyeStyle.Render("█")
		}
		if rainDrop(r, c, s.frame, s.kitty) {
			return lipgloss.NewStyle().Foreground(splashColor(splashRainPalette, -c, -s.frame)).Render(string(splashGlyphAt(c, r, s.frame)))
		}
		return " "
	}
	for r := 0; r < h-2; r++ {
		if flashBar != "" && r == flashRow {
			b.WriteString(flashBar)
		} else {
			for c := 0; c < w; c++ {
				b.WriteString(stormCell(r, c))
			}
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
func rainDrop(row, col, frame int, kitty bool) bool {
	d := rainDensity(frame, kitty)
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
