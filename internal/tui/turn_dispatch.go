package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// TurnDispatch owns the turn state machine: startTurn, stopTurn, turnCmd, appendStreamDelta, and handleTurnDone.
type TurnDispatch struct {
	turn            Turn
	thinkingEnabled bool
	turnCtx         context.Context
	turnCancel      context.CancelFunc
	curStream       int
}

// NewTurnDispatch creates a TurnDispatch with the given engine seam.
func NewTurnDispatch(turn Turn) *TurnDispatch {
	return &TurnDispatch{
		turn:      turn,
		curStream: -1,
	}
}

// startTurn installs a cancelable context, appends a user message to the transcript, marks the transcript busy, resets the stream cursor, anchors the tool log, and returns the command that dispatches the turn.
func (d *TurnDispatch) startTurn(tx *Transcript, prompt, payload string) tea.Cmd {
	d.turnCtx, d.turnCancel = context.WithCancel(context.Background())
	tx.messages = append(tx.messages, message{role: "you", content: prompt})
	tx.busy = true
	d.curStream = -1
	tx.layout.dirty = true
	tx.log.SetAnchor(len(tx.messages) - 1)
	return d.turnCmd(prompt, payload)
}

// SetThinkingEnabled updates the thinking flag for new messages created by appendStreamDelta and handleTurnDone.
func (d *TurnDispatch) SetThinkingEnabled(v bool) { d.thinkingEnabled = v }

// stopTurn cancels the in-flight turn's context.
func (d *TurnDispatch) stopTurn() {
	if d.turnCancel != nil {
		d.turnCancel()
	}
}

// turnCmd returns a command that runs the turn on the cancelable context and delivers a turnDoneMsg when complete.
func (d *TurnDispatch) turnCmd(prompt, payload string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := d.turnCtx
		if ctx == nil {
			// Defensive fallback for a command dispatched before startTurn ran.
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
		} else {
			defer d.turnCancel()
		}
		res, err := d.turn(ctx, prompt, payload)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return turnDoneMsg{prompt: prompt, stopped: true, answer: res.Answer, reasoning: res.Reasoning}
			}
			return turnDoneMsg{prompt: prompt, err: err}
		}
		return turnDoneMsg{prompt: prompt, answer: res.Answer, reasoning: res.Reasoning, stopped: res.Stopped}
	})
}

// appendStreamDelta grows the in-progress assistant message by one streamed delta.
func (d *TurnDispatch) appendStreamDelta(tx *Transcript, kind StreamKind, delta string) {
	if delta == "" {
		return
	}
	if d.curStream >= 0 && d.curStream < len(tx.messages) && tx.messages[d.curStream].streaming {
		if kind == ReasoningStream {
			tx.messages[d.curStream].reasoning += delta
		} else {
			tx.messages[d.curStream].content += delta
		}
		return
	}
	if kind == ReasoningStream {
		tx.messages = append(tx.messages, message{role: "eitri", reasoning: delta, streaming: true, thinkingRequested: d.thinkingEnabled})
	} else {
		tx.messages = append(tx.messages, message{role: "eitri", content: delta, streaming: true, thinkingRequested: d.thinkingEnabled})
	}
	d.curStream = len(tx.messages) - 1
	tx.busyPulse = 3
}

// handleTurnDone reconciles the turn completion into the transcript.
func (d *TurnDispatch) handleTurnDone(tx *Transcript, msg turnDoneMsg) (stopped bool, err error) {
	tx.busy = false
	tx.spinner = 0
	d.turnCancel = nil
	d.turnCtx = nil
	tx.layout.dirty = true
	wasStreaming := d.curStream >= 0 && d.curStream < len(tx.messages)

	if msg.stopped {
		tx.layout.dirty = true
		if wasStreaming {
			tx.messages[d.curStream].content = msg.answer
			tx.messages[d.curStream].reasoning = msg.reasoning
			tx.messages[d.curStream].streaming = false
			tx.messages[d.curStream].stopped = true
			d.curStream = -1
		} else if msg.answer != "" || msg.reasoning != "" {
			tx.messages = append(tx.messages, message{role: "eitri", content: msg.answer, reasoning: msg.reasoning, stopped: true, thinkingRequested: d.thinkingEnabled})
		}
		return true, nil
	}
	if msg.err != nil {
		if wasStreaming {
			tx.messages[d.curStream].streaming = false
		}
		d.curStream = -1
		tx.messages = append(tx.messages, message{role: "eitri", content: failurePrefix() + msg.err.Error(), thinkingRequested: d.thinkingEnabled})
		return false, msg.err
	}
	if wasStreaming {
		tx.messages[d.curStream].content = msg.answer
		tx.messages[d.curStream].reasoning = msg.reasoning
		tx.messages[d.curStream].streaming = false
		if !tx.expandAll {
			tx.messages[d.curStream].thinkingExpanded = false
		}
		d.curStream = -1
	} else {
		tx.messages = append(tx.messages, message{role: "eitri", content: msg.answer, reasoning: msg.reasoning, thinkingRequested: d.thinkingEnabled})
	}
	return false, nil
}
