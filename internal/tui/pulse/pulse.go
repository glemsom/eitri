package pulse

// Package pulse owns the live turn's animation pulse: the busy flag and its
// elapsed clock, the braille spinner and forge frames, the tool-start glint,
// the idle ember, and the Phase derived from the streaming assistant message.
import (
	"time"

	tea "charm.land/bubbletea/v2"
)

type Pulse struct {
	clock              func() time.Time
	tick               TickFunc
	busy               bool
	busyStartedAt      time.Time
	spinner            int
	forgeFrame         int
	busyPulse          int
	idleEmberFrame     int
	idleEmberRemaining int
	streaming          bool
	hasReasoning       bool
	hasAnswer          bool
}

// New builds a pulse reading wall-clock time and ticking on real timers; a nil
// clock uses time.Now.
func New(clock func() time.Time) *Pulse {
	if clock == nil {
		clock = time.Now
	}
	return &Pulse{clock: clock, tick: RealTick}
}

// Busy reports whether a turn is running. A nil pulse is never busy, so a bare
// Transcript's zero value reads as idle.
func (p *Pulse) Busy() bool {
	if p == nil {
		return false
	}
	return p.busy
}

// BeginBusy marks a turn busy without starting the elapsed clock: the
// skill-activation window shows the busy panel before the run starts the clock.
func (p *Pulse) BeginBusy() {
	if p == nil {
		return
	}
	p.busy = true
}

// Start marks a turn busy and starts its elapsed clock.
func (p *Pulse) Start() {
	if p == nil {
		return
	}
	p.busy = true
	p.busyStartedAt = p.clock()
}

// EndTurn clears the busy state and parks the animation frames.
func (p *Pulse) EndTurn() {
	if p == nil {
		return
	}
	p.busy = false
	p.spinner = 0
	p.forgeFrame = 0
}

// Stop clears the busy flag alone, leaving the frames untouched.
func (p *Pulse) Stop() {
	if p == nil {
		return
	}
	p.busy = false
}

// NoteStream records which assistant text is streaming, so Status derives Phase
// from it alongside the busy flag.
func (p *Pulse) NoteStream(streaming, hasReasoning, hasAnswer bool) {
	if p == nil {
		return
	}
	p.streaming = streaming
	p.hasReasoning = hasReasoning
	p.hasAnswer = hasAnswer
}

// Arm restarts the tool-start glint on the busy band.
func (p *Pulse) Arm() {
	if p == nil {
		return
	}
	p.busyPulse = 3
}

// Status reads the whole pulse as one value.
func (p *Pulse) Status() Status {
	if p == nil {
		return Status{Phase: PhaseIdle}
	}
	var elapsed time.Duration
	if !p.busyStartedAt.IsZero() {
		elapsed = p.clock().Sub(p.busyStartedAt)
	}
	return Status{
		Busy:           p.busy,
		Phase:          derivePhase(p.busy, p.streaming, p.hasReasoning, p.hasAnswer),
		Elapsed:        elapsed,
		Pulse:          p.busyPulse,
		Frame:          p.spinner,
		ForgeFrame:     p.forgeFrame,
		EmberFrame:     p.idleEmberFrame,
		EmberRemaining: p.idleEmberRemaining,
	}
}

// TickBusy advances the busy animation one 80 ms cadence and reports the next
// tick; it parks the frames and stops the tick once the turn ends or motion is
// off.
func (p *Pulse) TickBusy() tea.Cmd {
	if p == nil || !p.busy || !MotionEnabled() {
		if p != nil {
			p.spinner = 0
			p.forgeFrame = 0
		}
		return nil
	}
	p.spinner = (p.spinner + 1) % len(SpinnerFrames)
	p.forgeFrame++
	if p.busyPulse > 0 {
		p.busyPulse--
	}
	return p.SpinnerTick()
}

// ArmEmber restarts the idle-ember window at its leading frame, extending the
// bounded shimmer on activity. A busy surface, an open overlay, or reduced
// motion never arms, and an already-running ember only has its window extended
// so no second timer starts.
func (p *Pulse) ArmEmber(overlayOpen bool) tea.Cmd {
	if p == nil || p.busy || !MotionEnabled() || overlayOpen {
		return nil
	}
	running := p.idleEmberRemaining > 0
	p.armIdleEmber()
	if running {
		return nil
	}
	return p.idleEmberTick()
}

// TickEmber advances the idle ember one 140 ms cadence and reports the next
// tick, settling the ember and stopping the tick when the window closes or a
// busy surface, an open overlay, or reduced motion pauses it.
func (p *Pulse) TickEmber(overlayOpen bool) tea.Cmd {
	if p == nil {
		return nil
	}
	if !p.busy && !overlayOpen && MotionEnabled() && p.advanceIdleEmber() {
		return p.idleEmberTick()
	}
	p.SettleIdleEmber()
	return nil
}

// SettleIdleEmber parks the ember on the static mark and closes the window, so
// a busy turn, an overlay, or reduced motion stops the idle wakeup.
func (p *Pulse) SettleIdleEmber() {
	if p == nil {
		return
	}
	p.setIdleEmberFrame(0)
	p.idleEmberRemaining = 0
}

// armIdleEmber restarts the idle-ember window at its leading frame.
func (p *Pulse) armIdleEmber() {
	p.setIdleEmberFrame(0)
	p.idleEmberRemaining = EmberWindow
}

// advanceIdleEmber steps the ember within the open window and reports whether
// the window is still open, settling the frame back to the static mark when it
// closes.
func (p *Pulse) advanceIdleEmber() bool {
	if p.idleEmberRemaining <= 0 {
		p.setIdleEmberFrame(0)
		return false
	}
	p.setIdleEmberFrame(p.idleEmberFrame + 1)
	p.idleEmberRemaining--
	if p.idleEmberRemaining <= 0 {
		p.setIdleEmberFrame(0)
		return false
	}
	return true
}

// setIdleEmberFrame stores the ember frame when it actually changes.
func (p *Pulse) setIdleEmberFrame(frame int) {
	if p.idleEmberFrame == frame {
		return
	}
	p.idleEmberFrame = frame
}

// SetEmberFrame places the idle ember on a given frame, a seam for tests and
// renderers that need a specific shimmer position.
func (p *Pulse) SetEmberFrame(frame int) {
	if p == nil {
		return
	}
	p.setIdleEmberFrame(frame)
}

// SetClock swaps the clock read, a seam for tests that assert the elapsed readout.
func (p *Pulse) SetClock(clock func() time.Time) {
	if p == nil {
		return
	}
	if clock == nil {
		clock = time.Now
	}
	p.clock = clock
}

// SetTick swaps the timer schedule the spinner and idle ember arm, a seam for
// tests that advance the ticks deterministically; a nil schedule restores the
// real timers.
func (p *Pulse) SetTick(tick TickFunc) {
	if p == nil {
		return
	}
	if tick == nil {
		tick = RealTick
	}
	p.tick = tick
}
