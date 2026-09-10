package tui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"strings"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/tui/telemetry"
)

// Field indexes for the settings form, in display/cycle order.
const (
	fieldProvider = iota
	fieldModel
	fieldThinking
	fieldEffort
	fieldMaxTurns
	fieldContextOverflowRecovery
	fieldTheme
	fieldCoTCollapsed
	fieldToolResultsCollapsed
	fieldPaths
	fieldOpenCodeKey
	fieldCustomOpenAIBaseURL
	fieldCustomOpenAIKey
	fieldSave
	fieldCancel
	fieldCount
)

// settingVals are the option lists for cycle-style setters.
var (
	providerFams = []string{"opencode-go", "github-copilot", "custom-openai"}
	effortTiers  = []string{"low", "medium", "high", "max"}
)

// discoverState is the on-demand model-discovery lifecycle of the Settings surface.
type discoverState int

const (
	discoverIdle discoverState = iota
	discoverLoading
	discoverError
)

// settingsForm is the pure state behind the TUI Settings surface.
type settingsForm struct {
	theme               Theme
	cfg                 config.Config
	original            config.Config
	models              []string
	field               int
	selectedPath        int
	pickerActive        bool
	picker              filepicker.Model
	textInputActive     bool
	textInput           textinput.Model
	textInputField      int
	telemetry           *telemetry.Telemetry
	discoverState       discoverState
	discoverErr         string
	thinkingSuppression func() bool
	confirmDiscard      bool
	discardSelected     bool
}

// newSettingsForm seeds the form with the loaded config and the discovered model list.
func newSettingsForm(cfg config.Config, models []string) settingsForm {
	f := settingsForm{cfg: cfg, original: cfg, models: models, field: 0, theme: defaultTheme}
	if len(models) == 0 {
		f.models = []string{cfg.Model}
	}
	f.normalizeSelectedPath()
	return f
}

// Model returns the currently selected model (a discovered id, or the configured model when none is discovered).
func (f settingsForm) Model() string {
	if len(f.models) == 0 {
		return f.cfg.Model
	}
	i := indexOf(f.models, f.cfg.Model)
	if i < 0 {
		return f.models[0]
	}
	return f.models[i]
}

func (f *settingsForm) fieldVisible(field int) bool {
	switch field {
	case fieldOpenCodeKey:
		return f.cfg.Provider == string(provider.ProviderOpenCodeGo)
	case fieldCustomOpenAIBaseURL, fieldCustomOpenAIKey:
		return f.cfg.Provider == string(provider.ProviderCustomOpenAI)
	default:
		return true
	}
}

func (f *settingsForm) step(d int) {
	for {
		f.field = (f.field + d + fieldCount) % fieldCount
		if f.fieldVisible(f.field) {
			break
		}
	}
}

func (f *settingsForm) next() { f.step(1) }

// adjust steps the focused value. d is ±1 for cycle setters, ±step for numeric steppers, or a direct edit for the free-form path field.
func (f *settingsForm) adjust(d int) {
	switch f.field {
	case fieldProvider:
		f.cfg.Provider = cycle(f.cfg.Provider, providerFams, d)
	case fieldModel:
		if len(f.models) > 0 {
			f.cfg.Model = cycle(f.cfg.Model, f.models, d)
		}
	case fieldThinking:
		if d != 0 {
			f.cfg.ThinkingEnabled = !f.cfg.ThinkingEnabled
		}
	case fieldCoTCollapsed:
		if d != 0 {
			f.cfg.CoTCollapsedByDefault = !f.cfg.CoTCollapsedByDefault
		}
	case fieldToolResultsCollapsed:
		if d != 0 {
			f.cfg.ToolResultsCollapsedByDefault = !f.cfg.ToolResultsCollapsedByDefault
		}
	case fieldEffort:
		f.cfg.ReasoningEffort = cycle(f.cfg.ReasoningEffort, effortTiers, d)
	case fieldMaxTurns:
		f.cfg.MaxTurns = stepInt(f.cfg.MaxTurns, d, 25, 0, 10000)
	case fieldContextOverflowRecovery:
		if d != 0 {
			f.cfg.ContextOverflowRecovery = !f.cfg.ContextOverflowRecovery
		}
	case fieldTheme:
		f.cfg.Theme = cycle(f.cfg.Theme, supportedThemes, d)
		f.theme = themeFor(f.cfg.Theme)
	case fieldPaths:
		f.stepPathSelection(d)
	case fieldOpenCodeKey, fieldCustomOpenAIBaseURL, fieldCustomOpenAIKey, fieldSave, fieldCancel:
		// no-op: text fields are edited via text input, save/cancel are buttons
	}
}

