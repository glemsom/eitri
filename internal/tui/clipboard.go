package tui

import (
	"os"

	"github.com/glemsom/eitri/internal/osc52"
)

// newClipboard returns the explicitly supplied clipboard adapter, or an OSC 52 adapter when none is supplied. Eitri never shells out to a host clipboard tool such as xclip or wl-clipboard.
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
