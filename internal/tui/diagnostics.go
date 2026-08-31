package tui

import "time"

// DiagnosticsConfig is the startup/test-construction seam for opt-in TUI
// diagnostics. The zero value keeps diagnostics disabled and preserves normal
// rendering.
type DiagnosticsConfig struct {
	RenderStats     bool
	FrameSnapshots  bool
	RawFrameCapture bool
	Pprof           bool
}

type RenderFrameStats struct {
	Frame          int
	RenderDuration time.Duration
	OutputBytes    int
	TerminalWidth  int
	TerminalHeight int
	Phase          string
	Busy           bool
	Follow         bool
	ViewportWidth  int
	ViewportHeight int
	ScrollOffset   int
}

type renderDiagnostics struct {
	frames []RenderFrameStats
}

func (d *renderDiagnostics) record(f RenderFrameStats) {
	if d == nil {
		return
	}
	d.frames = append(d.frames, f)
}

func (m *Model) recordRenderFrame(cost time.Duration, content string) {
	if !m.diagnostics.RenderStats {
		return
	}
	m.renderDiagnostics.record(RenderFrameStats{
		Frame:          len(m.renderDiagnostics.frames) + 1,
		RenderDuration: cost,
		OutputBytes:    len(content),
		TerminalWidth:  m.tx.width,
		TerminalHeight: m.tx.height,
		Phase:          m.tx.phase().String(),
		Busy:           m.tx.busy,
		Follow:         m.tx.histFollow,
		ViewportWidth:  m.tx.histViewport.Width(),
		ViewportHeight: m.tx.histViewport.Height(),
		ScrollOffset:   m.tx.histViewport.YOffset(),
	})
}

func (m Model) RenderDiagnosticFrames() []RenderFrameStats {
	if !m.diagnostics.RenderStats {
		return nil
	}
	if m.renderDiagnostics == nil {
		return nil
	}
	return append([]RenderFrameStats(nil), m.renderDiagnostics.frames...)
}
