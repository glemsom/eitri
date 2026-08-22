package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// splashModel builds a model with the launch splash enabled and a sized surface.
func splashModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Splash: true,
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "answer"}, nil
		},
	})
	if m.splash == nil {
		t.Fatalf("splash-enabled model must start with an active splash")
	}
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return asModel(t, nm)
}

func TestSplash_rendersWordmarkAndTagline(t *testing.T) {
	m := splashModel(t)
	for i := 0; i < splashTotalFrames-1; i++ {
		nm, _ := m.Update(splashTickMsg{})
		m = asModel(t, nm)
	}
	content := stripANSI(view(m))

	for _, want := range []string{"██████", "████"} {
		if !strings.Contains(content, want) {
			t.Errorf("splash wordmark missing %q", want)
		}
	}
	if !strings.Contains(content, "forging agents") {
		t.Errorf("splash missing emoji tagline, got:\n%s", content)
	}
}

// stripANSI removes SGR escape sequences so raw text assertions work on styled output.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\x1b':
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestSplash_ansiColorsPresent
func TestSplash_wordmarkUsesTrueColorGradient(t *testing.T) {
	first := lipgloss.NewStyle().Foreground(splashWordPalette[0]).Render("x")
	if !strings.Contains(first, "38;2;0;255;200") {
		t.Errorf("gradient must start at #00FFC8, got %q", first)
	}
	lastSGR := lipgloss.NewStyle().Foreground(splashWordPalette[len(splashWordPalette)-1]).Render("x")
	if !strings.Contains(lastSGR, "38;2;0;170;255") {
		t.Errorf("gradient must end at #00AAFF, got %q", lastSGR)
	}
	m := splashModel(t)
	for i := 0; i < splashTotalFrames-1; i++ {
		nm, _ := m.Update(splashTickMsg{})
		m = asModel(t, nm)
	}
	if !strings.Contains(view(m), "\x1b[38;2;") {
		t.Errorf("wordmark must use true-color SGR (\\x1b[38;2;), got plain/256-color text")
	}
}

func TestSplash_ansiColorsPresent(t *testing.T) {
	m := splashModel(t)
	if !strings.Contains(view(m), "\x1b[38;5;") {
		t.Errorf("splash must carry ANSI colors, got plain text")
	}
}

// TestSplash_wordmarkUsesRobustGlyphs guards against the "Fitri" misread:
// half-block box-drawing characters render inconsistently across terminal
// fonts, so the wordmark must only use █ and spaces.
func TestSplash_wordmarkUsesRobustGlyphs(t *testing.T) {
	banned := "╗╔╝╚═║║▔▁"
	for r, line := range eitriWordmark {
		for _, ch := range line {
			if strings.ContainsRune(banned, ch) {
				t.Errorf("wordmark row %d uses fragile glyph %q", r, ch)
			}
		}
	}
}

func TestSplash_rainBeforeWordmark(t *testing.T) {
	m := splashModel(t)
	if strings.Contains(view(m), "███████╗") {
		t.Fatalf("wordmark must stay hidden while the rain phase runs")
	}
	if !strings.Contains(view(m), "ᚠ") && !strings.Contains(view(m), "*") {
		t.Fatalf("rain phase must show falling glyphs")
	}
}

func TestSplash_settlesToIdleWelcomeAfterDuration(t *testing.T) {
	m := splashModel(t)
	var cmd tea.Cmd
	for i := 0; i < splashTotalFrames; i++ {
		var nm tea.Model
		nm, cmd = m.Update(splashTickMsg{})
		m = asModel(t, nm)
	}
	if m.splash != nil {
		t.Fatalf("splash must clear after %d frames", splashTotalFrames)
	}
	if cmd != nil {
		t.Errorf("finished splash must not re-issue ticks")
	}
	if !strings.Contains(view(m), "your terminal coding agent") {
		t.Errorf("after the splash the idle welcome must show, got:\n%s", view(m))
	}
}

func TestSplash_keypressSkips(t *testing.T) {
	m := splashModel(t)
	nm, _ := m.Update(splashTickMsg{})
	m = asModel(t, nm)

	nm, _ = m.Update(tea.KeyPressMsg{Code: 'x'})
	m = asModel(t, nm)
	if m.splash != nil {
		t.Fatalf("any keypress must skip the splash")
	}
	if !strings.Contains(view(m), "your terminal coding agent") {
		t.Errorf("skipped splash must settle into the idle welcome, got:\n%s", view(m))
	}
}

func TestSplash_disabledAndReducedMotion(t *testing.T) {
	m := NewModelCfg(Dependencies{Splash: false})
	if m.splash != nil {
		t.Fatalf("splash must default off")
	}

	t.Setenv("EITRI_NO_MOTION", "1")
	m = NewModelCfg(Dependencies{Splash: true})
	if m.splash != nil {
		t.Fatalf("reduced motion must skip the splash entirely")
	}
}
