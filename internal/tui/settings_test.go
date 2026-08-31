package tui

import (
	"math"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

func cfgFixture() config.Config {
	return config.Config{
		Provider:                      "opencode-go",
		Model:                         "deepseek-v4-flash",
		ReasoningEffort:               "high",
		ThinkingEnabled:               true,
		MaxTurns:                      250,
		CompactionFraction:            0.8,
		ExtraWritablePaths:            []string{"/srv"},
		Theme:                         config.DefaultTheme,
		CoTCollapsedByDefault:         true,
		ToolResultsCollapsedByDefault: true,
	}
}

func nearEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSettingsForm_ModelStartsWithConfigured(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{"deepseek-v4-flash", "grok-2", "kimi"})
	if got := f.Model(); got != "deepseek-v4-flash" {
		t.Fatalf("Model() = %q, want configured deepseek-v4-flash", got)
	}
}

func TestSettingsForm_ModelFallsBackToConfiguredWhenNoneDiscovered(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), nil)
	if got := f.Model(); got != "deepseek-v4-flash" {
		t.Fatalf("Model() = %q, want configured deepseek-v4-flash (no discovery)", got)
	}
}

func TestSettingsForm_ThemeAdjustReskinsPanel(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), nil)
	f.field = fieldTheme
	f.adjust(1)
	if got := f.theme.accent; got != lipgloss.Color("#005FFF") {
		t.Fatalf("light theme accent = %v, want light palette accent", got)
	}
	f.adjust(1)
	if f.cfg.Theme != "dracula" {
		t.Fatalf("cfg.Theme = %q, want dracula", f.cfg.Theme)
	}
	if got := f.theme.accent; got != lipgloss.Color("#BD93F9") {
		t.Fatalf("dracula theme accent = %v, want dracula accent", got)
	}
}

func TestSettingsForm_AdjustsKnobs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		field  int
		dir    int
		verify func(config.Config) bool
	}{
		{"provider+", fieldProvider, 1, func(c config.Config) bool { return c.Provider == "github-copilot" }},
		{"provider-", fieldProvider, -1, func(c config.Config) bool { return c.Provider == "custom-openai" }},
		{"effort+", fieldEffort, 1, func(c config.Config) bool { return c.ReasoningEffort == "max" }},
		{"maxTurns+", fieldMaxTurns, 1, func(c config.Config) bool { return c.MaxTurns == 275 }},
		{"maxTurns-", fieldMaxTurns, -1, func(c config.Config) bool { return c.MaxTurns == 225 }},
		{"fraction+", fieldFraction, 1, func(c config.Config) bool { return nearEq(c.CompactionFraction, 0.85) }},
		{"fraction-", fieldFraction, -1, func(c config.Config) bool { return nearEq(c.CompactionFraction, 0.75) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newSettingsForm(cfgFixture(), []string{})
			f.field = tc.field
			f.adjust(tc.dir)
			if !tc.verify(f.draft()) {
				t.Fatalf("adjust(%d,%+d) draft = %+v, want the expected change", tc.field, tc.dir, f.draft())
			}
		})
	}
}

func TestSettingsForm_EffortCyclesAllTiers(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "high"
	f.field = fieldEffort

	want := []string{"max", "low", "medium", "high"}
	for _, w := range want {
		f.adjust(1)
		if got := f.draft().ReasoningEffort; got != w {
			t.Fatalf("effort after + = %q, want %q", got, w)
		}
	}
}

func TestSettingsForm_EffortCyclesBackwardWraps(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "high"
	f.field = fieldEffort
	f.adjust(-1)
	if got := f.draft().ReasoningEffort; got != "medium" {
		t.Fatalf("effort after - = %q, want medium", got)
	}
}

func TestSettingsForm_ThinkingToggleRetainsEffort(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldThinking

	f.adjust(1)
	if f.draft().ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = true after a down on Thinking, want off")
	}
	if got := f.draft().ReasoningEffort; got != "high" {
		t.Fatalf("ReasoningEffort = %q after turning thinking off, want retained \"high\"", got)
	}

	f.adjust(-1)
	if !f.draft().ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = false after an up on Thinking, want on")
	}
}

func TestSettingsForm_ThinkingToggleDirectionInsensitive(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldThinking
	f.adjust(-1)
	if f.draft().ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = true after up on Thinking, want off")
	}
}

func TestSettingsForm_ModelAdjustSelectsDiscovered(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{"deepseek-v4-flash", "grok-2"})
	f.field = fieldModel
	f.adjust(1)
	if got := f.draft().Model; got != "grok-2" {
		t.Fatalf("Model after adjust = %q, want grok-2", got)
	}
}

func TestSettingsForm_ThemeCyclesAllThemes(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "dark"
	f.field = fieldTheme

	want := []string{"light", "dracula", "tokyo-night", "pink", "nord", "gruvbox", "solarized", "dark-daltonized", "light-daltonized", "notty", "auto", "dark"}
	for _, w := range want {
		f.adjust(1)
		if got := f.draft().Theme; got != w {
			t.Fatalf("theme after + = %q, want %q", got, w)
		}
	}
}

func TestSettingsForm_ThemeCyclesBackwardWraps(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "dark"
	f.field = fieldTheme
	f.adjust(-1)
	if got := f.draft().Theme; got != "auto" {
		t.Fatalf("theme after - = %q, want auto", got)
	}
}