func (f *settingsForm) beginAddPath() tea.Cmd {
	f.pickerActive = true
	f.picker = filepicker.New()
	if wd, err := os.Getwd(); err == nil {
		f.picker.CurrentDirectory = wd
	}
	f.picker.DirAllowed = true
	f.picker.FileAllowed = false
	f.picker.ShowSize = false
	f.picker.ShowPermissions = false
	f.picker.SetHeight(10)
	f.picker.KeyMap.Back = key.NewBinding(key.WithKeys("h", "u", "backspace", "left"), key.WithHelp("u/left", "parent"))
	f.picker.KeyMap.Open = key.NewBinding(key.WithKeys("l", "right", "enter"), key.WithHelp("enter", "open"))
	f.picker.KeyMap.Select = key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "select folder"))
	return f.picker.Init()
}

func (f *settingsForm) cancelAddPath() {
	f.pickerActive = false
}

func (f *settingsForm) addPath(p string) {
	p = strings.TrimSpace(p)
	if p == "" {
		return
	}
	for i, existing := range f.cfg.ExtraWritablePaths {
		if existing == p {
			f.selectedPath = i
			f.pickerActive = false
			return
		}
	}
	f.cfg.ExtraWritablePaths = append(f.cfg.ExtraWritablePaths, p)
	f.selectedPath = len(f.cfg.ExtraWritablePaths) - 1
	f.pickerActive = false
}

func (f *settingsForm) removeSelectedPath() {
	if len(f.cfg.ExtraWritablePaths) == 0 {
		return
	}
	f.normalizeSelectedPath()
	f.cfg.ExtraWritablePaths = append(f.cfg.ExtraWritablePaths[:f.selectedPath], f.cfg.ExtraWritablePaths[f.selectedPath+1:]...)
	f.normalizeSelectedPath()
}

func (f *settingsForm) normalizeSelectedPath() {
	if len(f.cfg.ExtraWritablePaths) == 0 {
		f.selectedPath = 0
		return
	}
	if f.selectedPath < 0 {
		f.selectedPath = 0
	}
	if f.selectedPath >= len(f.cfg.ExtraWritablePaths) {
		f.selectedPath = len(f.cfg.ExtraWritablePaths) - 1
	}
}

func (f *settingsForm) stepPathSelection(d int) {
	if len(f.cfg.ExtraWritablePaths) == 0 {
		return
	}
	f.selectedPath = (f.selectedPath + d + len(f.cfg.ExtraWritablePaths)) % len(f.cfg.ExtraWritablePaths)
}

func (f *settingsForm) isTextField(field int) bool {
	switch field {
	case fieldOpenCodeKey, fieldCustomOpenAIBaseURL, fieldCustomOpenAIKey:
		return true
	default:
		return false
	}
}

func (f *settingsForm) currentTextValue() string {
	switch f.textInputField {
	case fieldOpenCodeKey:
		return f.cfg.OpenCodeGo.Key
	case fieldCustomOpenAIBaseURL:
		return f.cfg.CustomOpenAI.BaseURL
	case fieldCustomOpenAIKey:
		return f.cfg.CustomOpenAI.Key
	default:
		return ""
	}
}

func (f *settingsForm) setTextValue(v string) {
	switch f.textInputField {
	case fieldOpenCodeKey:
		f.cfg.OpenCodeGo.Key = v
	case fieldCustomOpenAIBaseURL:
		f.cfg.CustomOpenAI.BaseURL = v
	case fieldCustomOpenAIKey:
		f.cfg.CustomOpenAI.Key = v
	}
}

