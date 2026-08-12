package tui

import (
	"math"
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

func cfgFixture() config.Config {
	return config.Config{
		Provider:           "opencode-go",
		Model:              "deepseek-v4-flash",
		ReasoningEffort:    "high",
		MaxTurns:           250,
		CompactionFraction: 0.8,
		ExtraWritablePaths: []string{"/srv"},
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

func TestSettingsForm_ModelAdjustSelectsDiscovered(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{"deepseek-v4-flash", "grok-2"})
	f.field = fieldModel
	f.adjust(1)
	if got := f.draft().Model; got != "grok-2" {
		t.Fatalf("Model after adjust = %q, want grok-2", got)
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
	view := settingsView(f, 80)
	for _, want := range []string{"Eitri Settings", "opencode-go", "grok-2", "high", "250", "0.80", "[ Save ]", "[ Cancel ]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view %q missing %q", view, want)
		}
	}
}

func TestSettingsView_HighlightsFocusedRow(t *testing.T) {
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldModel
	view := settingsView(f, 80)
	if !strings.Contains(view, "▸ ") {
		t.Fatalf("settings view %q missing focus marker", view)
	}
}
