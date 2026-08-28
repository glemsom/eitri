package tui

import (
	"context"
	"fmt"
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
)

// Field indexes for the settings form, in display/cycle order.
const (
	fieldProvider = iota
	fieldModel
	fieldThinking
	fieldEffort
	fieldMaxTurns
	fieldFraction
	fieldCoTCollapsed
	fieldToolResultsCollapsed
	fieldTheme
	fieldPaths
	fieldSave
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
	models              []string
	field               int
	pathBuf             string
	telemetry           *Telemetry
	discoverState       discoverState
	discoverErr         string
	thinkingSuppression func() bool
}

// newSettingsForm seeds the form with the loaded config and the discovered model list.
func newSettingsForm(cfg config.Config, models []string) settingsForm {
	f := settingsForm{cfg: cfg, models: models, field: 0, theme: defaultTheme}
	if len(models) == 0 {
		f.models = []string{cfg.Model}
	}
	f.pathBuf = strings.Join(cfg.ExtraWritablePaths, ",")
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

// step moves focus by ±1, wrapping within [0, fieldCount).
func (f *settingsForm) step(d int) {
	f.field = (f.field + d + fieldCount) % fieldCount
}

// next advances to the next field (wrap).
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
	case fieldFraction:
		f.cfg.CompactionFraction = stepFrac(f.cfg.CompactionFraction, d)
	case fieldTheme:
		f.cfg.Theme = cycle(f.cfg.Theme, supportedThemes, d)
		f.theme = themeFor(f.cfg.Theme)
	case fieldPaths:
		f.next()
	case fieldSave:
	}
}

// SetPathBuf replaces the extra_writable_paths draft and parses it back into the config's slice (comma-separated, trimmed, empties dropped).
func (f *settingsForm) SetPathBuf(s string) {
	f.pathBuf = s
	f.cfg.ExtraWritablePaths = splitPaths(s)
}

// draft returns the current edited config (parses the path field fresh).
func (f *settingsForm) draft() config.Config {
	c := f.cfg
	c.ExtraWritablePaths = splitPaths(f.pathBuf)
	return c
}

// onSave reports whether the focused field is the Save button.
func (f settingsForm) onSave() bool { return f.field == fieldSave }

