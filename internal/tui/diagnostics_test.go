package tui

import (
	"os"
	"strings"
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

func TestRenderDiagnosticSummariesArePeriodicBoundedAndContentFree(t *testing.T) {
	m := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{RenderStats: true, RenderSummaryEvery: 2, RenderSummaryLimit: 2}})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)

	for range 5 {
		_ = m.View()
	}

	summaries := m.RenderDiagnosticSummaries()
	if len(summaries) != 2 {
		t.Fatalf("summaries = %d, want bounded last 2", len(summaries))
	}
	first := summaries[0]
	if first.StartFrame != 1 || first.EndFrame != 2 || first.FrameCount != 2 || first.FrameRate <= 0 {
		t.Fatalf("first summary missing frame count/rate window: %+v", first)
	}
	if first.AverageRenderDuration <= 0 || first.MaxRenderDuration <= 0 || first.AverageOutputBytes == 0 || first.MaxOutputBytes == 0 {
		t.Fatalf("summary missing render cost/output size: %+v", first)
	}
	if first.Phase != "idle" || first.Busy || !first.Follow || first.ViewportWidth <= 0 || first.ViewportHeight <= 0 || first.ScrollOffset != 0 {
		t.Fatalf("summary missing phase/viewport/follow facts: %+v", first)
	}
	if first.Evidence != "in-memory render diagnostics: RenderDiagnosticFrames and RenderDiagnosticSummaries" {
		t.Fatalf("evidence = %q", first.Evidence)
	}
}

func TestFrameSnapshotsOptInWritesBoundedAnsiStrippedFiles(t *testing.T) {
	dir := t.TempDir()
	m := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{FrameSnapshots: true, FrameSnapshotDir: dir, FrameSnapshotLimit: 1, FrameSnapshotByteLimit: 10_000}})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)

	_ = m.View()
	_ = m.View()

	snaps := m.RenderDiagnosticFrameSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want bounded last 1", len(snaps))
	}
	if snaps[0].Frame != 2 || snaps[0].Path == "" || snaps[0].OutputBytes == 0 {
		t.Fatalf("snapshot missing frame metadata: %+v", snaps[0])
	}
	body, err := os.ReadFile(snaps[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("snapshot must be ANSI-stripped by default, got %q", text)
	}
	if !strings.Contains(text, "frame: 2") || !strings.Contains(text, "phase: idle") || !strings.Contains(text, "--- content ---") {
		t.Fatalf("snapshot missing correlating metadata/content sections:\n%s", text)
	}
}
