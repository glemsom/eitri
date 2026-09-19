package tui

import (
	"fmt"
	"image/color"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Forge ambience: the palette-driven gradient treatment shared by the idle
// brand, the header rules, and the chrome panel's top border. Everything here
// is glyph-tier (theme-owned colour, UTF-8 only), so it adds no icon and no
// fallback path.

// gradientColorAt returns the theme's gradient hue at position t in [0,1],
// blending accent -> skill -> web across the active palette. A nil palette (a
// bare Theme in tests or a zero value) returns nil so callers can fall back to
// a plain glyph.
func gradientColorAt(th Theme, t float64) color.Color {
	if th.accent == nil || th.skill == nil || th.web == nil {
		return nil
	}
	switch {
	case t <= 0:
		return th.accent
	case t >= 1:
		return th.web
	case t < 0.5:
		return lerpColor(th.accent, th.skill, t*2)
	default:
		return lerpColor(th.skill, th.web, (t-0.5)*2)
	}
}

// lerpColor blends two colors in RGB by t in [0,1], returning a hex color so
// every palette gets the same treatment without new Theme fields.
func lerpColor(a, b color.Color, t float64) color.Color {
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	mix := func(x, y uint32) uint8 {
		return uint8(float64(x>>8) + (float64(y>>8)-float64(x>>8))*t)
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(ar, br), mix(ag, bg), mix(ab, bb)))
}

// gradientBands quantises the interpolation into a fixed set of colour bands.
// A handful of SGR runs keeps the rendered rule cheap for lipgloss's downstream
// width/wrap passes, which iterate every escape sequence; one sequence per cell
// would make the whole view measurably slower.
const gradientBands = 9

// gradientRule renders a horizontal rule of width cells blended across the
// theme's accent -> skill -> web hues: the single reusable gradient helper the
// idle banner and the chrome panel both draw through. Plain text under
// ansiStrip is exactly width rule glyphs, and a nil palette falls back to the
// plain rule, so the interpolation is stable at every width.
func (th Theme) gradientRule(width int) string {
	return th.gradientRuns(0, width, width)
}

// gradientKey identifies a cached band palette: the three blended palette
// colors. Themes are values, so the key packs their RGB rather than comparing
// interfaces.
type gradientKey struct {
	accent, skill, web uint32
}

// gradientBandColorsCache memoizes each palette's band colors so per-frame
// rules reuse them instead of re-interpolating.
var gradientBandColorsCache sync.Map // gradientKey -> []color.Color

// gradientBandColors returns the fixed band colors for the theme.
func (th Theme) gradientBandColors() []color.Color {
	key := gradientKey{colorKey(th.accent), colorKey(th.skill), colorKey(th.web)}
	if v, ok := gradientBandColorsCache.Load(key); ok {
		return v.([]color.Color)
	}
	colors := make([]color.Color, gradientBands)
	for i := range colors {
		colors[i] = gradientColorAt(th, float64(i)/float64(gradientBands-1))
	}
	actual, _ := gradientBandColorsCache.LoadOrStore(key, colors)
	return actual.([]color.Color)
}

// gradientBand returns the band index for a column, rounded to the nearest band
// so the ends land on the accent and web hues exactly.
func gradientBand(col, width int) int {
	if width <= 1 {
		return 0
	}
	return (col*(gradientBands-1) + (width-1)/2) / (width - 1)
}

// gradientRuns renders the rule cells for columns [start,end) as one SGR run
// per colour band, so the rendered rule carries a handful of escapes instead of
// one per cell. Results are memoized because the chrome panel top border is
// rebuilt several times per rendered frame.
func (th Theme) gradientRuns(start, end, width int) string {
	if start < 0 {
		start = 0
	}
	if end > width {
		end = width
	}
	if start >= end {
		return ""
	}
	if th.accent == nil || th.skill == nil || th.web == nil {
		return strings.Repeat(lookup("hr"), end-start)
	}
	key := gradientRunKey{
		palette: gradientKey{colorKey(th.accent), colorKey(th.skill), colorKey(th.web)},
		width:   width,
		start:   start,
		end:     end,
	}
	gradientRunsMu.Lock()
	if v, ok := gradientRunsCache[key]; ok {
		gradientRunsMu.Unlock()
		return v
	}
	gradientRunsMu.Unlock()

	colors := th.gradientBandColors()
	var b strings.Builder
	runStart := start
	runBand := gradientBand(start, width)
	for col := start + 1; col < end; col++ {
		band := gradientBand(col, width)
		if band != runBand {
			b.WriteString(styledRuleRun(colors[runBand], col-runStart))
			runStart = col
			runBand = band
		}
	}
	b.WriteString(styledRuleRun(colors[runBand], end-runStart))
	result := b.String()

	gradientRunsMu.Lock()
	if len(gradientRunsCache) < gradientRunsCacheCap {
		gradientRunsCache[key] = result
	}
	gradientRunsMu.Unlock()
	return result
}