// thinkingModeLabel renders the reasoning mode value (on/off) for the Settings panel, reflecting the thinking_enabled config.
func thinkingModeLabel(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func splitPaths(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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

func stepFrac(v float64, d int) float64 {
	const step = 0.05
	nv := v + float64(d)*step
	if nv < 0 {
		nv = 0
	}
	if nv > 1 {
		nv = 1
	}
	return nv
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
// removed in issue #374). When no models are known yet and discovery is
// available it arms the loading state and returns the discovery command.
func openSettingsOverlay(cfg config.Config, models []string, theme Theme, telemetry *Telemetry, thinkingSuppressed func() bool, deps Dependencies) (*SettingsOverlay, tea.Cmd) {
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
// provider change).
func (o *SettingsOverlay) Key(k tea.KeyPressMsg) (settingsKeyOutcome, tea.Cmd) {
	s := &o.settingsForm
	switch k.String() {
	case "esc", "ctrl+c":
		return outcomeClosed, nil
	case "tab", "enter":
		if k.String() == "enter" && s.onSave() {
			return outcomeSaved, nil
		}
		if s.field == fieldPaths {
			s.cfg.ExtraWritablePaths = splitPaths(s.pathBuf)
		}
		s.next()
	case "up", "shift+up", "left":
		before := s.cfg.Provider
		s.adjust(-1)
		if s.cfg.Provider != before {
			return outcomeContinue, o.beginDiscovery()
		}
	case "down", "shift+down", "right":
		before := s.cfg.Provider
		s.adjust(1)
		if s.cfg.Provider != before {
			return outcomeContinue, o.beginDiscovery()
		}
	default:
		if s.field == fieldPaths {
			if k.String() == "backspace" && len(s.pathBuf) > 0 {
				s.SetPathBuf(s.pathBuf[:len(s.pathBuf)-1])
			} else if k.String() != "backspace" {
				s.SetPathBuf(s.pathBuf + k.Text)
			}
		}
	}
	return outcomeContinue, nil
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
		return settingsResult{outcome: outcomeContinue}
	}
}

// View renders the open Settings surface.
func (o *SettingsOverlay) View() string { return settingsView(o.settingsForm) }

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
	if applied && o.saveBack != nil {
		o.saveBack(cfg)
	}
	return cfg, status, applied
}

// settingsView renders the Settings surface: a focused row per settable knob, the focused row highlighted, a Save/Cancel footer.
func settingsView(f settingsForm) string {
	th := f.theme
	var b strings.Builder
	b.WriteString(th.headerStyle.Render("Eitri Settings"))
	b.WriteString("\n")

	rows := []struct {
		name string
		val  string
	}{
		{"Provider", f.cfg.Provider},
		{"Model", f.Model()},
		{"Thinking", thinkingModeLabel(f.cfg.ThinkingEnabled)},
		{"Reasoning", f.cfg.ReasoningEffort},
		{"Max turns", fmt.Sprintf("%d", f.cfg.MaxTurns)},
		{"Compaction", fmt.Sprintf("%.2f", f.cfg.CompactionFraction)},
		{"CoT collapsed", thinkingModeLabel(f.cfg.CoTCollapsedByDefault)},
		{"Tool results collapsed", thinkingModeLabel(f.cfg.ToolResultsCollapsedByDefault)},
		{"Theme", f.cfg.Theme},
		{"Writable", f.pathBuf},
	}
	sections := []struct {
		label string
		start int
	}{
		{"model", fieldProvider},
		{"behavior", fieldThinking},
		{"appearance", fieldTheme},
	}
	emit := func(label string) {
		b.WriteString(th.statusStyle.Render("   " + hr() + " " + label + " " + hr()))
		b.WriteString("\n")
	}
	for i, r := range rows {
		for _, sec := range sections {
			if i == sec.start {
				emit(sec.label)
			}
		}
		name := r.name
		if f.field == i {
			name = "\u25b8 " + name
		} else {
			name = "   " + name
		}
		fmt.Fprintf(&b, "%-2s%-10s %s\n", "", name, r.val)
	}

	if !f.cfg.ThinkingEnabled && f.thinkingSuppression != nil && !f.thinkingSuppression() {
		b.WriteString(th.statusStyle.Render("   " + g("⚠", "!") + " reasoning cannot be disabled on this provider"))
		b.WriteString("\n")
	}

	b.WriteString(th.statusStyle.Render("   palette"))
	for _, c := range []color.Color{th.accent, th.ok, th.error, th.shell, th.file, th.web, th.skill} {
		b.WriteString(" " + lipgloss.NewStyle().Foreground(c).Render(g("██", "##")))
	}
	b.WriteString("\n")

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
			"   cache:%.0f%%", f.telemetry.hitPercent(),
		)))
		b.WriteString("\n")
	}

	save := "[ Save ]"
	cancel := "[ Cancel ]"
	if f.field == fieldSave {
		save = "[" + th.statusStyle.Render(" Save ") + "]"
	}
	b.WriteString("\n")
	b.WriteString(save + "  " + cancel + "\n")
	b.WriteString(th.statusStyle.Render("tab/enter: navigate " + g("·", ".") + " arrows/+" + g("−", "-") + ": adjust " + g("·", ".") + " esc: close"))
	return b.String()
}

// startSettings opens the Settings surface and returns the command to run.
func (m Model) startSettings() (tea.Model, tea.Cmd) {
	o, cmd := openSettingsOverlay(m.deps.Config, m.deps.Models, m.tx.theme, m.telemetry, m.deps.ThinkingSuppression, m.deps)
	m.settings = o
	return m, cmd
}

// updateSettings routes one message through the open Settings overlay and applies its outcome.
func (m Model) updateSettings(msgi tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	res := m.settings.Handle(msgi)
	switch res.outcome {
	case outcomeClosed:
		m.settings = nil
	case outcomeSaved:
		m.savedMsg = res.status
		if res.applied {
			m.deps.Config = *res.saved
			m.tx.applySettings(*res.saved)
			m.tx.reasoningEffort = res.saved.ReasoningEffort
			m.session.SetThinkingEnabled(res.saved.ThinkingEnabled)
		}
		m.settings = nil
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
