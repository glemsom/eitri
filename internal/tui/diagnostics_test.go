package tui

import (
	"context"
	"fmt"
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

func TestRenderStatsTrackResizeScrollAndFollowThroughModel(t *testing.T) {
	m := NewModelCfg(Dependencies{Turn: streamingTurn, Events: NewEventFeed(), Diagnostics: DiagnosticsConfig{RenderStats: true, RenderSummaryEvery: 2}})
	m = resizeTo(t, m, 80, 24)
	_ = m.View()

	m = resizeTo(t, m, 60, 10)
	m = longStreamModelWithDiagnostics(t, m)
	_ = m.View()

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	_ = m.View()

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	_ = m.View()

	frames := m.RenderDiagnosticFrames()
	if len(frames) != 4 {
		t.Fatalf("render diagnostic frames = %d, want 4", len(frames))
	}
	streaming := frames[1]
	if streaming.TerminalWidth != 60 || streaming.TerminalHeight != 10 || streaming.ViewportHeight <= 0 {
		t.Fatalf("resized terminal/viewport facts missing: %+v", streaming)
	}
	paused := frames[2]
	if paused.Follow || paused.ScrollOffset == streaming.ScrollOffset {
		t.Fatalf("scroll diagnostics must show paused follow and changed scroll offset: before=%+v paused=%+v", streaming, paused)
	}
	resumed := frames[3]
	if !resumed.Follow || resumed.ScrollOffset <= paused.ScrollOffset {
		t.Fatalf("End diagnostics must show resumed follow at newer scroll offset: paused=%+v resumed=%+v", paused, resumed)
	}

	summaries := m.RenderDiagnosticSummaries()
	last := summaries[len(summaries)-1]
	if last.TerminalWidth != 60 || last.TerminalHeight != 10 || !last.Follow || last.ViewportWidth <= 0 || last.ViewportHeight <= 0 || last.ScrollOffset != resumed.ScrollOffset {
		t.Fatalf("summary must carry latest terminal, viewport, and follow facts: %+v", last)
	}
}

func longStreamModelWithDiagnostics(t *testing.T, m Model) Model {
	t.Helper()
	m = typeText(t, m, "prompt")
	m, _ = submitBusy(t, m)
	for i := 0; i < 40; i++ {
		m = applyDelta(t, m, fmt.Sprintf("token%d %s", i, strings.Repeat("w", 30)))
	}
	return m
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

func TestFrameSnapshotsPreserveRenderedTranscriptLayout(t *testing.T) {
	dir := t.TempDir()
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) {
			return TurnResult{
				Reasoning: "inspect stale row",
				Answer:    "Unicode: λ ⚒\n\n- first row\n- second row with wide glyph 漢",
			}, nil
		},
		Diagnostics: DiagnosticsConfig{FrameSnapshots: true, FrameSnapshotDir: dir, FrameSnapshotLimit: 3, FrameSnapshotByteLimit: 20_000},
	})
	m = resize(t, m)
	m = typeText(t, m, "render this")
	m = submitAndWait(t, m)

	_ = m.View()

	snaps := m.RenderDiagnosticFrameSnapshots()
	if len(snaps) == 0 {
		t.Fatal("expected frame snapshot after rendering transcript")
	}
	body, err := os.ReadFile(snaps[len(snaps)-1].Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("snapshot must be inspectable after ANSI stripping, got %q", text)
	}
	for _, want := range []string{"render this", "Unicode: λ ⚒", "• first row", "• second row with wide glyph 漢", "Ask Eitri"} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot missing rendered transcript layout %q:\n%s", want, text)
		}
	}
}

func TestRawFrameCaptureOptInWritesSeparateBoundedMarkedFiles(t *testing.T) {
	dir := t.TempDir()
	m := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{RawFrameCapture: true, RawFrameCaptureDir: dir, RawFrameCaptureLimit: 1, RawFrameCaptureByteLimit: 10_000}})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)

	_ = m.View()
	_ = m.View()

	frames := m.RenderDiagnosticRawFrames()
	if len(frames) != 1 {
		t.Fatalf("raw captures = %d, want bounded last 1", len(frames))
	}
	if frames[0].Frame != 2 || frames[0].Path == "" || frames[0].OutputBytes == 0 {
		t.Fatalf("raw capture missing frame metadata: %+v", frames[0])
	}
	body, err := os.ReadFile(frames[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "raw_frame_capture: true") || !strings.Contains(text, "warning: raw capture may contain terminal escape sequences and transcript content") {
		t.Fatalf("raw capture missing warning marker:\n%s", text)
	}
	if !strings.Contains(text, "--- raw content ---") || strings.Contains(text, "--- content ---") {
		t.Fatalf("raw capture must be separate from ANSI-stripped snapshots:\n%s", text)
	}
}

func TestRawFrameCaptureDisabledWhenRenderStatsEnabled(t *testing.T) {
	dir := t.TempDir()
	m := NewModelCfg(Dependencies{Diagnostics: DiagnosticsConfig{RenderStats: true, RawFrameCaptureDir: dir}})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)

	_ = m.View()

	if got := m.RenderDiagnosticRawFrames(); len(got) != 0 {
		t.Fatalf("raw captures = %d, want disabled by default", len(got))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("raw capture dir entries = %d, want none", len(entries))
	}
}

func TestDiagnosticsEnabledStreamingUsesBatchedRenderFrame(t *testing.T) {
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{Turn: streamingTurn, Events: feed, Diagnostics: DiagnosticsConfig{RenderStats: true}})
	m = resizeTo(t, m, 80, 24)
	m = typeText(t, m, "prompt")
	m, _ = submitBusy(t, m)

	const deltas = 50
	for i := 0; i < deltas; i++ {
		feed.UpdateChan() <- Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: "x"}}
	}
	first, ok := feed.TryNext()
	if !ok {
		t.Fatal("expected a queued stream delta")
	}
	nm, _ := m.Update(eventMsg{update: first})
	m = asModel(t, nm)

	_ = m.View()

	if got := m.tx.messages[len(m.tx.messages)-1].content; len(got) != deltas {
		t.Fatalf("batched stream content length = %d, want %d", len(got), deltas)
	}
	if _, ok := feed.TryNext(); ok {
		t.Fatal("expected diagnostics-enabled stream update to drain queued deltas before rendering")
	}
	frames := m.RenderDiagnosticFrames()
	if len(frames) != 1 {
		t.Fatalf("diagnostic render frames = %d, want 1 batched frame after %d deltas", len(frames), deltas)
	}
	if frames[0].Phase != "answering" || !frames[0].Busy {
		t.Fatalf("diagnostic frame should describe the live answering turn: %+v", frames[0])
	}
}
