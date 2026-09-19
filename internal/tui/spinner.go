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
