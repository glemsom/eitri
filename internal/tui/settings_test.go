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
		Provider:           "opencode-go",
		Model:              "deepseek-v4-flash",
		ReasoningEffort:    "high",
		ThinkingEnabled:    true,
		MaxTurns:           250,
		CompactionFraction: 0.8,
		ExtraWritablePaths: []string{"/srv"},
		Theme:              config.DefaultTheme,
	}
}

// nearEq reports float equality within 1e-9 (stepping 0.05 from 0.8 drifts by
// float rounding).
func nearEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestSettingsForm_ModelStartsWithConfigured(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{"deepseek-v4-flash", "grok-2", "kimi"})
	if got := f.Model(); got != "deepseek-v4-flash" {
		t.Fatalf("Model() = %q, want configured deepseek-v4-flash", got)
	}
}

func TestSettingsForm_ModelFallsBackToConfiguredWhenNoneDiscovered(t *testing.T) {
	f := newSettingsForm(cfgFixture(), nil)
	if got := f.Model(); got != "deepseek-v4-flash" {
		t.Fatalf("Model() = %q, want configured deepseek-v4-flash (no discovery)", got)
	}
}

// TestSettingsForm_ThemeAdjustReskinsPanel asserts cycling the theme field
// re-skins the Settings chrome live (issue #179 AC5): the form's theme tracks
// the newly selected palette as the arrow cycle moves, so the panel visibly
// follows the selection before Save — the mechanism proven end-to-end, not
// just persisted on Save.
func TestSettingsForm_ThemeAdjustReskinsPanel(t *testing.T) {
	f := newSettingsForm(cfgFixture(), nil)
	f.field = fieldTheme
	// From "dark", one step lands on "light" (default chrome palette).
	f.adjust(1)
	if got := f.theme.accent; got != defaultTheme.accent {
		t.Fatalf("light theme accent = %v, want default accent", got)
	}
	// A second step lands on "dracula": the chrome follows the selection.
	f.adjust(1)
	if f.cfg.Theme != "dracula" {
		t.Fatalf("cfg.Theme = %q, want dracula", f.cfg.Theme)
	}
	if got := f.theme.accent; got != lipgloss.Color("#BD93F9") {
		t.Fatalf("dracula theme accent = %v, want dracula accent", got)
	}
}

func TestSettingsForm_AdjustsKnobs(t *testing.T) {
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

// TestSettingsForm_EffortCyclesAllTiers verifies the reasoning-effort selector
// cycles through every first-class tier low→medium→high→max (issue #74).
func TestSettingsForm_EffortCyclesAllTiers(t *testing.T) {
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

// TestSettingsForm_EffortCyclesBackwardWraps verifies backward stepping wraps
// low→max as well, so no tier is unreachable from the default.
func TestSettingsForm_EffortCyclesBackwardWraps(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "high"
	f.field = fieldEffort
	f.adjust(-1)
	if got := f.draft().ReasoningEffort; got != "medium" {
		t.Fatalf("effort after - = %q, want medium", got)
	}
}

// TestSettingsForm_ThinkingToggleRetainsEffort validates the reasoning mode
// (on/off) toggles ThinkingEnabled while retaining the effort selection, so
// toggling back on restores the original effort tier (issue #56).
func TestSettingsForm_ThinkingToggleRetainsEffort(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldThinking

	// Toggle off.
	f.adjust(1)
	if f.draft().ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = true after a down on Thinking, want off")
	}
	// Effort selection is retained while off, so re-enabling restores it.
	if got := f.draft().ReasoningEffort; got != "high" {
		t.Fatalf("ReasoningEffort = %q after turning thinking off, want retained \"high\"", got)
	}

	// Toggle back on.
	f.adjust(-1)
	if !f.draft().ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = false after an up on Thinking, want on")
	}
}

func TestSettingsForm_ThinkingToggleDirectionInsensitive(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldThinking
	// Both arrows flip the boolean mode; there is no meaningful directional
	// order for an on/off switch.
	f.adjust(-1)
	if f.draft().ThinkingEnabled {
		t.Fatalf("ThinkingEnabled = true after up on Thinking, want off")
	}
}

func TestSettingsForm_ModelAdjustSelectsDiscovered(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{"deepseek-v4-flash", "grok-2"})
	f.field = fieldModel
	f.adjust(1)
	if got := f.draft().Model; got != "grok-2" {
		t.Fatalf("Model after adjust = %q, want grok-2", got)
	}
}

// TestSettingsForm_ThemeCyclesAllThemes verifies the theme selector cycles
// through every supported theme dark→light→dracula→tokyo-night→pink→notty→auto
// and wraps back to dark (issue #130 AC2).
func TestSettingsForm_ThemeCyclesAllThemes(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "dark"
	f.field = fieldTheme

	want := []string{"light", "dracula", "tokyo-night", "pink", "notty", "auto", "dark"}
	for _, w := range want {
		f.adjust(1)
		if got := f.draft().Theme; got != w {
			t.Fatalf("theme after + = %q, want %q", got, w)
		}
	}
}