// gradientRunKey identifies a cached rule run: the palette, total width, and
// the [start,end) column window.
type gradientRunKey struct {
	palette           gradientKey
	width, start, end int
}

var (
	gradientRunsMu    sync.Mutex
	gradientRunsCache = make(map[gradientRunKey]string)
)

// gradientRunsCacheCap bounds the run cache so a long session with many elapsed
// title widths cannot grow it without limit.
const gradientRunsCacheCap = 256

// styledRuleRun renders n rule glyphs in one SGR run.
func styledRuleRun(c color.Color, n int) string {
	return styledGlyph(c, strings.Repeat(lookup("hr"), n))
}

// styledGlyph wraps glyph in a single 24-bit foreground SGR run, the minimal
// escape shape for a theme-colored mark.
func styledGlyph(c color.Color, glyph string) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[m", r>>8, g>>8, b>>8, glyph)
}

// colorKey packs a color's 24-bit RGB for use as a cache key.
func colorKey(c color.Color) uint32 {
	r, g, b, _ := c.RGBA()
	return uint32(r>>8)<<16 | uint32(g>>8)<<8 | uint32(b>>8)
}

// gradientRune styles one rule cell (or border corner) with the theme hue at
// the cell's horizontal position, emitting a minimal SGR run.
func gradientRune(th Theme, glyph string, col, width int) string {
	t := 0.0
	if width > 1 {
		t = float64(col) / float64(width-1)
	}
	c := gradientColorAt(th, t)
	if c == nil {
		return glyph
	}
	return styledGlyph(c, glyph)
}

// gradientCorner styles the top-border corner at col (0 is the opening corner,
// any other column the closing one). Corners are uncached because a panel has
// only two of them.
func (th Theme) gradientCorner(col, width int) string {
	glyph := "╭"
	if col != 0 {
		glyph = "╮"
	}
	return gradientRune(th, glyph, col, width)
}

// brandWordmark renders the idle brand ("⚒️ Eitri") with a gradient across the
// theme's accent -> skill -> web hues, optionally carrying the idle ember's
// moving highlight at frame > 0. Plain text is always brandMark()+" Eitri"; a
// nil palette falls back to the flat header style.
func brandWordmark(th Theme, frame int) string {
	text := brandMark() + " Eitri"
	if th.accent == nil || th.skill == nil || th.web == nil {
		return th.headerStyle.Render(text)
	}
	// Style whole grapheme clusters (the VS16 brand mark included), never the
	// base rune alone, so the emoji keeps its two-cell width through the SGR.
	clusters := graphemeClusters(text)
	total := 0
	for _, c := range clusters {
		total += ansi.StringWidth(c)
	}
	active := frame > 0 && motionEnabled()
	var b strings.Builder
	col := 0
	for _, c := range clusters {
		t := 0.0
		if total > 1 {
			t = float64(col) / float64(total-1)
		}
		color := gradientColorAt(th, t)
		if active {
			if level, ok := emberLevel(frame, col, total); ok {
				color = lerpColor(color, lipgloss.Color("#FFFFFF"), level)
			}
		}
		b.WriteString(styledGlyph(color, c))
		col += ansi.StringWidth(c)
	}
	return b.String()
}

// graphemeClusters groups each base rune with its following VS16 variation
// selector, so styling a cluster cannot split the emoji presentation.
func graphemeClusters(s string) []string {
	var clusters []string
	var cur strings.Builder
	for _, r := range s {
		if r == '\ufe0f' && cur.Len() > 0 {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			clusters = append(clusters, cur.String())
			cur.Reset()
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		clusters = append(clusters, cur.String())
	}
	return clusters
}

// emberLevel returns the highlight intensity for the given frame at display
// column col, sweeping a three-cell ember across the wordmark and back. The
// window is bounded at the call site; this is the per-frame shape only.
func emberLevel(frame, col, total int) (float64, bool) {
	if total <= 2 {
		return 0, false
	}
	path := total*2 - 2
	step := frame % path
	pos := step
	if pos >= total {
		pos = path - pos
	}
	start := pos - 1
	if start < 0 {
		start = 0
	} else if start > total-3 {
		start = total - 3
	}
	levels := [3]float64{0.28, 0.5, 0.28}
	for i, level := range levels {
		if col == start+i {
			return level, true
		}
	}
	return 0, false
}