func (f *settingsForm) beginTextInput() tea.Cmd {
	f.textInputActive = true
	f.textInputField = f.field
	f.textInput = textinput.New()
	f.textInput.Focus()
	f.textInput.SetValue(f.currentTextValue())
	if f.field == fieldOpenCodeKey || f.field == fieldCustomOpenAIKey {
		f.textInput.EchoMode = textinput.EchoPassword
		f.textInput.EchoCharacter = '•'
	}
	return nil
}

func (f *settingsForm) cancelTextInput() {
	f.textInputActive = false
	f.textInputField = 0
}

func (f *settingsForm) confirmTextInput() {
	f.setTextValue(f.textInput.Value())
	f.textInputActive = false
	f.textInputField = 0
	f.next()
}

func (f *settingsForm) draft() config.Config { return f.cfg }

// onSave reports whether the focused field is the Save button.
func (f settingsForm) onSave() bool { return f.field == fieldSave }

// onCancel reports whether the focused field is the Cancel button.
func (f settingsForm) onCancel() bool { return f.field == fieldCancel }

func (f settingsForm) dirty() bool {
	return !configsEqual(f.draft(), f.original)
}

func configsEqual(a, b config.Config) bool {
	if a.Provider != b.Provider || a.Model != b.Model || a.ReasoningEffort != b.ReasoningEffort || a.ThinkingEnabled != b.ThinkingEnabled || a.CoTCollapsedByDefault != b.CoTCollapsedByDefault || a.ToolResultsCollapsedByDefault != b.ToolResultsCollapsedByDefault || a.MaxTurns != b.MaxTurns || a.ContextOverflowRecovery != b.ContextOverflowRecovery || a.Theme != b.Theme || a.RailWidth != b.RailWidth {
		return false
	}
	if a.OpenCodeGo.Key != b.OpenCodeGo.Key || a.CustomOpenAI.BaseURL != b.CustomOpenAI.BaseURL || a.CustomOpenAI.Key != b.CustomOpenAI.Key {
		return false
	}
	if len(a.ExtraWritablePaths) != len(b.ExtraWritablePaths) {
		return false
	}
	for i := range a.ExtraWritablePaths {
		if a.ExtraWritablePaths[i] != b.ExtraWritablePaths[i] {
			return false
		}
	}
	return true
}

// thinkingModeLabel renders the reasoning mode value (on/off) for the Settings panel, reflecting the thinking_enabled config.
func thinkingModeLabel(on bool) string {
	if on {
		return g("✓ on", "on")
	}
	return g("○ off", "off")
}

func settingsHelp(f settingsForm) string {
	switch f.field {
	case fieldProvider:
		return "Backend used for future turns. Changing this refreshes available models."
	case fieldModel:
		return "Model used for the next assistant turn."
	case fieldThinking:
		return "Allow deeper model reasoning when the provider supports it."
	case fieldEffort:
		return "Reasoning depth: higher can improve hard answers and increase cost/latency."
	case fieldMaxTurns:
		return "Safety limit for assistant/tool loop iterations."
	case fieldContextOverflowRecovery:
		return "If the provider rejects an oversized request, summarize older history and retry once."
	case fieldTheme:
		return "Color palette for Eitri’s interface."
	case fieldCoTCollapsed:
		return "Show thinking blocks as compact one-liners until expanded."
	case fieldToolResultsCollapsed:
		return "Show tool output as compact one-liners until expanded."
	case fieldPaths:
		if f.pickerActive {
			return "Choose a folder with ↑/↓, Enter opens, Left/Backspace/u goes to parent, Ctrl+O selects, Esc cancels."
		}
		return "Press + or a to add a folder. ←/→ selects an existing path. Delete removes the selected path."
	case fieldOpenCodeKey:
		return "API key for the OpenCode Go provider. Press Enter to edit."
	case fieldCustomOpenAIBaseURL:
		return "Base URL for the custom OpenAI-compatible endpoint. Press Enter to edit."
	case fieldCustomOpenAIKey:
		return "API key for the custom OpenAI-compatible endpoint. Press Enter to edit."
	case fieldSave:
		return "Save changes to config.json and apply them now."
	case fieldCancel:
		return "Discard draft changes and return to chat."
	default:
		return ""
	}
}

