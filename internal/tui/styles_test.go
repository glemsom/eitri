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

// TestTheme_draculaPalette asserts the second curated palette (issue #179

// TestTheme_railHues asserts every theme carries three distinct per-section
// hues for the right context rail (issue #182): the STATS / CONTEXT / MODEL
// sections each draw a distinct hue from the theme's palette registry, and the
// derived rail styles render headers and bodies from those hues — so a palette
// addition alone re-skins the rail. The SKILLS section hue is gone with the
// section (issue #188).
func TestTheme_railHues(t *testing.T) {
	themes := []Theme{defaultTheme, newDraculaTheme(), newTokyoNightTheme(), newPinkTheme(), newLightTheme()}
	for _, th := range themes {
		// Three distinct hues, each a hex-derived color that adapts to any
		// color profile (issue #182 AC5: safe fallback on non-truecolor).
		seen := map[color.Color]bool{}
		for i, c := range th.railHues {
			if seen[c] {
				t.Errorf("theme %v rail hue %d duplicates another section hue", th, i)
			}
			seen[c] = true
			if _, ok := c.(color.RGBA); !ok {
				t.Errorf("theme %v rail hue %d = %T, want a hex-derived color.RGBA", th, i, c)
			}
		}
		// The derived styles draw from the palette entries.
		for _, s := range []railSection{railStats, railContext, railModel} {
			if got := th.railHeaderStyles[s].GetForeground(); got != th.railHues[s] {
				t.Errorf("theme %v rail header style %d foreground = %v, want hue %v", th, s, got, th.railHues[s])
			}
			if got := th.railBodyStyles[s].GetForeground(); got != th.railHues[s] {
				t.Errorf("theme %v rail body style %d foreground = %v, want hue %v", th, s, got, th.railHues[s])
			}
		}
	}
}

// AC3) is a full Theme value on the same constructor pattern as the default:
// distinct hex palette entries and the derived styles drawn from them, so a
// non-default theme fully re-skins the chrome.
func TestTheme_draculaPalette(t *testing.T) {
	th := newDraculaTheme()

	for name, want := range map[string]color.Color{
		"accent": lipgloss.Color("#BD93F9"),
		"error":  lipgloss.Color("#FF5555"),
		"ok":     lipgloss.Color("#50FA7B"),
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
		if _, ok := color.Color(got).(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, color.Color(got))
		}
	}

	if got := th.agentPaneStyle.GetBorderLeftForeground(); got != th.accent {
		t.Errorf("agent pane border foreground = %v, want accent %v", got, th.accent)
	}
}

// TestTheme_tokyoNightPalette asserts the tokyo-night chrome palette (issue
// #180): the canonical tokyo-night hues — purple accent (glamour's heading
// color for the theme), red error, green ok — as a full Theme value on the
// same constructor pattern, so tokyo-night reads as one surface with its
// Markdown counterpart instead of inheriting the default chrome.
func TestTheme_tokyoNightPalette(t *testing.T) {
	th := newTokyoNightTheme()

	for name, want := range map[string]color.Color{
		"accent": lipgloss.Color("#BB9AF7"),
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
		if _, ok := color.Color(got).(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, color.Color(got))
		}
	}

	if got := th.agentPaneStyle.GetBorderLeftForeground(); got != th.accent {
		t.Errorf("agent pane border foreground = %v, want accent %v", got, th.accent)
	}
}

// TestTheme_pinkPalette asserts the pink chrome palette (issue #180): the
// glamour pink theme's hot-pink heading hue as the accent, with a crimson
// error and a soft green ok — ✓/✗ outcomes and the error pane stay
// distinguishable from the pink accent, so pink chrome reads as one surface
// with the pink Markdown theme.
func TestTheme_pinkPalette(t *testing.T) {
	th := newPinkTheme()

	for name, want := range map[string]color.Color{
		"accent": lipgloss.Color("#FF87D7"),
		"error":  lipgloss.Color("#E5484D"),
		"ok":     lipgloss.Color("#69DB8C"),
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
		if _, ok := color.Color(got).(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, color.Color(got))
		}
	}

	if got := th.errorPaneStyle.GetBorderLeftForeground(); got != th.error {
		t.Errorf("error pane border foreground = %v, want error %v", got, th.error)
	}
	if got := th.outcomeOKStyle.GetForeground(); got != th.ok {
		t.Errorf("ok outcome foreground = %v, want ok %v", got, th.ok)
	}
}