func TestSettingsForm_InvalidThemeFirstAdjustSelectsValid(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.Theme = "rainbow"
	f := newSettingsForm(cfg, []string{})
	f.field = fieldTheme

	if got := f.draft().Theme; got != "rainbow" {
		t.Fatalf("theme before adjust = %q, want raw unknown %q", "rainbow", got)
	}
	f.adjust(1)
	if got := f.draft().Theme; got != "dark" {
		t.Fatalf("theme after first + = %q, want first valid %q", "dark", got)
	}
}

func TestSettingsForm_PathsRoundTrip(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldPaths
	f.SetPathBuf("/a, /b ,/c")
	got := f.draft().ExtraWritablePaths
	if len(got) != 3 || got[0] != "/a" || got[1] != "/b" || got[2] != "/c" {
		t.Fatalf("SetPathBuf draft paths = %v, want [/a /b /c]", got)
	}
}

func TestSettingsForm_SaveIsAFocusableField(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldSave
	if !f.onSave() {
		t.Fatal("expected onSave() true when focused on the Save field")
	}
	f.next() // wraps back to the first field
	if f.field != fieldProvider {
		t.Fatalf("field after wrapping past Save = %d, want %d (fieldProvider)", f.field, fieldProvider)
	}
}

func TestSettingsView_RendersKnobsAndSave(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{"grok-2"})
	view := settingsView(f)
	for _, want := range []string{"Eitri Settings", "opencode-go", "grok-2", "Thinking", "on", "high", "250", "0.80", "Theme", "dark", "[ Save ]", "[ Cancel ]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view %q missing %q", view, want)
		}
	}
}

func TestSettingsForm_CollapseTogglesAdjust(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{}) // both collapsed-by-default on

	f.field = fieldCoTCollapsed
	f.adjust(1)
	if f.draft().CoTCollapsedByDefault {
		t.Fatalf("CoTCollapsedByDefault = true after adjust on CoT collapsed, want off")
	}
	f.adjust(-1)
	if !f.draft().CoTCollapsedByDefault {
		t.Fatalf("CoTCollapsedByDefault = false after adjust back, want on")
	}

	f.field = fieldToolResultsCollapsed
	f.adjust(1)
	if f.draft().ToolResultsCollapsedByDefault {
		t.Fatalf("ToolResultsCollapsedByDefault = true after adjust on tool results collapsed, want off")
	}
}

func TestSettingsView_RendersCollapseRows(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	view := settingsView(f)
	for _, want := range []string{"CoT collapsed", "on", "Tool results collapsed"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view %q missing %q", view, want)
		}
	}
}

func TestSettingsView_ThemeRowSitsBetweenCompactionAndWritable(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	view := settingsView(f)
	compaction := strings.Index(view, "Compaction")
	theme := strings.Index(view, "Theme")
	writable := strings.Index(view, "Writable")
	if compaction < 0 || theme < 0 || writable < 0 {
		t.Fatalf("settings view %q missing Compaction/Theme/Writable rows", view)
	}
	if !(compaction < theme && theme < writable) {
		t.Fatalf("settings view row order wrong: Compaction@%d Theme@%d Writable@%d", compaction, theme, writable)
	}
}

func TestSettingsView_ShowsRawInvalidTheme(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.Theme = "rainbow"
	f := newSettingsForm(cfg, []string{})
	view := settingsView(f)
	if !strings.Contains(view, "rainbow") {
		t.Fatalf("settings view %q missing raw invalid theme \"rainbow\"", view)
	}
}

func TestSettingsView_HighlightsFocusedRow(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldModel
	view := settingsView(f)
	if !strings.Contains(view, "▸ ") {
		t.Fatalf("settings view %q missing focus marker", view)
	}
}

func TestSettingsView_RendersWritableCaretWhenFocused(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldPaths
	f.pathBuf = "/srv"

	view := ansiStrip(settingsView(f))
	if !strings.Contains(view, "/srv"+g("█", "|")) {
		t.Fatalf("settings view %q missing writable field caret", view)
	}
}

func TestSettingsView_RendersLiveCacheReadout(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "high", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	f := newSettingsForm(cfgFixture(), []string{"grok-2"})
	f.telemetry = te
	view := settingsView(f)
	if !strings.Contains(view, "cache:80%") {
		t.Fatalf("settings view %q missing live cache hit-ratio readout", view)
	}
	if strings.Contains(view, "cost") {
		t.Fatalf("settings view %q must not render a cost readout (issue #374)", view)
	}
}

func TestSettingsView_TelemetryReadoutZeroWhenNone(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	view := settingsView(f)
	if strings.Contains(view, "cache:") {
		t.Fatalf("settings view %q renders a readout without telemetry wired", view)
	}
}

func TestSettingsView_PaletteSwatchTracksTheme(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "dark"
	f.field = fieldTheme

	view := settingsView(f)
	if !strings.Contains(view, "palette") || !strings.Contains(view, "\u2588\u2588") {
		t.Fatalf("settings view %q missing the palette swatch row", view)
	}
	if !strings.Contains(view, "\x1b[38;2;122;162;247m") {
		t.Fatalf("dark swatch must carry the default accent chip, got: %q", view)
	}

	f.adjust(1) // dark -> light
	view = settingsView(f)
	if !strings.Contains(view, "\x1b[38;2;0;95;255m") {
		t.Fatalf("light swatch must carry the light accent chip, got: %q", view)
	}
}