// cycle moves v through vals by d (wrap); unknown v starts at the first entry.
func cycle(v string, vals []string, d int) string {
	if len(vals) == 0 {
		return v
	}
	i := indexOf(vals, v)
	if i < 0 {
		return vals[0]
	}
	return vals[(i+d+len(vals))%len(vals)]
}

func indexOf(vals []string, v string) int {
	for i, x := range vals {
		if x == v {
			return i
		}
	}
	return -1
}

func stepInt(v, d, step, min, max int) int {
	v += d * step
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return v
}

func maskKey(k string) string {
	if k == "" {
		return "(not set)"
	}
	if len(k) <= 8 {
		return "••••••••"
	}
	return k[:4] + "••••" + k[len(k)-4:]
}

func pathSummary(f settingsForm) string {
	if f.pickerActive {
		return "adding folder…"
	}
	if len(f.cfg.ExtraWritablePaths) == 0 {
		return "none"
	}
	return fmt.Sprintf("%d folder(s)", len(f.cfg.ExtraWritablePaths))
}

func writePathList(b *strings.Builder, f settingsForm, th Theme) {
	if len(f.cfg.ExtraWritablePaths) == 0 && !f.pickerActive {
		b.WriteString(th.statusStyle.Render("     no extra writable folders configured"))
		b.WriteString("\n")
	}
	for i, p := range f.cfg.ExtraWritablePaths {
		marker := "  "
		if f.field == fieldPaths && i == f.selectedPath && !f.pickerActive {
			marker = "› "
		}
		fmt.Fprintf(b, "     %s%s\n", marker, p)
	}
	if f.pickerActive {
		b.WriteString(th.statusStyle.Render("     Folder picker — Enter opens, Left/Backspace/u parent, Ctrl+O selects, Esc cancels"))
		b.WriteString("\n")
		b.WriteString(f.picker.View())
	}
	b.WriteString(th.statusStyle.Render("     [ + Add folder ] [ Delete: remove selected ]"))
	b.WriteString("\n")
}

// SettingsOverlay owns the open Settings surface: the draft form, its
// on-demand model-discovery lifecycle, and persistence of the draft through
// the save seams.
type SettingsOverlay struct {
	// Embedded form keeps the pure state API (fields, Model(), draft) intact.
	settingsForm

	// discover issues one on-demand model discovery for the draft config;
	// nil disables discovery entirely.
	discover func(ctx context.Context, cfg config.Config) ([]string, error)
	// save persists the accepted draft; nil makes the surface view-only.
	save func(config.Config) error
	// saveBack mirrors an accepted draft back to the caller (engine-side apply).
	saveBack func(config.Config)
	status   string
	height   int
}

// settingsKeyOutcome reports what the Model must do after one key press
// lands on the open overlay.
type settingsKeyOutcome int

const (
	outcomeContinue settingsKeyOutcome = iota
	outcomeClosed
	outcomeSaved
)

// openSettingsOverlay seeds the overlay from the loaded config + discovery,
// borrowing the live theme and telemetry for rendering (the cost readout was
// available it arms the loading state and returns the discovery command.
func openSettingsOverlay(cfg config.Config, models []string, theme Theme, telemetry *telemetry.Telemetry, thinkingSuppressed func() bool, deps Dependencies) (*SettingsOverlay, tea.Cmd) {
	if cfg.Provider == "" {
		cfg = config.Default()
	}
	sf := newSettingsForm(cfg, models)
	sf.theme = theme
	sf.telemetry = telemetry
	sf.thinkingSuppression = thinkingSuppressed
	o := &SettingsOverlay{settingsForm: sf, discover: deps.DiscoverModels, save: deps.Save, saveBack: deps.SaveBack}
	if len(models) != 0 || o.discover == nil {
		return o, nil
	}
	o.discoverState = discoverLoading
	return o, discoverCmd(o.discover, o.cfg)
}

