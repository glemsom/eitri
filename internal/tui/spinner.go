package tui

import "github.com/glemsom/eitri/internal/tui/pulse"

// The busy spinner and idle ember state and cadence live in the pulse package;
// these aliases keep the render call sites and tests reading the same names.
var busySpinnerFrames = pulse.SpinnerFrames

func motionEnabled() bool { return pulse.MotionEnabled() }

// idleEmberWindow is re-exported so the ember contract tests read the window
// through the same name the pulse enforces it with.
const idleEmberWindow = pulse.EmberWindow
