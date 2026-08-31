package tui

import "time"

// DiagnosticsConfig is the startup/test-construction seam for opt-in TUI
// diagnostics. The zero value keeps diagnostics disabled and preserves normal
// rendering.
type DiagnosticsConfig struct {
	RenderStats        bool
	FrameSnapshots     bool
	RawFrameCapture    bool
	Pprof              bool
	RenderSummaryEvery int
	RenderSummaryLimit int
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

type RenderDiagnosticSummary struct {
	StartFrame            int
	EndFrame              int
	FrameCount            int
	FrameRate             float64
	AverageRenderDuration time.Duration
	MaxRenderDuration     time.Duration
	AverageOutputBytes    int
	MaxOutputBytes        int
	Phase                 string
	Busy                  bool
	Follow                bool
	ViewportWidth         int
	ViewportHeight        int
	ScrollOffset          int
	Evidence              string
}

type renderDiagnostics struct {
	frames    []RenderFrameStats
	summaries []RenderDiagnosticSummary
}

func (d *renderDiagnostics) record(f RenderFrameStats, summaryEvery, summaryLimit int) {
	if d == nil {
		return
	}
	d.frames = append(d.frames, f)
	if summaryEvery <= 0 || len(d.frames)%summaryEvery != 0 {
		return
	}
	d.summaries = append(d.summaries, summarizeRenderFrames(d.frames[len(d.frames)-summaryEvery:]))
	if summaryLimit > 0 && len(d.summaries) > summaryLimit {
		d.summaries = append([]RenderDiagnosticSummary(nil), d.summaries[len(d.summaries)-summaryLimit:]...)
	}
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
	}, m.diagnostics.RenderSummaryEvery, m.diagnostics.RenderSummaryLimit)
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

func summarizeRenderFrames(frames []RenderFrameStats) RenderDiagnosticSummary {
	if len(frames) == 0 {
		return RenderDiagnosticSummary{}
	}
	var totalDuration time.Duration
	var maxDuration time.Duration
	var totalBytes int
	var maxBytes int
	for _, f := range frames {
		totalDuration += f.RenderDuration
		if f.RenderDuration > maxDuration {
			maxDuration = f.RenderDuration
		}
		totalBytes += f.OutputBytes
		if f.OutputBytes > maxBytes {
			maxBytes = f.OutputBytes
		}
	}
	last := frames[len(frames)-1]
	elapsed := totalDuration.Seconds()
	var rate float64
	if elapsed > 0 {
		rate = float64(len(frames)) / elapsed
	}
	return RenderDiagnosticSummary{
		StartFrame:            frames[0].Frame,
		EndFrame:              last.Frame,
		FrameCount:            len(frames),
		FrameRate:             rate,
		AverageRenderDuration: totalDuration / time.Duration(len(frames)),
		MaxRenderDuration:     maxDuration,
		AverageOutputBytes:    totalBytes / len(frames),
		MaxOutputBytes:        maxBytes,
		Phase:                 last.Phase,
		Busy:                  last.Busy,
		Follow:                last.Follow,
		ViewportWidth:         last.ViewportWidth,
		ViewportHeight:        last.ViewportHeight,
		ScrollOffset:          last.ScrollOffset,
		Evidence:              "in-memory render diagnostics: RenderDiagnosticFrames and RenderDiagnosticSummaries",
	}
}

func (m Model) RenderDiagnosticSummaries() []RenderDiagnosticSummary {
	if !m.diagnostics.RenderStats {
		return nil
	}
	if m.renderDiagnostics == nil {
		return nil
	}
	return append([]RenderDiagnosticSummary(nil), m.renderDiagnostics.summaries...)
}
