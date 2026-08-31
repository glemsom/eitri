package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDiagnosticsConfigDefaultsDisabled(t *testing.T) {
	m := NewModelCfg(Dependencies{})

	if m.diagnostics.RenderStats || m.diagnostics.FrameSnapshots || m.diagnostics.RawFrameCapture || m.diagnostics.Pprof {
		t.Fatalf("diagnostics default = %+v, want all disabled", m.diagnostics)
	}
}

func TestDiagnosticsConfigCanBeInjected(t *testing.T) {
	m := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{RenderStats: true, FrameSnapshots: true, RawFrameCapture: true, Pprof: true}})

	if !m.diagnostics.RenderStats || !m.diagnostics.FrameSnapshots || !m.diagnostics.RawFrameCapture || !m.diagnostics.Pprof {
		t.Fatalf("diagnostics injection = %+v, want all enabled", m.diagnostics)
	}
}

func TestDisabledDiagnosticsDoNotChangeInitialRender(t *testing.T) {
	without := NewModelCfg(Dependencies{})
	withDisabled := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{}})

	nm, _ := without.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	without = nm.(Model)
	nm, _ = withDisabled.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	withDisabled = nm.(Model)

	if view(without) != view(withDisabled) {
		t.Fatalf("disabled diagnostics changed initial render")
	}
}