// Key routes one key press into the overlay. It returns what the caller
// should do next plus any follow-up command (model discovery after a
// provider change, or folder picker after Add folder).
func (o *SettingsOverlay) Key(k tea.KeyPressMsg) (settingsKeyOutcome, tea.Cmd) {
	s := &o.settingsForm
	if s.confirmDiscard {
		switch k.String() {
		case "left", "right", "tab":
			s.discardSelected = !s.discardSelected
		case "esc":
			s.confirmDiscard = false
			s.discardSelected = false
		case "enter":
			if s.discardSelected {
				return outcomeClosed, nil
			}
			s.confirmDiscard = false
		}
		return outcomeContinue, nil
	}
	if s.textInputActive {
		return o.keyTextInput(k)
	}
	if s.pickerActive {
		return o.keyAddPath(k)
	}
	switch k.String() {
	case "ctrl+,":
		if s.dirty() {
			return outcomeSaved, nil
		}
	case "esc", "ctrl+c":
		if s.dirty() {
			s.confirmDiscard = true
			s.discardSelected = false
			return outcomeContinue, nil
		}
		return outcomeClosed, nil
	case "enter":
		if s.onSave() {
			return outcomeSaved, nil
		}
		if s.onCancel() {
			return outcomeClosed, nil
		}
		if s.isTextField(s.field) {
			return outcomeContinue, s.beginTextInput()
		}
		s.next()
	case "up", "shift+up":
		s.step(-1)
	case "down", "shift+down":
		s.step(1)
	case "a", "+":
		if s.field == fieldPaths {
			return outcomeContinue, s.beginAddPath()
		}
	case "delete":
		if s.field == fieldPaths {
			s.removeSelectedPath()
		}
	case "left":
		before := s.cfg.Provider
		s.adjust(-1)
		if s.cfg.Provider != before {
			return outcomeContinue, o.beginDiscovery()
		}
	case "right", "tab":
		before := s.cfg.Provider
		s.adjust(1)
		if s.cfg.Provider != before {
			return outcomeContinue, o.beginDiscovery()
		}
	}
	return outcomeContinue, nil
}

func (o *SettingsOverlay) keyTextInput(k tea.KeyPressMsg) (settingsKeyOutcome, tea.Cmd) {
	s := &o.settingsForm
	if k.String() == "esc" || k.String() == "ctrl+c" {
		s.cancelTextInput()
		return outcomeContinue, nil
	}
	if k.String() == "enter" {
		s.confirmTextInput()
		return outcomeContinue, nil
	}
	var cmd tea.Cmd
	s.textInput, cmd = s.textInput.Update(k)
	return outcomeContinue, cmd
}

func (o *SettingsOverlay) keyAddPath(k tea.KeyPressMsg) (settingsKeyOutcome, tea.Cmd) {
	s := &o.settingsForm
	if k.String() == "esc" || k.String() == "ctrl+c" {
		s.cancelAddPath()
		return outcomeContinue, nil
	}
	if key.Matches(k, s.picker.KeyMap.Select) {
		// bubbles dispatches Open before Select, so a key in both maps would
		// also descend into the highlighted folder; handle selection here so
		// it never reaches the picker's Open handler.
		s.selectHighlightedFolder()
		return outcomeContinue, nil
	}
	var cmd tea.Cmd
	s.picker, cmd = s.picker.Update(k)
	return outcomeContinue, cmd
}

// selectHighlightedFolder adds the folder under the picker cursor as a
// writable path and closes the picker without opening it.
func (s *settingsForm) selectHighlightedFolder() {
	p := s.picker.HighlightedPath()
	if p == "" {
		return
	}
	info, err := os.Stat(p)
	if err != nil || !info.IsDir() {
		return
	}
	s.addPath(p)
}

// settingsResult reports the outcome of one message routed into the open
// overlay: what the caller should do next, any follow-up command, and — when
// the outcome is a save — the accepted config plus its status line, and for
// discovery results the refreshed model list.
type settingsResult struct {
	outcome settingsKeyOutcome
	handled bool
	cmd     tea.Cmd
	saved   *config.Config
	applied bool
	status  string
	models  []string
}

