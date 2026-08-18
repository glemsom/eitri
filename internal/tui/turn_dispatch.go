package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// turnState holds the per-turn mutable state that survives Bubble Tea value
// copies via pointer indirection. It contains only the fields used by turn
// methods: the cancelable context for the running turn and the cursor into the
// streaming assistant message.
type turnState struct {
	turnCtx    context.Context
	turnCancel context.CancelFunc
	curStream  int
}

// TurnDispatch owns the turn state machine: startTurn, stopTurn, turnCmd,
// appendStreamDelta, and handleTurnDone. It is self-contained and testable
// without a terminal or a live provider. Continuation logic (continueReq /
// continueResp / prompting) stays on Model.
type TurnDispatch struct {
	s    *turnState
	turn Turn
}

// NewTurnDispatch creates a TurnDispatch with the given engine seam.
func NewTurnDispatch(turn Turn) *TurnDispatch {
	return &TurnDispatch{
		s:    &turnState{curStream: -1},
		turn: turn,
	}
}

// startTurn installs a cancelable context, appends a user message to the
// transcript, marks the transcript busy, resets the stream cursor, anchors the
// tool log, and returns the command that dispatches the turn.
func (d *TurnDispatch) startTurn(tx *Transcript, prompt, payload string) tea.Cmd {
	d.s.turnCtx, d.s.turnCancel = context.WithCancel(context.Background())
	tx.messages = append(tx.messages, message{role: "you", content: prompt})
	tx.busy = true
	d.s.curStream = -1
	tx.layout.dirty = true
	tx.log.SetAnchor(len(tx.messages) - 1)
	return d.turnCmd(prompt, payload)
}

// stopTurn cancels the in-flight turn's context. It is a no-op when nothing
// is running.
func (d *TurnDispatch) stopTurn() {
	if d.s.turnCancel != nil {
		d.s.turnCancel()
	}
}

// turnCmd returns a command that runs the turn on the cancelable context and
// delivers a turnDoneMsg when complete. The closure runs on the context
// startTurn installed, so pressing esc mid-turn aborts the engine work at the
// context boundary and the turn returns the cancellation as a stopped result
// rather than an error.
func (d *TurnDispatch) turnCmd(prompt, payload string) tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		ctx := d.s.turnCtx
		if ctx == nil {
			// Defensive fallback for a command dispatched before startTurn ran.
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(context.Background())
			defer cancel()
		} else {
			// Release the per-turn context once the turn finishes naturally;
			// esc releases it earlier via the cancel handle.
			defer d.s.turnCancel()
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

// appendStreamDelta grows the in-progress assistant message by one streamed
// delta. On the first delta it appends a new assistant message and records its
// index as the current stream target; subsequent deltas extend that same
// message in place. Reasoning deltas accumulate onto the message's reasoning
// buffer and answer deltas onto its content buffer; the two never interleave.
func (d *TurnDispatch) appendStreamDelta(tx *Transcript, kind StreamKind, delta string) {
	if delta == "" {
		return
	}
	if d.s.curStream >= 0 && d.s.curStream < len(tx.messages) && tx.messages[d.s.curStream].streaming {
		if kind == ReasoningStream {
			tx.messages[d.s.curStream].reasoning += delta
		} else {
			tx.messages[d.s.curStream].content += delta
		}
		return
	}
	if kind == ReasoningStream {
		tx.messages = append(tx.messages, message{role: "eitri", reasoning: delta, streaming: true})
	} else {
		tx.messages = append(tx.messages, message{role: "eitri", content: delta, streaming: true})
	}
	d.s.curStream = len(tx.messages) - 1
}

// handleTurnDone reconciles the turn completion into the transcript. It
// returns whether the turn was stopped and any error. The five branches are:
//   - stopped + streaming: update streaming message, mark stopped
//   - stopped + no streaming: append partial message, mark stopped
//   - error: append error message, return error
//   - success + streaming: reconcile incremental buffer with final answer
//   - success + no streaming: append final answer message
func (d *TurnDispatch) handleTurnDone(tx *Transcript, msg turnDoneMsg) (stopped bool, err error) {
	tx.busy = false
	tx.spinner = 0
	d.s.turnCancel = nil
	d.s.turnCtx = nil
	tx.layout.dirty = true
	wasStreaming := d.s.curStream >= 0 && d.s.curStream < len(tx.messages)

	if msg.stopped {
		// A stopped turn keeps the partial output already on screen, marked
		// as stopped rather than rendered as an error.
		tx.layout.dirty = true
		if wasStreaming {
			tx.messages[d.s.curStream].content = msg.answer
			tx.messages[d.s.curStream].reasoning = msg.reasoning
			tx.messages[d.s.curStream].streaming = false
			tx.messages[d.s.curStream].stopped = true
			d.s.curStream = -1
		} else if msg.answer != "" || msg.reasoning != "" {
			tx.messages = append(tx.messages, message{role: "eitri", content: msg.answer, reasoning: msg.reasoning, stopped: true})
		}
		return true, nil
	}
	if msg.err != nil {
		// A streaming turn aborting with an error drops the partial reply and
		// renders the error in its place.
		d.s.curStream = -1
		tx.messages = append(tx.messages, message{role: "eitri", content: failurePrefix() + msg.err.Error()})
		return false, msg.err
	}
	if wasStreaming {
		// Streaming turn: reconcile the incremental buffer with the full
		// answer. When every delta already arrived the contents match, so
		// this is a no-op visual diff.
		tx.messages[d.s.curStream].content = msg.answer
		tx.messages[d.s.curStream].reasoning = msg.reasoning
		tx.messages[d.s.curStream].streaming = false
		if !tx.expandAll {
			tx.messages[d.s.curStream].thinkingExpanded = false
		}
		d.s.curStream = -1
	} else {
		tx.messages = append(tx.messages, message{role: "eitri", content: msg.answer, reasoning: msg.reasoning})
	}
	return false, nil
}
