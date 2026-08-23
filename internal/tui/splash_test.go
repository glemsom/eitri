package tui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
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
	content := stripANSI(finalSplashView(t))

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

// TestSplash_wordmarkUsesTrueColorGradient
func TestSplash_wordmarkUsesTrueColorGradient(t *testing.T) {
	first := lipgloss.NewStyle().Foreground(splashWordPalette[0]).Render("x")
	if !strings.Contains(first, "38;2;0;255;200") {
		t.Errorf("gradient must start at #00FFC8, got %q", first)
	}
	lastSGR := lipgloss.NewStyle().Foreground(splashWordPalette[len(splashWordPalette)-1]).Render("x")
	if !strings.Contains(lastSGR, "38;2;0;170;255") {
		t.Errorf("gradient must end at #00AAFF, got %q", lastSGR)
	}
	if !strings.Contains(finalSplashView(t), "\x1b[38;2;") {
		t.Errorf("wordmark must use true-color SGR (\\x1b[38;2;), got plain/256-color text")
	}
}

// finalSplashView advances a fresh splash-enabled model to the last frame and
// returns its view, where the wordmark is fully revealed.
func finalSplashView(t *testing.T) string {
	t.Helper()
	m := splashModel(t)
	for i := 0; i < splashTotalFrames-1; i++ {
		nm, _ := m.Update(splashTickMsg{})
		m = asModel(t, nm)
	}
	return view(m)
}

func TestSplash_ansiColorsPresent(t *testing.T) {
	m := splashModel(t)
	if !strings.Contains(view(m), "\x1b[38;5;") {
		t.Errorf("splash must carry ANSI colors, got plain text")
	}
}

// TestSplash_ansi256Fallback verifies the terminal-side fallback: pushing a
// wordmark gradient color through colorprofile.Writer with an ANSI256 profile
// downgrades the true-color SGR to a 256-color one.
func TestSplash_ansi256Fallback(t *testing.T) {
	var buf bytes.Buffer
	w := colorprofile.NewWriter(&buf, nil)
	w.Profile = colorprofile.ANSI256
	if _, err := fmt.Fprint(w, lipgloss.NewStyle().Foreground(splashWordPalette[0]).Render("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[38;2;") {
		t.Errorf("ANSI256 profile must downgrade true-color SGR, got %q", out)
	}
	if !strings.Contains(out, "\x1b[38;5;") {
		t.Errorf("ANSI256 profile must emit 256-color SGR, got %q", out)
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

// splashFrameView advances a fresh splash to the given frame and returns its view.
func splashFrameView(t *testing.T, frame int) string {
	t.Helper()
	m := splashModel(t)
	for i := 0; i < frame; i++ {
		nm, _ := m.Update(splashTickMsg{})
		m = asModel(t, nm)
	}
	return view(m)
}

// flashBarSGR is the true-color background SGR for #00FFC8; the flash renders
// as a solid bar (cyan-backed spaces), which no other splash element uses.
const flashBarSGR = "\x1b[48;2;0;255;200"

// TestSplash_convergenceFlash verifies the one-frame ignition flash at
// splashWordmarkStartFrame: a full-width bright cyan bar across the wordmark
// row that exists for exactly that single frame.
func TestSplash_convergenceFlash(t *testing.T) {
	if strings.Contains(splashFrameView(t, splashWordmarkStartFrame-1), flashBarSGR) {
		t.Errorf("flash bar must be absent before frame %d", splashWordmarkStartFrame)
	}

	flash := splashFrameView(t, splashWordmarkStartFrame)
	if !strings.Contains(flash, flashBarSGR) {
		t.Errorf("frame %d must carry the bright cyan flash bar (#00FFC8), got:\n%s", splashWordmarkStartFrame, stripANSI(flash))
	}

	after := splashFrameView(t, splashWordmarkStartFrame+1)
	if strings.Contains(after, flashBarSGR) {
		t.Errorf("flash bar must vanish after frame %d", splashWordmarkStartFrame)
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
	if cmd != nil && cmd() != nil {
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
