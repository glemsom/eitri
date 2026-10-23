package tui

import "github.com/glemsom/eitri/internal/tui/pulse"

// The busy spinner and idle ember state and cadence live in the pulse package;
// these aliases keep the render call sites and tests reading the same names.
var busySpinnerFrames = pulse.SpinnerFrames

func motionEnabled() bool { return pulse.MotionEnabled() }

const idleEmberWindow = pulse.EmberWindow
