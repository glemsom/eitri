package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// colorSGR renders the 24-bit foreground escape a theme color emits, so
// gradient assertions can name the exact hue without duplicating the blend.
func colorSGR(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r>>8, g>>8, b>>8)
}

func TestGradientRule_preservesPlainTextAtEveryWidth(t *testing.T) {
	t.Parallel()
	th := newDefaultTheme()
	for _, w := range []int{0, 1, 2, 5, 40, 120} {
		w := w
		t.Run(fmt.Sprintf("width/%d", w), func(t *testing.T) {
			got := th.gradientRule(w)
			if plain := ansiStrip(got); plain != strings.Repeat("─", w) {
				t.Errorf("gradientRule(%d) plain = %q, want %q", w, plain, strings.Repeat("─", w))
			}
			if width := lipgloss.Width(got); width != w {
				t.Errorf("gradientRule(%d) display width = %d, want %d", w, width, w)
			}
		})
	}
}

func TestGradientRule_blendsAccentToSkillToWeb(t *testing.T) {
	t.Parallel()
	th := newDefaultTheme()
	rule := th.gradientRule(7)
	cells := strings.Split(rule, "\x1b[m")
	if !strings.Contains(cells[0], colorSGR(th.accent)) {
		t.Errorf("gradientRule start = %q, want accent hue %q", cells[0], colorSGR(th.accent))
	}
	if last := cells[len(cells)-2]; !strings.Contains(last, colorSGR(th.web)) {
		t.Errorf("gradientRule end = %q, want web hue %q", last, colorSGR(th.web))
	}
	if !strings.Contains(rule, colorSGR(th.skill)) {
		t.Errorf("gradientRule = %q, want the skill hue %q somewhere", rule, colorSGR(th.skill))
	}
}

// bundledThemeNames is every explicit palette a user can select. "auto"
// resolves through the terminal environment and is covered by the theme
// resolution tests, so the gradient's per-palette contract runs over these.
var bundledThemeNames = []string{
	"dark", "dracula", "tokyo-night", "pink", "light", "nord",
	"gruvbox", "solarized", "dark-daltonized", "light-daltonized",
}

func TestGradientRule_everyBundledThemeBlendsThreeHues(t *testing.T) {
	t.Parallel()
	const width = 40
	for _, name := range bundledThemeNames {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			th := themeFor(name)
			rule := th.gradientRule(width)
			if plain := ansiStrip(rule); plain != strings.Repeat("─", width) {
				t.Fatalf("plain = %q, want %d rule cells", plain, width)
			}
			if w := lipgloss.Width(rule); w != width {
				t.Fatalf("display width = %d, want %d", w, width)
			}
			for _, hue := range []color.Color{th.accent, th.skill, th.web} {
				if !strings.Contains(rule, colorSGR(hue)) {
					t.Errorf("gradient for %s missing hue %s", name, colorSGR(hue))
				}
			}
		})
	}
}

func TestGradientRule_nilPaletteFallsBackToPlainRule(t *testing.T) {
	t.Parallel()
	th := renderSurfaceTestTheme()
	got := th.gradientRule(10)
	if got != strings.Repeat("─", 10) {
		t.Errorf("gradientRule on a nil palette = %q, want a plain rule", got)
	}
}

func TestGradientRule_singleCellIsAccent(t *testing.T) {
	t.Parallel()
	th := newDefaultTheme()
	if got := th.gradientRule(1); !strings.Contains(got, colorSGR(th.accent)) {
		t.Errorf("gradientRule(1) = %q, want accent hue", got)
	}
}

func TestBrandWordmark_preservesPlainText(t *testing.T) {
	t.Parallel()
	th := newDefaultTheme()
	for _, frame := range []int{0, 1, 7, 40} {
		got := brandWordmark(th, frame)
		if plain := ansiStrip(got); plain != brandMark()+" Eitri" {
			t.Errorf("brandWordmark(frame=%d) plain = %q, want %q", frame, plain, brandMark()+" Eitri")
		}
		if width := lipgloss.Width(got); width != lipgloss.Width(brandMark()+" Eitri") {
			t.Errorf("brandWordmark(frame=%d) width = %d, want %d", frame, width, lipgloss.Width(brandMark()+" Eitri"))
		}
	}
}

func TestBrandWordmark_settledCarriesGradient(t *testing.T) {
	t.Parallel()
	th := newDefaultTheme()
	got := brandWordmark(th, 0)
	cells := strings.Split(got, "\x1b[m")
	if !strings.Contains(cells[0], colorSGR(th.accent)) {
		t.Errorf("brand start = %q, want accent %q", cells[0], colorSGR(th.accent))
	}
	if last := cells[len(cells)-2]; !strings.Contains(last, colorSGR(th.web)) {
		t.Errorf("brand end = %q, want web %q", last, colorSGR(th.web))
	}
}

func TestBrandWordmark_emberFrameShiftsHighlightWithoutChangingText(t *testing.T) {
	t.Parallel()
	th := newDefaultTheme()
	settled := brandWordmark(th, 0)
	ember := brandWordmark(th, 5)
	if settled == ember {
		t.Fatal("a mid-ember frame must change the rendered brand")
	}
	if ansiStrip(settled) != ansiStrip(ember) {
		t.Errorf("ember changed plain text: %q vs %q", ansiStrip(settled), ansiStrip(ember))
	}
}

func TestBrandWordmark_nilPaletteFallsBackToHeaderStyle(t *testing.T) {
	t.Parallel()
	th := renderSurfaceTestTheme()
	if got := brandWordmark(th, 9); got != "⚒️ Eitri" {
		t.Errorf("brandWordmark on a nil palette = %q, want plain brand", got)
	}
}
