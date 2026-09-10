package tui

import (
	"github.com/glemsom/eitri/internal/tui/telemetry"
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

func TestSettingsForm_FieldVisibility(t *testing.T) {
	t.Parallel()
	cases := []struct {
		provider string
		visible  []int
		hidden   []int
	}{
		{"opencode-go", []int{fieldProvider, fieldModel, fieldOpenCodeKey}, []int{fieldCustomOpenAIBaseURL, fieldCustomOpenAIKey}},
		{"custom-openai", []int{fieldProvider, fieldModel, fieldCustomOpenAIBaseURL, fieldCustomOpenAIKey}, []int{fieldOpenCodeKey}},
		{"github-copilot", []int{fieldProvider, fieldModel}, []int{fieldOpenCodeKey, fieldCustomOpenAIBaseURL, fieldCustomOpenAIKey}},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			f := newSettingsForm(cfgFixture(), []string{})
			f.cfg.Provider = tc.provider
			for _, v := range tc.visible {
				if !f.fieldVisible(v) {
					t.Fatalf("field %d should be visible for %s", v, tc.provider)
				}
			}
			for _, h := range tc.hidden {
				if f.fieldVisible(h) {
					t.Fatalf("field %d should be hidden for %s", h, tc.provider)
				}
			}
		})
	}
}

func TestSettingsForm_StepSkipsInvisibleFields(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.cfg.Provider = "github-copilot"
	f.field = fieldPaths
	f.step(1)
	if f.field != fieldSave {
		t.Fatalf("field after step from paths = %d, want fieldSave (skipped credential fields)", f.field)
	}
}

func TestSettingsForm_TextInputSetsValue(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.cfg.Provider = "custom-openai"
	f.field = fieldCustomOpenAIBaseURL
	f.beginTextInput()
	if !f.textInputActive {
		t.Fatal("textInputActive = false after beginTextInput")
	}
	f.textInput.SetValue("https://example.com/v1")
	f.confirmTextInput()
	if f.textInputActive {
		t.Fatal("textInputActive = true after confirmTextInput")
	}
	if f.cfg.CustomOpenAI.BaseURL != "https://example.com/v1" {
		t.Fatalf("BaseURL = %q, want https://example.com/v1", f.cfg.CustomOpenAI.BaseURL)
	}
}

func TestSettingsForm_TextInputCancelRestores(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.cfg.Provider = "opencode-go"
	f.cfg.OpenCodeGo.Key = "original"
	f.field = fieldOpenCodeKey
	f.beginTextInput()
	f.textInput.SetValue("changed")
	f.cancelTextInput()
	if f.textInputActive {
		t.Fatal("textInputActive = true after cancelTextInput")
	}
	if f.cfg.OpenCodeGo.Key != "original" {
		t.Fatalf("Key = %q, want original", f.cfg.OpenCodeGo.Key)
	}
}

func TestSettingsForm_ConfigsEqualIncludesCredentials(t *testing.T) {
	t.Parallel()
	a := cfgFixture()
	b := cfgFixture()
	if !configsEqual(a, b) {
		t.Fatal("configsEqual = false for identical configs")
	}
	a.OpenCodeGo.Key = "different"
	if configsEqual(a, b) {
		t.Fatal("configsEqual = true after changing OpenCodeGo.Key")
	}
}

func TestSettingsView_RendersProviderCredentials(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.cfg.Provider = "custom-openai"
	f.cfg.CustomOpenAI = config.OpenAIConfig{BaseURL: "https://example.com/v1", Key: "secret"}
	view := settingsView(f)
	if !strings.Contains(view, "Base URL") {
		t.Fatalf("settings view %q missing Base URL row", view)
	}
	if !strings.Contains(view, "API key") {
		t.Fatalf("settings view %q missing API key row", view)
	}
}

func TestSettingsView_RendersOpenCodeKey(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.cfg.Provider = "opencode-go"
	f.cfg.OpenCodeGo = config.OpenCodeGoConfig{Key: "my-key"}
	view := settingsView(f)
	if !strings.Contains(view, "OpenCode API key") {
		t.Fatalf("settings view %q missing OpenCode API key row", view)
	}
}

