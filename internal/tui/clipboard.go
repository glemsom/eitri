package tui

import (
	"os"

	"github.com/glemsom/eitri/internal/osc52"
)

// newClipboard falls back to OSC 52 rather than shelling out to a host clipboard
// tool such as xclip or wl-clipboard, which the sandboxed environment lacks.
func newClipboard(d Dependencies) func(text string) error {
	if d.Clipboard != nil {
		return d.Clipboard
	}
	out := d.OSC52Out
	if out == nil {
		out = os.Stdout
	}
	return osc52.New(out).Write
}
