package pulse

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

func (p *Pulse) Start() {
	if p == nil {
		return
	}
	p.busy = true
	p.busyStartedAt = p.clock()
}

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

// SettleIdleEmber closes the idle-ember window so no further wakeup is scheduled:
// a busy turn, an overlay, or reduced motion must stop the idle animation rather
// than let a stray tick keep it alive.
func (p *Pulse) SettleIdleEmber() {
	if p == nil {
		return
	}
	p.setIdleEmberFrame(0)
	p.idleEmberRemaining = 0
}

func (p *Pulse) armIdleEmber() {
	p.setIdleEmberFrame(0)
	p.idleEmberRemaining = EmberWindow
}

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

func (p *Pulse) setIdleEmberFrame(frame int) {
	if p.idleEmberFrame == frame {
		return
	}
	p.idleEmberFrame = frame
}

func (p *Pulse) SetEmberFrame(frame int) {
	if p == nil {
		return
	}
	p.setIdleEmberFrame(frame)
}

func (p *Pulse) SetClock(clock func() time.Time) {
	if p == nil {
		return
	}
	if clock == nil {
		clock = time.Now
	}
	p.clock = clock
}

func (p *Pulse) SetTick(tick TickFunc) {
	if p == nil {
		return
	}
	if tick == nil {
		tick = RealTick
	}
	p.tick = tick
}
