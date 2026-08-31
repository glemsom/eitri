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

func TestRenderStatsDisabledCollectsNoFrames(t *testing.T) {
	m := NewModelCfg(Dependencies{})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)

	_ = m.View()

	if got := m.RenderDiagnosticFrames(); len(got) != 0 {
		t.Fatalf("disabled render stats collected %d frames, want 0", len(got))
	}
}

func TestRenderStatsRecordFrameMetadataWithoutContent(t *testing.T) {
	m := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{RenderStats: true}})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)

	_ = m.View()
	frames := m.RenderDiagnosticFrames()
	if len(frames) != 1 {
		t.Fatalf("render diagnostic frames = %d, want 1", len(frames))
	}
	f := frames[0]
	if f.Frame != 1 || f.OutputBytes == 0 || f.RenderDuration <= 0 {
		t.Fatalf("frame stats missing render cost/size facts: %+v", f)
	}
	if f.TerminalWidth != 80 || f.TerminalHeight != 24 {
		t.Fatalf("terminal facts = %dx%d, want 80x24", f.TerminalWidth, f.TerminalHeight)
	}
	if f.Phase != "idle" || f.Busy || !f.Follow {
		t.Fatalf("phase/busy/follow facts = %+v, want idle/not busy/follow", f)
	}
	if f.ViewportHeight <= 0 || f.ViewportWidth <= 0 || f.ScrollOffset != 0 {
		t.Fatalf("viewport facts missing: %+v", f)
	}
	if f.Phase == "" {
		t.Fatalf("stats must include phase without transcript content: %+v", f)
	}
}
