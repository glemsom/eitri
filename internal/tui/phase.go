package tui

import "github.com/glemsom/eitri/internal/tui/pulse"

// The live-turn pulse — including the Phase derived from it — lives in the pulse
// package. These aliases keep the render call sites (rail, composer, glyphs, the
// transcript) reading the same names while the extraction settles.
type Phase = pulse.Phase

const (
	PhaseIdle      = pulse.PhaseIdle
	PhaseReasoning = pulse.PhaseReasoning
	PhaseWorking   = pulse.PhaseWorking
	PhaseAnswering = pulse.PhaseAnswering
)

func phaseVerb(p Phase) string { return pulse.Verb(p) }
