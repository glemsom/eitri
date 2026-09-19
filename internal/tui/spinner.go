package tui

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Busy spinner: the animated braille indicator that runs while a turn works,
// plus the motion gate that decides when animation is allowed. The default is an
// OpenCode-style braille frame set advanced every busySpinnerTick; the static
// "… thinking" line (render.go's busyLine) is the reduced-motion fallback.
// The gate disables all animation when the user opts out (EITRI_NO_MOTION).
const busySpinnerTick = 80 * time.Millisecond

// busySpinnerFrames is the OpenCode-style braille frame set.
var busySpinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

type spinnerTickMsg struct{}

func spinnerTick() tea.Cmd {
	return tea.Tick(busySpinnerTick, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// motionEnabled reports whether animated indicators may run.
func motionEnabled() bool {
	return os.Getenv("EITRI_NO_MOTION") == ""
}

// Idle ember: a slow shimmer that crosses the idle brand for a bounded window
// after activity. It is deliberately a different timer than the busy spinner
// tick, so a run and an idle surface never arm the same timer state, and the
// window is bounded so idle motion never holds a permanent wakeup.
const idleEmberTickInterval = 140 * time.Millisecond

// idleEmberWindow is how many ember ticks run after the last activity before
// the surface settles to the static brand mark.
const idleEmberWindow = 24

type idleEmberTickMsg struct{}

func idleEmberTick() tea.Cmd {
	return tea.Tick(idleEmberTickInterval, func(time.Time) tea.Msg { return idleEmberTickMsg{} })
}