// TestTheme_lightPalette asserts the light chrome palette (issue #180 AC3):
// hues readable on a light terminal background — the glamour light theme's
// heading blue as the accent, with a dark red error and a dark teal-green ok,
// each contrast-checked against white (≥ 4.5:1) — so light terminals get a
// light-surface chrome instead of the default dark one.
func TestTheme_lightPalette(t *testing.T) {
	th := newLightTheme()

	for name, want := range map[string]color.Color{
		"accent": lipgloss.Color("#005FFF"),
		"error":  lipgloss.Color("#C92A2A"),
		"ok":     lipgloss.Color("#00875F"),
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
		if _, ok := color.Color(got).(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, color.Color(got))
		}
	}

	if got := th.headerStyle.GetForeground(); got != th.accent {
		t.Errorf("header foreground = %v, want accent %v", got, th.accent)
	}
}

// TestThemeFor_auto asserts the "auto" theme resolves its chrome palette by
// the terminal background exactly like the renderer (issue #180 AC3): "auto"
// delegates to the same resolution autoTheme() computes for Markdown, so the
// chrome and Markdown always agree — light terminal → light palette, dark
// terminal → default dark palette. The assertion compares against the
// resolved theme rather than a hardcoded palette because autoTheme() queries
// the ambient terminal.
func TestThemeFor_auto(t *testing.T) {
	if got := themeFor("auto").accent; got != themeFor(autoTheme()).accent {
		t.Errorf("themeFor(auto) accent = %v, want resolved theme %q accent %v", got, autoTheme(), themeFor(autoTheme()).accent)
	}
}

