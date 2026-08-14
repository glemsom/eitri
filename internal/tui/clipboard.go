package tui

import (
	"io"

	"github.com/glemsom/eitri/internal/osc52"
)

// clipboardWithOSCFallback wraps a primary clipboard seam in the OSC 52
// fallback (issue #201): when the primary path fails — e.g. the atotto/clipboard
// package reporting "Unsupported platform" on a machine without xclip or
// wl-clipboard — the copy re-routes through the OSC 52 terminal-clipboard
// writer (issue #200) to the terminal output, which Ghostty and other OSC 52
// terminals turn into a system-clipboard write. The OSC 52 writer's own guard
// refuses non-terminal outputs (osc52.ErrNotTerminal) without emitting
// anything, so piped output never receives escape garbage. When both paths
// fail, the fallback error is surfaced so the existing "copy failed: …" status
// note still reports the failure.
func clipboardWithOSCFallback(primary func(text string) error, out io.Writer) func(text string) error {
	return func(text string) error {
		if err := primary(text); err == nil {
			return nil
		}
		return osc52.New(out).Write(text)
	}
}
