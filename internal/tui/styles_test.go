package tui

import (
	"context"
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestTheme_defaultPalette(t *testing.T) {
	t.Parallel()
	th := defaultTheme

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
		if _, ok := color.Color(got).(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, color.Color(got))
		}
	}

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

func TestTheme_railHues(t *testing.T) {
	t.Parallel()
	themes := []Theme{defaultTheme, newDraculaTheme(), newTokyoNightTheme(), newPinkTheme(), newLightTheme()}
	for _, th := range themes {
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

func TestTheme_draculaPalette(t *testing.T) {
	t.Parallel()
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

func TestTheme_tokyoNightPalette(t *testing.T) {
	t.Parallel()
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

func TestTheme_pinkPalette(t *testing.T) {
	t.Parallel()
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

func TestTheme_lightPalette(t *testing.T) {
	t.Parallel()
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

func TestThemeFor_auto(t *testing.T) {
	t.Parallel()
	if got := themeFor("auto").accent; got != themeFor(autoTheme()).accent {
		t.Errorf("themeFor(auto) accent = %v, want resolved theme %q accent %v", got, autoTheme(), themeFor(autoTheme()).accent)
	}
}

func TestThemeFor_mapsConfigNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]color.Color{
		"dracula":          lipgloss.Color("#BD93F9"),
		"tokyo-night":      lipgloss.Color("#BB9AF7"),
		"pink":             lipgloss.Color("#FF87D7"),
		"light":            lipgloss.Color("#005FFF"),
		"nord":             lipgloss.Color("#88C0D0"),
		"gruvbox":          lipgloss.Color("#83A598"),
		"solarized":        lipgloss.Color("#268BD2"),
		"dark-daltonized":  lipgloss.Color("#56B4E9"),
		"light-daltonized": lipgloss.Color("#0072B2"),
		"dark":             defaultTheme.accent,
		"notty":            defaultTheme.accent,
		"bogus":            defaultTheme.accent,
		"":                 defaultTheme.accent,
	} {
		if got := themeFor(name).accent; got != want {
			t.Errorf("themeFor(%q) accent = %v, want %v", name, got, want)
		}
	}
}

func TestTheme_streamingPaneStyle(t *testing.T) {
	t.Parallel()
	th := defaultTheme
	if got := th.streamingPaneStyle.GetBorderLeftForeground(); got == nil {
		t.Fatal("streaming pane style border foreground is nil")
	}
	if got := th.streamingPaneStyle.GetBorderLeftForeground(); got == th.accent {
		t.Errorf("streaming pane border foreground must differ from agent pane accent, got same %v", got)
	}
}

func TestTheme_streamingErrorPaneStyle(t *testing.T) {
	t.Parallel()
	th := defaultTheme
	if got := th.streamingErrorPaneStyle.GetBorderLeftForeground(); got == nil {
		t.Fatal("streaming error pane style border foreground is nil")
	}
	if got := th.streamingErrorPaneStyle.GetBorderLeftForeground(); got == th.error {
		t.Errorf("streaming error pane border foreground must differ from error pane, got same %v", got)
	}
}

func TestTheme_streamingPaneDistinctAcrossThemes(t *testing.T) {
	t.Parallel()
	themes := map[string]Theme{
		"default":     defaultTheme,
		"dracula":     newDraculaTheme(),
		"tokyo-night": newTokyoNightTheme(),
	}
	seen := map[color.Color]string{}
	for name, th := range themes {
		got := th.streamingPaneStyle.GetBorderLeftForeground()
		if got == nil {
			t.Errorf("%s: streaming pane border foreground is nil", name)
			continue
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("%s streaming pane color %v collides with %s", name, got, prev)
		}
		seen[got] = name
	}
}

func TestTheme_thinkingPaneDistinctFromAgent(t *testing.T) {
	t.Parallel()
	th := defaultTheme

	agentBorder := th.agentPaneStyle.GetBorderLeftForeground()
	thinkBorder := th.thinkingPaneStyle.GetBorderLeftForeground()
	if thinkBorder == nil {
		t.Fatal("thinking pane style border foreground is nil")
	}
	if thinkBorder == agentBorder {
		t.Errorf("thinking pane border foreground must differ from agent pane, got same %v", thinkBorder)
	}
	if !th.thinkingPaneStyle.GetItalic() {
		t.Errorf("thinking pane should be italic (internal monologue), got %v", th.thinkingPaneStyle.GetItalic())
	}

	streamBorder := th.streamingThinkingPaneStyle.GetBorderLeftForeground()
	if streamBorder == nil {
		t.Fatal("streaming thinking pane style border foreground is nil")
	}
	if streamBorder == agentBorder {
		t.Errorf("streaming thinking pane border foreground must differ from agent pane, got same %v", streamBorder)
	}
	if !th.streamingThinkingPaneStyle.GetItalic() {
		t.Errorf("streaming thinking pane should be italic, got %v", th.streamingThinkingPaneStyle.GetItalic())
	}
}

func TestTheme_thinkingPanePresentAcrossPalettes(t *testing.T) {
	t.Parallel()
	themes := map[string]Theme{
		"default":          defaultTheme,
		"dracula":          newDraculaTheme(),
		"tokyo-night":      newTokyoNightTheme(),
		"pink":             newPinkTheme(),
		"light":            newLightTheme(),
		"nord":             newNordTheme(),
		"gruvbox":          newGruvboxTheme(),
		"solarized":        newSolarizedTheme(),
		"dark-daltonized":  newDarkDaltonizedTheme(),
		"light-daltonized": newLightDaltonizedTheme(),
	}
	for name, th := range themes {
		agentBorder := th.agentPaneStyle.GetBorderLeftForeground()
		if thinkBorder := th.thinkingPaneStyle.GetBorderLeftForeground(); thinkBorder == nil {
			t.Errorf("%s: thinking pane border foreground is nil", name)
		} else if thinkBorder == agentBorder {
			t.Errorf("%s: thinking pane border must differ from agent pane", name)
		}
		if got := th.streamingThinkingPaneStyle.GetBorderLeftForeground(); got == nil {
			t.Errorf("%s: streaming thinking pane border foreground is nil", name)
		}
	}
}

func TestModel_themeSeam(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	pane := lineContaining(view(m), "plain")
	if pane == "" {
		t.Fatalf("expected agent answer in view, got: %q", view(m))
	}
	if !strings.Contains(pane, "\x1b[38;2;122;162;247m") {
		t.Errorf("default-theme pane must render the default accent border, got: %q", pane)
	}

	alt := defaultTheme
	alt.accent = lipgloss.Color("#FF0000")
	alt.agentPaneStyle = borderedPane(alt.accent)
	m.tx.theme = alt

	pane = lineContaining(view(m), "plain")
	if !strings.Contains(pane, "\x1b[38;2;255;0;0m") {
		t.Errorf("swapped theme must re-color the agent pane border, got: %q", pane)
	}
	if strings.Contains(pane, "\x1b[38;2;122;162;247m") {
		t.Errorf("default accent leaked after theme swap, got: %q", pane)
	}
}

func TestTheme_toolCategoryPalettes(t *testing.T) {
	t.Parallel()
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

func TestTheme_toolCategoryStyles(t *testing.T) {
	t.Parallel()
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