// Handle routes one message into the overlay: key presses navigate/save,
// discovery results fold in unless stale from an earlier draft provider.
// Unrelated messages are ignored.
func (o *SettingsOverlay) Handle(msg tea.Msg) settingsResult {
	switch msgi := msg.(type) {
	case tea.WindowSizeMsg:
		o.height = msgi.Height
		return settingsResult{outcome: outcomeContinue, handled: true}
	case tea.KeyPressMsg:
		outcome, cmd := o.Key(msgi)
		res := settingsResult{outcome: outcome, handled: true, cmd: cmd}
		if outcome == outcomeSaved {
			cfg, status, applied := o.Save()
			res.saved, res.status, res.applied = &cfg, status, applied
		}
		return res
	case discoverDoneMsg:
		if o.cfg.Provider != msgi.provider {
			return settingsResult{outcome: outcomeContinue}
		}
		o.ApplyDiscovery(msgi)
		return settingsResult{outcome: outcomeContinue, handled: true, models: o.models}
	default:
		if o.pickerActive {
			var cmd tea.Cmd
			o.picker, cmd = o.picker.Update(msgi)
			return settingsResult{outcome: outcomeContinue, handled: true, cmd: cmd}
		}
		return settingsResult{outcome: outcomeContinue}
	}
}

// View renders the open Settings surface.
func (o *SettingsOverlay) View() string {
	view := settingsView(o.settingsForm)
	if o.status != "" {
		view += "\n" + o.theme.statusStyle.Render("   "+o.status)
	}
	if o.height <= 0 {
		return view
	}
	lines := strings.Split(view, "\n")
	if len(lines) <= o.height {
		return view
	}
	footerRows := min(4, o.height)
	bodyRows := o.height - footerRows
	return strings.Join(append(lines[:bodyRows], lines[len(lines)-footerRows:]...), "\n")
}

// beginDiscovery arms the loading state for the draft config and returns the
// discovery command; nil when discovery is unavailable.
func (o *SettingsOverlay) beginDiscovery() tea.Cmd {
	if o.discover == nil {
		return nil
	}
	o.discoverState = discoverLoading
	o.discoverErr = ""
	o.models = []string{o.cfg.Model}
	return discoverCmd(o.discover, o.cfg)
}

// ApplyDiscovery folds one discovery result into the form; callers drop the
// message before calling when the overlay is closed or stale.
func (o *SettingsOverlay) ApplyDiscovery(msg discoverDoneMsg) {
	o.models = msg.models
	o.discoverState = discoverIdle
	o.discoverErr = ""
	if msg.err != nil {
		o.discoverErr = msg.err.Error()
		o.discoverState = discoverError
		return
	}
	if len(msg.models) != 0 && indexOf(msg.models, o.cfg.Model) < 0 {
		o.cfg.Model = msg.models[0]
	}
}

// Save persists the draft via the save seams and reports the resulting status
// message alongside the accepted config; the caller applies the config to the
// live session.
func (o *SettingsOverlay) Save() (config.Config, string, bool) {
	cfg := o.draft()
	var status string
	applied := false
	if o.save == nil {
		status = "view-only"
		applied = true
	} else if err := o.save(cfg); err != nil {
		status = "save failed: " + err.Error()
	} else {
		status = "saved"
		applied = true
	}
	if applied {
		o.original = cfg
		o.status = "Settings saved"
		if o.saveBack != nil {
			o.saveBack(cfg)
		}
	} else {
		o.status = status
	}
	return cfg, status, applied
}

