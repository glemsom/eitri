package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
		ContextOverflowRecovery:       true,
		ExtraWritablePaths:            []string{"/srv"},
		Theme:                         config.DefaultTheme,
		CoTCollapsedByDefault:         true,
		ToolResultsCollapsedByDefault: true,
	}
}

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
		{"contextOverflowRecovery", fieldContextOverflowRecovery, 1, func(c config.Config) bool { return !c.ContextOverflowRecovery }},
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

func TestSettingsForm_SaveAndCancelAreFocusableFields(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldSave
	if !f.onSave() {
		t.Fatal("expected onSave() true when focused on the Save field")
	}
	f.next()
	if !f.onCancel() {
		t.Fatal("expected onCancel() true after Save field")
	}
	f.next() // wraps back to the first field
	if f.field != fieldProvider {
		t.Fatalf("field after wrapping past Cancel = %d, want %d (fieldProvider)", f.field, fieldProvider)
	}
}

func TestSettingsView_RendersKnobsAndSave(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{"grok-2"})
	view := settingsView(f)
	for _, want := range []string{"Eitri Settings", "opencode-go", "grok-2", "Deep thinking", "✓ on", "high", "250", "Context overflow recovery", "Theme", "dark", "[ Save ]", "[ Cancel ]"} {
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
	for _, want := range []string{"Collapse thinking", "✓ on", "Collapse tool output"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view %q missing %q", view, want)
		}
	}
}

func TestSettingsView_GroupsDisplayBeforeWorkspaceAccess(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	view := settingsView(f)
	recovery := strings.Index(view, "Context overflow recovery")
	theme := strings.Index(view, "Theme")
	writable := strings.Index(view, "Writable paths")
	if recovery < 0 || theme < 0 || writable < 0 {
		t.Fatalf("settings view %q missing recovery/theme/writable rows", view)
	}
	if strings.Contains(view, "Summarize history at") {
		t.Fatalf("settings view %q still renders proactive compaction row", view)
	}
	if !(recovery < theme && theme < writable) {
		t.Fatalf("settings view row order wrong: recovery@%d Theme@%d writable@%d", recovery, theme, writable)
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

func TestSettingsView_RendersWritableListWhenFocused(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.field = fieldPaths

	view := ansiStrip(settingsView(f))
	for _, want := range []string{"1 folder(s)", "› /srv", "+ Add folder", "Delete: remove selected"} {
		if !strings.Contains(view, want) {
			t.Fatalf("settings view %q missing %q", view, want)
		}
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
	if !strings.Contains(view, "Palette") || !strings.Contains(view, "\u2588\u2588") {
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

func TestSettingsForm_RemoveSelectedPath(t *testing.T) {
	t.Parallel()
	cfg := cfgFixture()
	cfg.ExtraWritablePaths = []string{"/srv", "/opt"}
	f := newSettingsForm(cfg, []string{})
	f.selectedPath = 1

	f.removeSelectedPath()

	got := f.draft().ExtraWritablePaths
	if len(got) != 1 || got[0] != "/srv" {
		t.Fatalf("paths after remove = %v, want [/srv]", got)
	}
}

func TestSettingsOverlay_FilePickerCanNavigateBackToParent(t *testing.T) {
	dir := t.TempDir()
	child := dir + "/child"
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	o.field = fieldPaths
	outcome, cmd := o.Key(tea.KeyPressMsg{Text: "+", Code: '+'})
	if outcome != outcomeContinue || cmd == nil {
		t.Fatalf("start picker outcome/cmd = %v/%v, want continue/init cmd", outcome, cmd)
	}
	o.picker.CurrentDirectory = child
	o.Handle(tea.KeyPressMsg{Text: "u", Code: 'u'})
	if got := filepath.Clean(o.picker.CurrentDirectory); got != filepath.Clean(dir) {
		t.Fatalf("picker current dir after u = %q, want parent %q", got, dir)
	}
}

func TestSettingsOverlay_FilePickerSelectClosesPickerBeforeTab(t *testing.T) {
	dir := t.TempDir()
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	o.field = fieldPaths
	o.addingPath = true
	o.pickerActive = true
	o.picker.Path = dir
	o.Handle(tea.KeyPressMsg{Text: "s", Code: 's', Mod: tea.ModCtrl})
	o.Handle(tea.KeyPressMsg{Code: tea.KeyTab})

	got := o.draft().ExtraWritablePaths
	count := 0
	for _, p := range got {
		if p == dir {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("selected path count after tab = %d in %v, want 1", count, got)
	}
	if o.addingPath || o.pickerActive {
		t.Fatalf("picker active after selection: adding=%v active=%v, want closed", o.addingPath, o.pickerActive)
	}
}
