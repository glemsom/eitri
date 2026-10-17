package pulse

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Busy spinner: the animated braille indicator that runs while a turn works,
// plus the motion gate that decides when animation is allowed. The default is an
// OpenCode-style braille frame set advanced every SpinnerTickInterval; the static
// "… thinking" line (render.go's busyLine) is the reduced-motion fallback.
// The gate disables all animation when the user opts out (EITRI_NO_MOTION).
const SpinnerTickInterval = 80 * time.Millisecond

// SpinnerFrames is the OpenCode-style braille frame set.
var SpinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// SpinnerTickMsg advances the busy spinner one frame.
type SpinnerTickMsg struct{}

// TickFunc schedules a timer message to arrive after d elapses, returning the
// command that delivers it. Production uses RealTick (a real tea.Tick); tests
// inject an immediate schedule so a submitted turn never waits on wall-clock
// time.
type TickFunc func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd

// RealTick is the production timer schedule: a real bubbletea tick.
func RealTick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	return tea.Tick(d, fn)
}

// SpinnerTick reports the next busy-spinner tick on this pulse's schedule.
func (p *Pulse) SpinnerTick() tea.Cmd {
	return p.tick(SpinnerTickInterval, func(time.Time) tea.Msg { return SpinnerTickMsg{} })
}

// MotionEnabled reports whether animated indicators may run.
func MotionEnabled() bool {
	return os.Getenv("EITRI_NO_MOTION") == ""
}

// Idle ember: a slow shimmer that crosses the idle brand for a bounded window
// after activity. It is deliberately a different timer than the busy spinner
// tick, so a run and an idle surface never arm the same timer state, and the
// window is bounded so idle motion never holds a permanent wakeup.
const EmberTickInterval = 140 * time.Millisecond

// EmberWindow is how many ember ticks run after the last activity before the
// surface settles to the static brand mark.
const EmberWindow = 24

// IdleEmberTickMsg advances the idle ember one frame.
type IdleEmberTickMsg struct{}

// idleEmberTick reports the next idle-ember tick on this pulse's schedule.
func (p *Pulse) idleEmberTick() tea.Cmd {
	return p.tick(EmberTickInterval, func(time.Time) tea.Msg { return IdleEmberTickMsg{} })
}