// TestThemeFor_mapsConfigNames asserts the config-theme → chrome-palette map
// (issue #179 AC1/AC4, extended by issue #180): "dracula" selects the second
// curated palette; "tokyo-night" selects the tokyo-night palette; "pink" and
// "light" select their curated palettes (light-terminal resolution for the
// latter is asserted in TestThemeFor_auto); "notty" keeps the default palette
// deliberately (unreachable in the TUI — the boot guard refuses
// non-interactive contexts); an unknown value falls back to default, matching
// the renderer.
func TestThemeFor_mapsConfigNames(t *testing.T) {
	for name, want := range map[string]color.Color{
		"dracula":     lipgloss.Color("#BD93F9"),
		"tokyo-night": lipgloss.Color("#BB9AF7"),
		"pink":        lipgloss.Color("#FF87D7"),
		"light":       lipgloss.Color("#005FFF"),
		"dark":        defaultTheme.accent,
		"notty":       defaultTheme.accent,
		"bogus":       defaultTheme.accent,
		"":            defaultTheme.accent,
	} {
		if got := themeFor(name).accent; got != want {
			t.Errorf("themeFor(%q) accent = %v, want %v", name, got, want)
		}
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

// TestTheme_toolCategoryPalettes asserts every curated palette carries the four
// tool-category entries the transcript colorizes by (issue #181 AC1): shell,
// file, web and skill, each a hex value distinct from the theme's accent,
// error and ok entries so a long session skims by category color. The
// light palette's category hues are additionally contrast-checked against
// white (≥ 4.5:1), matching its palette-level constraint.
func TestTheme_toolCategoryPalettes(t *testing.T) {
	for name, want := range map[string]map[string]color.Color{
		"default": {
			"shell": lipgloss.Color("#E0AF68"),
			"file":  lipgloss.Color("#7DCFFF"),
			"web":   lipgloss.Color("#BB9AF7"),
			"skill": lipgloss.Color("#FF87D7"),
		},
		"dracula": {
			"shell": lipgloss.Color("#FFB86C"),
			"file":  lipgloss.Color("#8BE9FD"),
			"web":   lipgloss.Color("#FF79C6"),
			"skill": lipgloss.Color("#F1FA8C"),
		},
		"tokyo-night": {
			"shell": lipgloss.Color("#FF9E64"),
			"file":  lipgloss.Color("#7DCFFF"),
			"web":   lipgloss.Color("#2AC3DE"),
			"skill": lipgloss.Color("#73DACA"),
		},
		"pink": {
			"shell": lipgloss.Color("#FFB224"),
			"file":  lipgloss.Color("#39C0ED"),
			"web":   lipgloss.Color("#A78BFA"),
			"skill": lipgloss.Color("#60A5FA"),
		},
		"light": {
			"shell": lipgloss.Color("#B45309"),
			"file":  lipgloss.Color("#0E7490"),
			"web":   lipgloss.Color("#6D28D9"),
			"skill": lipgloss.Color("#A21CAF"),
		},
	} {
		th := themeFor(name)
		for cat, want := range want {
			var got color.Color
			switch cat {
			case "shell":
				got = th.shell
			case "file":
				got = th.file
			case "web":
				got = th.web
			case "skill":
				got = th.skill
			}
			if got != want {
				t.Errorf("%s %s = %v, want %v", name, cat, got, want)
			}
			if _, ok := color.Color(got).(color.RGBA); !ok {
				t.Errorf("%s %s color = %T, want a hex-derived color.RGBA", name, cat, color.Color(got))
			}
		}
		// The seven palette entries are mutually distinct within a theme: a
		// category hue must never collide with the accent/error/ok trio or
		// another category, or the transcript would stop skimming by color
		// (issue #181 AC1).
		seen := map[color.Color]string{}
		entries := map[string]color.Color{
			"accent": th.accent, "error": th.error, "ok": th.ok,
			"shell": th.shell, "file": th.file, "web": th.web, "skill": th.skill,
		}
		for entry, c := range entries {
			if prev, dup := seen[c]; dup {
				t.Errorf("%s %s collides with %s: both %v", name, entry, prev, c)
			}
			seen[c] = entry
		}
	}
}

// TestTheme_toolCategoryStyles asserts the derived tool styles draw from the
// category palette entries (issue #181 AC1): each of the four tool styles
// carries its category hue, so the renderer styles a tool line by category
// through the seam — no hardcoded color outside the palette registry.
func TestTheme_toolCategoryStyles(t *testing.T) {
	th := defaultTheme
	for cat, want := range map[string]color.Color{
		"shell": th.shell,
		"file":  th.file,
		"web":   th.web,
		"skill": th.skill,
	} {
		var got color.Color
		switch cat {
		case "shell":
			got = th.toolShellStyle.GetForeground()
		case "file":
			got = th.toolFileStyle.GetForeground()
		case "web":
			got = th.toolWebStyle.GetForeground()
		case "skill":
			got = th.toolSkillStyle.GetForeground()
		}
		if got != want {
			t.Errorf("tool %s style foreground = %v, want %v", cat, got, want)
		}
	}
	// The generic tool line stays faint; the thinking hint renders italic so
	// the 🤔 block reads as a distinct treatment from the answer body (issue
	// #181 AC2).
	if got := th.toolStyle.GetFaint(); !got {
		t.Errorf("generic tool style should stay faint, got %v", got)
	}
	if got := th.thinkingStyle.GetItalic(); !got {
		t.Errorf("thinking style should be italic (distinct from answers), got %v", got)
	}
	if got := th.thinkingStyle.GetForeground(); got != th.accent {
		t.Errorf("thinking foreground = %v, want accent %v", got, th.accent)
	}
}
