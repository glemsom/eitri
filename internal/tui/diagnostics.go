package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// DiagnosticsConfig is the startup/test-construction seam for opt-in TUI
// diagnostics. The zero value keeps diagnostics disabled and preserves normal
// rendering.
type DiagnosticsConfig struct {
	RenderStats              bool
	FrameSnapshots           bool
	RawFrameCapture          bool
	Pprof                    bool
	RenderSummaryEvery       int
	RenderSummaryLimit       int
	FrameSnapshotDir         string
	FrameSnapshotLimit       int
	FrameSnapshotByteLimit   int
	RawFrameCaptureDir       string
	RawFrameCaptureLimit     int
	RawFrameCaptureByteLimit int
}

const (
	defaultRawFrameCaptureLimit     = 20
	defaultRawFrameCaptureByteLimit = 1 << 20
)

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
	TerminalWidth         int
	TerminalHeight        int
	ViewportWidth         int
	ViewportHeight        int
	ScrollOffset          int
	Evidence              string
}

type RenderFrameSnapshot struct {
	Frame       int
	Path        string
	OutputBytes int
}

type RenderRawFrame struct {
	Frame       int
	Path        string
	OutputBytes int
}

type renderDiagnostics struct {
	frames    []RenderFrameStats
	summaries []RenderDiagnosticSummary
	snapshots []RenderFrameSnapshot
	rawFrames []RenderRawFrame
	nextFrame int
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
	if !m.diagnostics.RenderStats && !m.diagnostics.FrameSnapshots && !m.diagnostics.RawFrameCapture {
		return
	}
	m.renderDiagnostics.nextFrame++
	f := RenderFrameStats{
		Frame:          m.renderDiagnostics.nextFrame,
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
	}
	if m.diagnostics.RenderStats {
		m.renderDiagnostics.record(f, m.diagnostics.RenderSummaryEvery, m.diagnostics.RenderSummaryLimit)
	}
	m.recordRenderSnapshot(f, content)
	m.recordRawFrame(f, content)
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
		TerminalWidth:         last.TerminalWidth,
		TerminalHeight:        last.TerminalHeight,
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

func (m *Model) recordRenderSnapshot(f RenderFrameStats, content string) {
	if !m.diagnostics.FrameSnapshots || m.diagnostics.FrameSnapshotDir == "" || m.renderDiagnostics == nil {
		return
	}
	body := ansiStrip(content)
	if limit := m.diagnostics.FrameSnapshotByteLimit; limit > 0 && len(body) > limit {
		body = body[:limit]
	}
	path := filepath.Join(m.diagnostics.FrameSnapshotDir, fmt.Sprintf("frame-%06d.txt", f.Frame))
	text := fmt.Sprintf("frame: %d\nphase: %s\nbusy: %t\nfollow: %t\nterminal: %dx%d\nviewport: %dx%d\nscroll_offset: %d\noutput_bytes: %d\n--- content ---\n%s", f.Frame, f.Phase, f.Busy, f.Follow, f.TerminalWidth, f.TerminalHeight, f.ViewportWidth, f.ViewportHeight, f.ScrollOffset, f.OutputBytes, body)
	if err := os.MkdirAll(m.diagnostics.FrameSnapshotDir, 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return
	}
	m.renderDiagnostics.snapshots = append(m.renderDiagnostics.snapshots, RenderFrameSnapshot{Frame: f.Frame, Path: path, OutputBytes: f.OutputBytes})
	if limit := m.diagnostics.FrameSnapshotLimit; limit > 0 && len(m.renderDiagnostics.snapshots) > limit {
		stale := m.renderDiagnostics.snapshots[:len(m.renderDiagnostics.snapshots)-limit]
		for _, snap := range stale {
			_ = os.Remove(snap.Path)
		}
		m.renderDiagnostics.snapshots = append([]RenderFrameSnapshot(nil), m.renderDiagnostics.snapshots[len(m.renderDiagnostics.snapshots)-limit:]...)
	}
}

func (m Model) RenderDiagnosticFrameSnapshots() []RenderFrameSnapshot {
	if !m.diagnostics.FrameSnapshots || m.renderDiagnostics == nil {
		return nil
	}
	return append([]RenderFrameSnapshot(nil), m.renderDiagnostics.snapshots...)
}

func (m *Model) recordRawFrame(f RenderFrameStats, content string) {
	if !m.diagnostics.RawFrameCapture || m.diagnostics.RawFrameCaptureDir == "" || m.renderDiagnostics == nil {
		return
	}
	body := content
	byteLimit := m.diagnostics.RawFrameCaptureByteLimit
	if byteLimit <= 0 {
		byteLimit = defaultRawFrameCaptureByteLimit
	}
	if len(body) > byteLimit {
		body = body[:byteLimit]
	}
	path := filepath.Join(m.diagnostics.RawFrameCaptureDir, fmt.Sprintf("raw-frame-%06d.txt", f.Frame))
	text := fmt.Sprintf("frame: %d\nraw_frame_capture: true\nwarning: raw capture may contain terminal escape sequences and transcript content\nphase: %s\nbusy: %t\nfollow: %t\nterminal: %dx%d\nviewport: %dx%d\nscroll_offset: %d\noutput_bytes: %d\n--- raw content ---\n%s", f.Frame, f.Phase, f.Busy, f.Follow, f.TerminalWidth, f.TerminalHeight, f.ViewportWidth, f.ViewportHeight, f.ScrollOffset, f.OutputBytes, body)
	if err := os.MkdirAll(m.diagnostics.RawFrameCaptureDir, 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return
	}
	m.renderDiagnostics.rawFrames = append(m.renderDiagnostics.rawFrames, RenderRawFrame{Frame: f.Frame, Path: path, OutputBytes: f.OutputBytes})
	frameLimit := m.diagnostics.RawFrameCaptureLimit
	if frameLimit <= 0 {
		frameLimit = defaultRawFrameCaptureLimit
	}
	if len(m.renderDiagnostics.rawFrames) > frameLimit {
		stale := m.renderDiagnostics.rawFrames[:len(m.renderDiagnostics.rawFrames)-frameLimit]
		for _, snap := range stale {
			_ = os.Remove(snap.Path)
		}
		m.renderDiagnostics.rawFrames = append([]RenderRawFrame(nil), m.renderDiagnostics.rawFrames[len(m.renderDiagnostics.rawFrames)-frameLimit:]...)
	}
}

func (m Model) RenderDiagnosticRawFrames() []RenderRawFrame {
	if !m.diagnostics.RawFrameCapture || m.renderDiagnostics == nil {
		return nil
	}
	return append([]RenderRawFrame(nil), m.renderDiagnostics.rawFrames...)
}
