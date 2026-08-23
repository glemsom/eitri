package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// TurnSession owns the live turn: the per-turn cancelable context, the
// thinking-enabled flag used when the turn creates messages, and the stream
// cursor of the in-progress assistant message. Begin arms a new turn end to
// end and Stop cancels the in-flight one, so turn start/stop has one owner.
type TurnSession struct {
	turn            Turn
	ctx             context.Context
	cancel          context.CancelFunc
	thinkingEnabled bool
	curStream       int
}

// NewTurnSession creates a disarmed session for the given turn function.
func NewTurnSession(turn Turn) *TurnSession {
	return &TurnSession{turn: turn, curStream: -1}
}

// Begin arms a fresh cancelable context for a new turn, appends the user
// message to the transcript, marks the transcript busy, resets the live-turn
// state, anchors the tool log to the new prompt, and returns the command that
// dispatches the turn.
func (s *TurnSession) Begin(tx *Transcript, prompt, payload string) tea.Cmd {
	s.ctx, s.cancel = context.WithCancel(context.Background())
	tx.messages = append(tx.messages, message{role: "you", content: prompt})
	tx.busy = true
	s.curStream = -1
	tx.timeline = nil
	tx.turnSeq = 0
	tx.layout.dirty = true
	tx.log.SetAnchor(len(tx.messages) - 1)
	return tea.Cmd(func() tea.Msg {
		ctx := s.Context()
		if ctx == nil {
			// Defensive fallback for a command dispatched before Begin ran.
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
		} else {
			defer s.Stop()
		}
		res, err := s.turn(ctx, prompt, payload)
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

// ThinkingEnabled reports whether new messages this turn creates request thinking.
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

// SetThinkingEnabled updates the thinking flag used for new messages created during the turn.
func (s *TurnSession) SetThinkingEnabled(v bool) { s.thinkingEnabled = v }
