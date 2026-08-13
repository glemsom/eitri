package tui

import (
	"fmt"
	"strings"

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
	fieldPaths
	fieldSave
	fieldCount
)

// settingVals are the option lists for cycle-style setters.
var (
	// providerFams are the documented provider families (eitri.md §2.2, T11).
	providerFams = []string{"opencode-go", "github-copilot", "custom-openai"}
	// effortTiers are the meaningful reasoning-effort tiers in cycle order
	// low→medium→high→max (docs/spec.md §6). Both low and medium are exposed
	// as first-class options.
	effortTiers = []string{"low", "medium", "high", "max"}
)

// discoverState is the on-demand model-discovery lifecycle of the Settings
// surface (issue #89 AC2). It lets the panel show where discovery stands
// instead of failing silently when a provider has no cached model list.
type discoverState int

const (
	// discoverIdle means no on-demand discovery is running: a model list is
	// already known (pre-seeded) or discovery is unwired.
	discoverIdle discoverState = iota
	// discoverLoading means provider model discovery is in flight.
	discoverLoading
	// discoverError means discovery finished but failed; discoverErr carries it.
	discoverError
)

// settingsForm is the pure state behind the TUI Settings surface (T12). It
// edits a draft copy of config.Config and yields a saved config; navigation
// and value stepping are deterministic so the panel is unit-testable without a
// terminal. It is deliberately config-only: the model list is surfaced from
// provider discovery but owned by the caller, and the live telemetry readout
// (issue #89 AC4) is borrowed from the status strip.
type settingsForm struct {
	cfg    config.Config
	models []string
	field  int
	// pathBuf holds the free-form extra_writable_paths draft while focused.
	pathBuf string
	// telemetry, when non-nil, backs the live cache hit-ratio + cost readout
	// rendered in the panel (issue #89 AC4). It is the same read-only surface
	// the status strip uses and is never mutated from the panel.
	telemetry *Telemetry
	// discoverState tracks on-demand provider model discovery (issue #89 AC2).
	discoverState discoverState
	// discoverErr is the surfaced failure when discoverState == discoverError.
	discoverErr string
}

// newSettingsForm seeds the form with the loaded config and the discovered
// model list.
func newSettingsForm(cfg config.Config, models []string) settingsForm {
	f := settingsForm{cfg: cfg, models: models, field: 0}
	if len(models) == 0 {
		f.models = []string{cfg.Model}
	}
	f.pathBuf = strings.Join(cfg.ExtraWritablePaths, ",")
	return f
}

// Model returns the currently selected model (a discovered id, or the
// configured model when none is discovered).
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

// adjust steps the focused value. d is ±1 for cycle setters, ±step for
// numeric steppers, or a direct edit for the free-form path field.
func (f *settingsForm) adjust(d int) {
	switch f.field {
	case fieldProvider:
		f.cfg.Provider = cycle(f.cfg.Provider, providerFams, d)
	case fieldModel:
		if len(f.models) > 0 {
			f.cfg.Model = cycle(f.cfg.Model, f.models, d)
		}
	case fieldThinking:
		// Reasoning mode is a boolean on/off switch; either arrow flips it.
		if d != 0 {
			f.cfg.ThinkingEnabled = !f.cfg.ThinkingEnabled
		}
	case fieldEffort:
		f.cfg.ReasoningEffort = cycle(f.cfg.ReasoningEffort, effortTiers, d)
	case fieldMaxTurns:
		f.cfg.MaxTurns = stepInt(f.cfg.MaxTurns, d, 25, 0, 10000)
	case fieldFraction:
		f.cfg.CompactionFraction = stepFrac(f.cfg.CompactionFraction, d)
	case fieldPaths:
		// Free-form edit is handled by the caller via SetPathBuf; adjust nudges
		// the focused field forward so enter still lands on Save.
		f.next()
	case fieldSave:
	}
}

// SetPathBuf replaces the extra_writable_paths draft and parses it back into
// the config's slice (comma-separated, trimmed, empties dropped).
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

// thinkingModeLabel renders the reasoning mode value (on/off) for the Settings
// panel, reflecting the thinking_enabled config (issue #56).
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

// settingsView renders the Settings surface: a focused row per settable knob,
// the focused row highlighted, a Save/Cancel footer. It is deterministic for
// render-testing.
func settingsView(f settingsForm) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Eitri Settings"))
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
		{"Writable", f.pathBuf},
	}
	for i, r := range rows {
		name := r.name
		if f.field == i {
			name = "\u25b8 " + name
		} else {
			name = "   " + name
		}
		fmt.Fprintf(&b, "%-2s%-10s %s\n", "", name, r.val)
	}

	// Provider model-discovery status (issue #89 AC2): loading/error/summary
	// reflects what the list behind the Model row shows. Deterministic for
	// render-testing.
	switch f.discoverState {
	case discoverLoading:
		b.WriteString(statusStyle.Render("   discovering models…"))
		b.WriteString("\n")
	case discoverError:
		b.WriteString(statusStyle.Render("   model discovery failed: " + f.discoverErr))
		b.WriteString("\n")
	default:
		// Idle: already-loaded or unwired, so no status line is needed.
	}

	// Live telemetry readout (issue #89 AC4): the same cache hit-ratio + cost
	// the status strip tracks, borrowed read-only so switching provider/model
	// and watching cost happen in one pane.
	if f.telemetry != nil {
		b.WriteString(statusStyle.Render(fmt.Sprintf(
			"   cache:%.0f%% · cost:%s", f.telemetry.hitPercent(), formatCost(f.telemetry.cost()),
		)))
		b.WriteString("\n")
	}

	save := "[ Save ]"
	cancel := "[ Cancel ]"
	if f.field == fieldSave {
		save = "[" + statusStyle.Render(" Save ") + "]"
	}
	b.WriteString("\n")
	b.WriteString(save + "  " + cancel + "\n")
	b.WriteString(statusStyle.Render("tab/enter: navigate · arrows/+/−: adjust · esc: close"))
	return b.String()
}
