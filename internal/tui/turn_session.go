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

// Begin arms a fresh cancelable context and submits the prompt through it,
// leaving the transcript side of turn start (user message, busy flag, stream
// cursor, tool-log anchor) consistent before any provider work runs. The
// returned command disarms the session when the turn completes, so a later
// Stop cannot touch a finished turn's context.
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
