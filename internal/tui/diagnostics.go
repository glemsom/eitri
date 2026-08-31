package tui

// DiagnosticsConfig is the startup/test-construction seam for opt-in TUI
// diagnostics. The zero value keeps diagnostics disabled and preserves normal
// rendering.
type DiagnosticsConfig struct {
	RenderStats     bool
	FrameSnapshots  bool
	RawFrameCapture bool
	Pprof           bool
}
