package tui

import (
	"context"
	"errors"
	"time"

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
	// flow owns the turn's streamed reasoning/answer observations, the tool
	// start/result observations, the live snapshots derived from the
	// streamed text, and the single arrival-ordered event log all of it
	// shares.
	flow TurnFlow
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
	tx.live = s
	tx.appendUserMsg(prompt)
	tx.busy = true
	tx.busyStartedAt = time.Now()
	s.curStream = -1
	s.flow.Reset()
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

// LiveTimeline exposes the in-progress turn's arrival-ordered event log for
// read-only rendering; TurnFlow stays its sole writer, for stream and tool
// observations alike.
func (s *TurnSession) LiveTimeline() []TimelineEvent {
	return s.flow.Events()
}

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

// Commit reconciles one turn completion — success, error, or stopped — into
// the transcript: the live event log attaches to the turn's assistant message
// (streamed) or to a freshly appended one, reasoning fragment cleanup and
// expand-force clearing run outside expand-all, and busy/stream state resets.
// Timeline-commit vs fresh-assistant bookkeeping stays internal so callers
// see a single completion verb.
func (s *TurnSession) Commit(tx *Transcript, msg turnDoneMsg) (stopped bool, err error) {
	turnElapsed := time.Duration(0)
	if !tx.busyStartedAt.IsZero() {
		turnElapsed = time.Since(tx.busyStartedAt)
	}
	tx.endTurn()
	s.End()
	wasStreaming := s.curStream >= 0 && s.curStream < len(tx.messages)
	content, reasoning := s.flow.Finalize(msg.answer, msg.reasoning, msg.stopped)

	if msg.stopped {
		if wasStreaming {
			i := s.curStream
			tx.messages[i].content = content
			tx.messages[i].reasoning = reasoning
			tx.messages[i].streaming = false
			tx.messages[i].stopped = true
			tx.clearReasoningFragments(i)
			s.commitTimeline(tx, i)
			s.curStream = -1
		} else if content != "" || reasoning != "" {
			tx.messages = append(tx.messages, message{role: "eitri", content: content, reasoning: reasoning, stopped: true, thinkingRequested: s.thinkingEnabled})
			s.commitNewAssistant(tx)
		}
		tx.applyTurnElapsed(turnElapsed)
		return true, nil
	}
	if msg.err != nil {
		if wasStreaming {
			i := s.curStream
			tx.messages[i].streaming = false
			s.commitTimeline(tx, i)
		}
		s.curStream = -1
		tx.messages = append(tx.messages, message{role: "eitri", content: failurePrefix() + msg.err.Error(), thinkingRequested: s.thinkingEnabled})
		s.commitNewAssistant(tx)
		tx.applyTurnElapsed(turnElapsed)
		return false, msg.err
	}
	if wasStreaming {
		i := s.curStream
		tx.messages[i].content = content
		tx.messages[i].reasoning = reasoning
		tx.messages[i].streaming = false
		tx.clearReasoningFragments(i)
		if !tx.expandAll {
			tx.clearReasoningExpandForce(i)
		}
		s.commitTimeline(tx, i)
		s.curStream = -1
	} else {
		tx.messages = append(tx.messages, message{role: "eitri", content: content, reasoning: reasoning, thinkingRequested: s.thinkingEnabled})
		s.commitNewAssistant(tx)
	}
	tx.applyTurnElapsed(turnElapsed)
	return false, nil
}

// commitTimeline attaches the live per-turn event log to the turn's assistant
// message and resets the live log, so a completed turn owns its arrival-ordered
// record and the next turn starts clean.
func (s *TurnSession) commitTimeline(tx *Transcript, i int) {
	if i >= 0 && i < len(tx.messages) {
		tx.messages[i].events = s.LiveTimeline()
	}
	s.flow.Reset()
}

// commitNewAssistant attaches the live event log to the freshly appended
// assistant message (the turn that completed without a streaming message) and
// resets the live log.
func (s *TurnSession) commitNewAssistant(tx *Transcript) {
	idx := len(tx.messages) - 1
	events := s.LiveTimeline()
	if len(events) == 0 {
		events = synthAnswerLog(tx.messages[idx].content)
	}
	tx.messages[idx].events = events
	s.flow.Reset()
}