// settingsView renders the Settings surface: a focused row per settable knob, the focused row highlighted, a Save/Cancel footer.
func settingsView(f settingsForm) string {
	th := f.theme
	if f.confirmDiscard {
		keep, discard := "[ Keep editing ]", "[ Discard ]"
		if f.discardSelected {
			discard = lipgloss.NewStyle().Bold(true).Reverse(true).Render("  Discard  ")
		} else {
			keep = lipgloss.NewStyle().Bold(true).Reverse(true).Render("  Keep editing  ")
		}
		return th.headerStyle.Render("Discard unsaved changes?") + "\n\nYour changes have not been saved.\n\n" + keep + "  " + discard + "\n" + th.statusStyle.Render("←/→ choose · enter confirm · esc keep editing")
	}
	var b strings.Builder
	title := "Eitri Settings"
	if f.dirty() {
		title += th.statusStyle.Render(" • unsaved changes")
	}
	b.WriteString(th.headerStyle.Render(title))
	b.WriteString("\n")

	rows := []struct {
		field int
		name  string
		val   string
	}{
		{fieldProvider, "Provider", f.cfg.Provider},
		{fieldModel, "Model", f.Model()},
		{fieldThinking, "Deep thinking", thinkingModeLabel(f.cfg.ThinkingEnabled)},
		{fieldEffort, "Reasoning depth", f.cfg.ReasoningEffort},
		{fieldMaxTurns, "Tool loop limit", fmt.Sprintf("%d", f.cfg.MaxTurns)},
		{fieldContextOverflowRecovery, "Context overflow recovery", thinkingModeLabel(f.cfg.ContextOverflowRecovery)},
		{fieldTheme, "Theme", f.cfg.Theme},
		{fieldCoTCollapsed, "Collapse thinking", thinkingModeLabel(f.cfg.CoTCollapsedByDefault)},
		{fieldToolResultsCollapsed, "Collapse tool output", thinkingModeLabel(f.cfg.ToolResultsCollapsedByDefault)},
		{fieldPaths, "Writable paths", pathSummary(f)},
	}
	if f.cfg.Provider == string(provider.ProviderOpenCodeGo) {
		rows = append(rows, struct{ field int; name string; val string }{fieldOpenCodeKey, "OpenCode API key", maskKey(f.cfg.OpenCodeGo.Key)})
	}
	if f.cfg.Provider == string(provider.ProviderCustomOpenAI) {
		rows = append(rows,
			struct{ field int; name string; val string }{fieldCustomOpenAIBaseURL, "Base URL", f.cfg.CustomOpenAI.BaseURL},
			struct{ field int; name string; val string }{fieldCustomOpenAIKey, "API key", maskKey(f.cfg.CustomOpenAI.Key)},
		)
	}
	sections := []struct {
		label string
		start int
	}{
		{g("🤖 model", "model"), fieldProvider},
		{g("🧠 reasoning & limits", "reasoning & limits"), fieldThinking},
		{g("🎨 appearance", "appearance"), fieldTheme},
		{g("🛡 workspace access", "workspace access"), fieldPaths},
	}
	emit := func(label string) {
		b.WriteString(th.statusStyle.Render("   " + hr() + " " + label + " " + hr()))
		b.WriteString("\n")
	}
	writePalette := func() {
		b.WriteString("       Palette ")
		for _, c := range []color.Color{th.accent, th.ok, th.error, th.shell, th.file, th.web, th.skill} {
			b.WriteString(" " + lipgloss.NewStyle().Foreground(c).Render(g("██", "##")))
		}
		b.WriteString("\n")
	}
	for _, r := range rows {
		for _, sec := range sections {
			if r.field == sec.start {
				emit(sec.label)
			}
		}
		marker := " "
		if f.field == r.field {
			marker = "▸"
		}
		fmt.Fprintf(&b, "%-2s%s %-20s %s\n", "", marker, r.name, r.val)
		if r.field == fieldPaths {
			writePathList(&b, f, th)
		}
		if r.field == fieldTheme {
			writePalette()
		}
		if r.field == fieldThinking && !f.cfg.ThinkingEnabled && f.thinkingSuppression != nil && !f.thinkingSuppression() {
			b.WriteString(th.statusStyle.Render("   " + g("⚠", "!") + " This provider always uses reasoning"))
			b.WriteString("\n")
		}
		if f.textInputActive && f.textInputField == r.field {
			b.WriteString("     " + f.textInput.View())
			b.WriteString("\n")
		}
	}

	switch f.discoverState {
	case discoverLoading:
		b.WriteString(th.statusStyle.Render("   discovering models" + g("…", "...")))
		b.WriteString("\n")
	case discoverError:
		b.WriteString(th.statusStyle.Render("   model discovery failed: " + f.discoverErr))
		b.WriteString("\n")
	default:
	}

	if f.telemetry != nil {
		b.WriteString(th.statusStyle.Render(fmt.Sprintf(
			"   cache:%.0f%%", f.telemetry.HitPercent(),
		)))
		b.WriteString("\n")
	}

	b.WriteString(th.statusStyle.Render("   " + settingsHelp(f)))
	b.WriteString("\n")

	dirty := f.dirty()
	state := g("✓", "OK") + " Settings are up to date"
	save := "[ Save changes ]"
	if dirty {
		state = g("●", "*") + " Unsaved changes"
	}
	cancel := "[ Cancel ]"
	focusButton := func(label string) string {
		return lipgloss.NewStyle().Bold(true).Reverse(true).Render(" " + label + " ")
	}
	if f.field == fieldSave {
		save = focusButton("Save changes")
	}
	if f.field == fieldCancel {
		cancel = focusButton("Cancel")
	}
	b.WriteString("\n" + th.statusStyle.Render(strings.Repeat(hr(), 58)) + "\n")
	b.WriteString(state + "   " + save + "  " + cancel + "\n")
	b.WriteString(th.statusStyle.Render(g("↑/↓ navigate", "up/down navigate") + " " + g("·", ".") + " " + g("←/→ adjust", "left/right adjust") + " " + g("·", ".") + " ctrl+, save " + g("·", ".") + " esc close"))
	return b.String()
}

