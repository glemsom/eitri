package tui

import (
	"fmt"
	"image/color"
	"strings"

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
