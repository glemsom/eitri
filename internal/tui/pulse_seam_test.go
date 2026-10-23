package tui

import "github.com/glemsom/eitri/internal/tui/pulse"

// runPulse builds a pulse backing a white-box Transcript literal: a bare
// transcript owns no pulse, so tests that need a busy readout supply one here.
func runPulse(busy bool) *pulse.Pulse {
	p := pulse.New(nil)
	if busy {
		p.BeginBusy()
	}
	return p
}

func busyPulse() *pulse.Pulse { return runPulse(true) }