// TestSettingsForm_ThemeCyclesBackwardWraps verifies backward stepping wraps
// dark→auto, so no theme is unreachable from the default (issue #130 AC2).
func TestSettingsForm_ThemeCyclesBackwardWraps(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{}) // seeded "dark"
	f.field = fieldTheme
	f.adjust(-1)
	if got := f.draft().Theme; got != "auto" {
		t.Fatalf("theme after - = %q, want auto", got)
	}
}

// TestSettingsForm_InvalidThemeFirstAdjustSelectsValid verifies a hand-written
// unknown theme in config shows raw and the first arrow press lands on a valid
// theme, matching the hand-edited bad model value behaviour (issue #130 AC3).
func TestSettingsForm_InvalidThemeFirstAdjustSelectsValid(t *testing.T) {
	cfg := cfgFixture()
	cfg.Theme = "rainbow"
	f := newSettingsForm(cfg, []string{})
	f.field = fieldTheme

	// The raw unknown value is displayed untouched until an arrow is pressed.
	if got := f.draft().Theme; got != "rainbow" {
		t.Fatalf("theme before adjust = %q, want raw unknown %q", "rainbow", got)
	}
	f.adjust(1)
	if got := f.draft().Theme; got != "dark" {
		t.Fatalf("theme after first + = %q, want first valid %q", "dark", got)
	}
}

func TestSettingsForm_PathsRoundTrip(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldPaths
	f.SetPathBuf("/a, /b ,/c")
	got := f.draft().ExtraWritablePaths
	if len(got) != 3 || got[0] != "/a" || got[1] != "/b" || got[2] != "/c" {
		t.Fatalf("SetPathBuf draft paths = %v, want [/a /b /c]", got)
	}
}

func TestSettingsForm_SaveIsAFocusableField(t *testing.T) {
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
	// Discovery surfaces grok-2; the view shows the discovered selection.
	f := newSettingsForm(cfgFixture(), []string{"grok-2"})
	view := settingsView(f)
	for _, want := range []string{"Eitri Settings", "opencode-go", "grok-2", "Thinking", "on", "high", "250", "0.80", "Theme", "dark", "[ Save ]", "[ Cancel ]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view %q missing %q", view, want)
		}
	}
}

// TestSettingsView_ThemeRowSitsBetweenCompactionAndWritable verifies the Theme
// row lands in the agreed position: after Compaction Fraction, before the
// writable-paths field (issue #130 AC1).
func TestSettingsView_ThemeRowSitsBetweenCompactionAndWritable(t *testing.T) {
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

// TestSettingsView_ShowsRawInvalidTheme verifies an unknown theme value from
// config renders raw in the row, surfaced rather than silently rewritten
// (issue #130 AC3).
func TestSettingsView_ShowsRawInvalidTheme(t *testing.T) {
	cfg := cfgFixture()
	cfg.Theme = "rainbow"
	f := newSettingsForm(cfg, []string{})
	view := settingsView(f)
	if !strings.Contains(view, "rainbow") {
		t.Fatalf("settings view %q missing raw invalid theme \"rainbow\"", view)
	}
}

func TestSettingsView_HighlightsFocusedRow(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldModel
	view := settingsView(f)
	if !strings.Contains(view, "▸ ") {
		t.Fatalf("settings view %q missing focus marker", view)
	}
}

// TestSettingsView_RendersLiveTelemetryReadout verifies the settings panel
// surfaces the same live cache hit-ratio + cost readout the run tracks (issue
// #89 AC4), so switching provider/model and watching cost happen in one pane.
// It reflects the live Telemetry borrowed from the status strip, never the
// agent loop itself (read-only).
func TestSettingsView_RendersLiveTelemetryReadout(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "high", true, 250)
	// 100k hit @0.0028/1M + 25k miss @0.14/1M + 10k output @0.28/1M.
	// = 0.00028 + 0.0035 + 0.0028 = $0.00658; hit ratio 80%.
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	f := newSettingsForm(cfgFixture(), []string{"grok-2"})
	f.telemetry = te
	view := settingsView(f)
	if !strings.Contains(view, "cache:80%") {
		t.Fatalf("settings view %q missing live cache hit-ratio readout", view)
	}
	if !strings.Contains(view, "cost:$0.00658") {
		t.Fatalf("settings view %q missing live cost readout", view)
	}
}

// TestSettingsView_TelemetryReadoutZeroWhenNone verifies a settings panel with
// no wired telemetry renders no readout line (the pre-telemetry default).
func TestSettingsView_TelemetryReadoutZeroWhenNone(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{})
	view := settingsView(f)
	if strings.Contains(view, "cache:") {
		t.Fatalf("settings view %q renders a readout without telemetry wired", view)
	}
}
