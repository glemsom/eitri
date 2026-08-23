package tui

import "context"

// TurnSession owns the live-turn resources that outlive a single dispatch call:
// the per-turn cancelable context and the thinking-enabled flag used when the
// turn creates messages. Start/stop paths go through it so cancellation state
// has one owner.
type TurnSession struct {
	ctx             context.Context
	cancel          context.CancelFunc
	thinkingEnabled bool
}

// NewTurnSession creates a disarmed session.
func NewTurnSession() *TurnSession { return &TurnSession{} }

// Begin arms a fresh cancelable context for a new turn.
func (s *TurnSession) Begin() {
	s.ctx, s.cancel = context.WithCancel(context.Background())
}

// Context returns the armed per-turn context, or nil if no turn is armed.
func (s *TurnSession) Context() context.Context { return s.ctx }

// Stop cancels the armed turn's context, if any.
func (s *TurnSession) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// End disarms the session after a completed turn so a later Stop cannot touch
// the finished turn's context.
func (s *TurnSession) End() {
	s.cancel = nil
	s.ctx = nil
}

// SetThinkingEnabled updates the thinking flag used for new messages created during the turn.
func (s *TurnSession) SetThinkingEnabled(v bool) { s.thinkingEnabled = v }