// startSettings opens the Settings surface and returns the command to run.
func (m Model) startSettings() (tea.Model, tea.Cmd) {
	o, cmd := openSettingsOverlay(m.deps.Config, m.deps.Models, m.tx.theme, m.telemetry, m.deps.ThinkingSuppression, m.deps)
	o.height = m.tx.height
	m.settings = o
	return m, cmd
}

// updateSettings routes one message through the open Settings overlay and applies its outcome.
func (m Model) updateSettings(msg tea.Msg) (tea.Model, tea.Cmd) {
	res := m.settings.Handle(msg)
	switch res.outcome {
	case outcomeClosed:
		m.settings = nil
		// Closing the overlay can reveal a face made dirty by the session's
		// last save; arm its upload so the theme/rail-width change lands.
		return m, tea.Batch(res.cmd, m.queueFaceDrawCmd())
	case outcomeSaved:
		if res.applied {
			m.feedback = successFeedback(res.status)
		} else if strings.HasPrefix(res.status, "save failed:") {
			m.feedback = failureFeedback(res.status)
		} else {
			m.feedback = neutralFeedback(res.status)
		}
		if res.applied {
			// Only changes the face actually renders at are damage: its theme
			// styling and the rail width it derives its column count from (rail
			// width has no settings knob; ctrl+x/z own it, and an unset width
			// resolves to the default like the transcript does).
			savedRailWidth := res.saved.RailWidth
			if savedRailWidth == 0 {
				savedRailWidth = defaultRailWidth
			}
			faceChanged := res.saved.Theme != m.tx.configTheme || savedRailWidth != m.tx.railWidthOrDefault()
			m.deps.Config = *res.saved
			m.tx.applySettings(*res.saved)
			m.tx.reasoningEffort = res.saved.ReasoningEffort
			m.runtime.SetThinkingEnabled(res.saved.ThinkingEnabled)
			if m.deps.Rail != nil {
				m.deps.Rail.ApplyConfig(*res.saved)
			}
			if faceChanged {
				m.faceDirty = true
			}
		}
	}
	return m, res.cmd
}

// discoverCmd runs one on-demand provider model discovery off the main loop and reports its result .
func discoverCmd(discover func(ctx context.Context, cfg config.Config) ([]string, error), cfg config.Config) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		models, err := discover(ctx, cfg)
		return discoverDoneMsg{provider: cfg.Provider, models: models, err: err}
	})
}