func TestMaskKey(t *testing.T) {
	t.Parallel()
	if got := maskKey(""); got != "(not set)" {
		t.Fatalf("maskKey(\"\") = %q, want (not set)", got)
	}
	if got := maskKey("short"); got != "••••••••" {
		t.Fatalf("maskKey(\"short\") = %q, want ••••••••", got)
	}
	if got := maskKey("very-long-secret-key"); got != "very••••-key" {
		t.Fatalf("maskKey(\"very-long-secret-key\") = %q, want very••••-key", got)
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
	for _, want := range []string{"Eitri Settings", "opencode-go", "grok-2", "Deep thinking", "✓ on", "high", "250", "Context overflow recovery", "Theme", "dark", "[ Save changes ]", "[ Cancel ]"} {
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
	te := telemetry.NewTelemetry("deepseek-v4-flash", "high", true, 250)
	te.Apply(telemetry.TelemetryUpdate{Kind: telemetry.TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	f := newSettingsForm(cfgFixture(), []string{"grok-2"})
	f.telemetry = te
	view := settingsView(f)
	if !strings.Contains(view, "cache:80%") {
		t.Fatalf("settings view %q missing live cache hit-ratio readout", view)
	}
	if strings.Contains(view, "cost") {
		t.Fatalf("settings view %q must not render a cost readout", view)
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

func TestSettingsOverlay_WritablePathsCannotBeManuallyEdited(t *testing.T) {
	t.Parallel()
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	o.field = fieldPaths

	o.Key(tea.KeyPressMsg{Text: "x", Code: 'x'})

	got := o.draft().ExtraWritablePaths
	if len(got) != 1 || got[0] != "/srv" {
		t.Fatalf("paths after typing = %v, want unchanged [/srv]", got)
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

// startAddPathPicker opens the add-folder picker over dir and loads dir's
// listing so the picker has a deterministic highlight (its first entry).
func startAddPathPicker(t *testing.T, o *SettingsOverlay, dir string) {
	t.Helper()
	o.field = fieldPaths
	if _, cmd := o.Key(tea.KeyPressMsg{Text: "+", Code: '+'}); cmd == nil {
		t.Fatalf("add-folder key returned no picker init cmd")
	}
	o.picker.CurrentDirectory = dir
	o.Handle(o.picker.Init()())
	if !o.pickerActive {
		t.Fatalf("picker inactive after start")
	}
}

// makeChildDir returns a fresh temp dir containing a single child dir.
func makeChildDir(t *testing.T) (dir, child string) {
	t.Helper()
	dir = t.TempDir()
	child = filepath.Join(dir, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}
	return dir, child
}

func TestSettingsOverlay_AddFolderPickerOpenAndSelectKeysDisjoint(t *testing.T) {
	t.Parallel()
	f := newSettingsForm(cfgFixture(), []string{})
	f.beginAddPath()

	open := map[string]bool{}
	for _, k := range f.picker.KeyMap.Open.Keys() {
		open[k] = true
	}
	var shared []string
	hasCtrlO := false
	for _, k := range f.picker.KeyMap.Select.Keys() {
		if open[k] {
			shared = append(shared, k)
		}
		if k == "ctrl+o" {
			hasCtrlO = true
		}
	}
	if len(shared) != 0 {
		t.Fatalf("Open and Select share keys %v in the add-folder picker, want disjoint", shared)
	}
	if !hasCtrlO {
		t.Fatalf("Select keys = %v, want ctrl+o bound to selection", f.picker.KeyMap.Select.Keys())
	}
}

func TestSettingsOverlay_FilePickerSelectAddsHighlightedFolderWithoutDescending(t *testing.T) {
	dir, child := makeChildDir(t)
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	startAddPathPicker(t, o, dir)

	o.Handle(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})

	got := o.draft().ExtraWritablePaths
	if len(got) != 2 || got[1] != child {
		t.Fatalf("paths after select = %v, want [/srv %q]", got, child)
	}
	if o.pickerActive {
		t.Fatalf("picker active after select, want closed")
	}
	if cd := o.picker.CurrentDirectory; cd != dir {
		t.Fatalf("picker current dir after select = %q, want unchanged %q (select must not descend)", cd, dir)
	}
}

func TestSettingsOverlay_FilePickerOpenKeyDescendsNotSelects(t *testing.T) {
	dir, child := makeChildDir(t)
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	startAddPathPicker(t, o, dir)

	o.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})

	if cd := o.picker.CurrentDirectory; cd != child {
		t.Fatalf("picker current dir after enter = %q, want %q", cd, child)
	}
	if !o.pickerActive {
		t.Fatalf("picker closed after enter, want open")
	}
	if got := o.draft().ExtraWritablePaths; len(got) != 1 {
		t.Fatalf("enter added paths %v, want none added", got)
	}
}

func TestSettingsOverlay_FilePickerSelectOnFileAddsNothing(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	startAddPathPicker(t, o, dir) // listing: [sub, notes.txt]
	o.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	o.Handle(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

	if !o.pickerActive {
		t.Fatalf("picker closed after selecting a file, want open")
	}
	if got := o.draft().ExtraWritablePaths; len(got) != 1 {
		t.Fatalf("select on a file added %v, want none", got)
	}
}

func TestSettingsOverlay_FilePickerSelectClosesPickerBeforeTab(t *testing.T) {
	dir, child := makeChildDir(t)
	o, _ := openSettingsOverlay(cfgFixture(), []string{"m"}, defaultTheme, nil, nil, Dependencies{})
	startAddPathPicker(t, o, dir)
	o.Handle(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	o.Handle(tea.KeyPressMsg{Code: tea.KeyTab})

	got := o.draft().ExtraWritablePaths
	count := 0
	for _, p := range got {
		if p == child {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("selected path count after tab = %d in %v, want 1", count, got)
	}
	if o.pickerActive {
		t.Fatalf("picker active after selection, want closed")
	}
}
