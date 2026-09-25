package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// TurnSession owns cancellable execution of one provider call. TurnRuntime owns
// Run lifecycle, dispatch, cancellation, and transcript projection.
type TurnSession struct {
	turn            func(context.Context, string, string) (TurnResult, error)
	ctx             context.Context
	cancel          context.CancelFunc
	thinkingEnabled bool
}

// NewTurnSession creates a disarmed session for the given turn function.
func NewTurnSession(turn func(context.Context, string, string) (TurnResult, error)) *TurnSession {
	return &TurnSession{turn: turn}
}

// Dispatch executes a provider call using a fresh cancellable context.
func (s *TurnSession) Dispatch(prompt, payload string) tea.Cmd {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return tea.Cmd(func() tea.Msg {
		defer s.Stop()
		res, err := s.turn(s.ctx, prompt, payload)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return turnDoneMsg{prompt: prompt, stopped: true, answer: res.Answer, reasoning: res.Reasoning}
			}
			return turnDoneMsg{prompt: prompt, err: err}
		}
		return turnDoneMsg{prompt: prompt, answer: res.Answer, reasoning: res.Reasoning, stopped: res.Stopped}
	})
}

// Context returns the armed per-turn context, or nil if no turn is armed.
func (s *TurnSession) Context() context.Context { return s.ctx }

func (s *TurnSession) ThinkingEnabled() bool { return s.thinkingEnabled }

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

func (s *TurnSession) SetThinkingEnabled(v bool) { s.thinkingEnabled = v }
