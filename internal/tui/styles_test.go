package tui

import (
	"context"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The theme seam (issue #178) converts the hardcoded lipgloss style globals
// into a Theme struct backed by a palette registry: the default theme carries
// exactly the pre-seam palette, and every chrome consumer renders through the
// model's theme so a second palette is a registry addition, not a refactor.
// These tests assert the seam through the registry (styles.go) and the render
// surface (View), never through internal plumbing.

// TestTheme_defaultPalette asserts the default theme registry carries exactly
// the pre-seam palette entries as hex colors (issue #178 AC1/AC2): accent,
// error and ok, each a "#RRGGBB" value lipgloss adapts to any color profile,
// plus the derived styles drawn from them.
func TestTheme_defaultPalette(t *testing.T) {
	th := defaultTheme

	// The palette entries are the pre-seam hex colors, verbatim.
	for name, want := range map[string]color.Color{
		"accent": lipgloss.Color("#7AA2F7"),
		"error":  lipgloss.Color("#F7768E"),
		"ok":     lipgloss.Color("#9ECE6A"),
	} {
		var got color.Color
		switch name {
		case "accent":
			got = th.accent
		case "error":
			got = th.error
		case "ok":
			got = th.ok
		}
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
		// Every palette entry is a hex color: lipgloss v2 maps a "#RRGGBB"
		// string to a concrete color.RGBA value, which adapts to any color
		// profile (issue #122 AC4/AC5).
		if _, ok := color.Color(got).(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, color.Color(got))
		}
	}

	// The derived styles draw from the palette entries.
	if got := th.agentPaneStyle.GetBorderLeftForeground(); got != th.accent {
		t.Errorf("agent pane border foreground = %v, want accent %v", got, th.accent)
	}
	if got := th.errorPaneStyle.GetBorderLeftForeground(); got != th.error {
		t.Errorf("error pane border foreground = %v, want error %v", got, th.error)
	}
	if got := th.outcomeOKStyle.GetForeground(); got != th.ok {
		t.Errorf("ok outcome foreground = %v, want ok %v", got, th.ok)
	}
	if got := th.outcomeErrStyle.GetForeground(); got != th.error {
		t.Errorf("error outcome foreground = %v, want error %v", got, th.error)
	}
}

// TestModel_themeSeam asserts the TUI chrome renders through the model's theme
// seam (issue #178 AC2/AC4): swapping the model's theme for one with a
// distinct accent re-colors the agent pane border, the default accent never
// leaks through, and the swap needs no consumer change — the seam is the
// model field, not the render code.
func TestModel_themeSeam(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	// Default theme: the agent pane border carries the default accent's
	// truecolor escape (#7AA2F7 → 38;2;122;162;247).
	pane := lineContaining(view(m), "plain")
	if pane == "" {
		t.Fatalf("expected agent answer in view, got: %q", view(m))
	}
	if !strings.Contains(pane, "\x1b[38;2;122;162;247m") {
		t.Errorf("default-theme pane must render the default accent border, got: %q", pane)
	}

	// An alternate palette re-colors the chrome through the same render path.
	alt := defaultTheme
	alt.accent = lipgloss.Color("#FF0000")
	alt.agentPaneStyle = borderedPane(alt.accent)
	m.theme = alt

	pane = lineContaining(view(m), "plain")
	if !strings.Contains(pane, "\x1b[38;2;255;0;0m") {
		t.Errorf("swapped theme must re-color the agent pane border, got: %q", pane)
	}
	if strings.Contains(pane, "\x1b[38;2;122;162;247m") {
		t.Errorf("default accent leaked after theme swap, got: %q", pane)
	}
}
