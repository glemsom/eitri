package tui

import (
	"fmt"
	"image/color"
	"slices"
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
var bundledThemeNames = slices.DeleteFunc(slices.Clone(supportedThemes), func(name string) bool {
	return name == "auto"
})

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

// TestRenderTitledPanel_gradientContractEveryTheme guards the chrome panel's
// top border: every band panel built on the titled-panel primitive draws the
// same accent -> web sweep as the idle banner, on every bundled palette and at
// narrow and wide widths.
func TestRenderTitledPanel_gradientContractEveryTheme(t *testing.T) {
	t.Parallel()
	const title = "Commands"
	// wantSkill is only asserted where the rule carries every quantised band and
	// the title does not cover the middle one; a claim of all three hues at every
	// width would pass by hiding the skill band behind the title.
	widths := []struct {
		width     int
		wantTitle bool
		wantSkill bool
	}{
		{width: 8, wantTitle: false, wantSkill: false},
		{width: 20, wantTitle: true, wantSkill: false},
		{width: 100, wantTitle: true, wantSkill: true},
	}
	for _, name := range bundledThemeNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			th := themeFor(name)
			for _, tc := range widths {
				t.Run(fmt.Sprintf("width/%d", tc.width), func(t *testing.T) {
					t.Parallel()
					border := strings.Split(renderTitledPanel(th, title, tc.width, th.bandSeparatorStyle, "body"), "\n")[0]
					if got := lipgloss.Width(border); got != tc.width {
						t.Fatalf("top border width = %d, want %d: %q", got, tc.width, border)
					}
					plain := ansiStrip(border)
					if !strings.HasPrefix(plain, "╭") || !strings.HasSuffix(plain, "╮") {
						t.Fatalf("top border plain = %q, want ╭...╮", plain)
					}
					if got := strings.Contains(plain, title); got != tc.wantTitle {
						t.Errorf("top border plain = %q, title shown = %v, want %v", plain, got, tc.wantTitle)
					}
					hues := []color.Color{th.accent, th.web}
					if tc.wantSkill {
						hues = append(hues, th.skill)
					}
					for _, hue := range hues {
						if !strings.Contains(border, colorSGR(hue)) {
							t.Errorf("gradient for %s at width %d missing hue %s: %q", name, tc.width, colorSGR(hue), border)
						}
					}
				})
			}
		})
	}
}

func TestRenderTitledPanel_nilPaletteFallsBackToPlainBorder(t *testing.T) {
	t.Parallel()
	th := renderSurfaceTestTheme()
	got := renderTitledPanel(th, "Commands", 20, th.bandSeparatorStyle, "body")
	if strings.Contains(got, "\x1b[") {
		t.Errorf("nil palette top border must carry no SGR, got %q", got)
	}
	border := strings.Split(got, "\n")[0]
	if want := "╭─ Commands ───────╮"; border != want {
		t.Errorf("nil palette top border = %q, want %q", border, want)
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
