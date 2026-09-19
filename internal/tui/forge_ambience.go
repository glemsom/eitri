package tui

import (
	"fmt"
	"image/color"
	"strings"

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

// gradientRule renders a horizontal rule of width cells blended across the
// theme's accent -> skill -> web hues: the single reusable gradient helper the
// idle banner and the chrome panel both draw through. Plain text under
// ansiStrip is exactly width rule glyphs, and a nil palette falls back to the
// plain rule, so the interpolation is stable at every width.
func (th Theme) gradientRule(width int) string {
	if width <= 0 {
		return ""
	}
	if th.accent == nil || th.skill == nil || th.web == nil {
		return strings.Repeat(lookup("hr"), width)
	}
	var b strings.Builder
	for col := range width {
		b.WriteString(gradientRune(th, lookup("hr"), col, width))
	}
	return b.String()
}

// gradientRune styles one rule cell (or border corner) with the theme hue at
// the cell's horizontal position.
func gradientRune(th Theme, glyph string, col, width int) string {
	t := 0.0
	if width > 1 {
		t = float64(col) / float64(width-1)
	}
	c := gradientColorAt(th, t)
	if c == nil {
		return glyph
	}
	return lipgloss.NewStyle().Foreground(c).Render(glyph)
}

// gradientCell styles the top-border cell at col: a corner at either end, a
// horizontal rule between.
func (th Theme) gradientCell(col, width int) string {
	glyph := lookup("hr")
	switch col {
	case 0:
		glyph = "╭"
	case width - 1:
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
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render(c))
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
