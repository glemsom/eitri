package tui

// TurnDispatch owns the turn state machine: appendStreamDelta and handleTurnDone.
type TurnDispatch struct {
	session *TurnSession
}

// NewTurnDispatch creates a TurnDispatch with the given engine seam.
func NewTurnDispatch(turn Turn) *TurnDispatch {
	return &TurnDispatch{session: NewTurnSession(turn)}
}

// SetThinkingEnabled delegates to the session, which owns the thinking flag.
func (d *TurnDispatch) SetThinkingEnabled(v bool) { d.session.SetThinkingEnabled(v) }

// appendStreamDelta grows the in-progress assistant message by one streamed delta and records the delta on the turn's arrival-ordered event timeline.
func (d *TurnDispatch) appendStreamDelta(tx *Transcript, kind StreamKind, delta string) {
	if delta == "" {
		return
	}
	tx.recordLive(TimelineEvent{Kind: streamEventKind(kind), Delta: delta})
	cur := d.session.curStream
	if cur >= 0 && cur < len(tx.messages) && tx.messages[cur].streaming {
		tx.syncStreamSnapshots(cur)
		return
	}
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: d.session.thinkingEnabled})
	d.session.curStream = len(tx.messages) - 1
	tx.syncStreamSnapshots(d.session.curStream)
	tx.busyPulse = 3
}

// commitTimeline attaches the live per-turn event log to the turn's assistant
// message and resets the live log, so a completed turn owns its arrival-ordered
// record and the next turn starts clean.
func (d *TurnDispatch) commitTimeline(tx *Transcript, i int) {
	if i >= 0 && i < len(tx.messages) {
		tx.messages[i].events = tx.timeline
	}
	tx.timeline = nil
	tx.turnSeq = 0
}

// commitNewAssistant attaches the live event log to the freshly appended
// assistant message (the turn that completed without a streaming message) and
// resets the live log.
func (d *TurnDispatch) commitNewAssistant(tx *Transcript) {
	idx := len(tx.messages) - 1
	if len(tx.timeline) == 0 {
		tx.timeline = synthAnswerLog(tx.messages[idx].content)
	}
	tx.messages[idx].events = tx.timeline
	tx.timeline = nil
	tx.turnSeq = 0
}

// handleTurnDone reconciles the turn completion into the transcript.
func (d *TurnDispatch) handleTurnDone(tx *Transcript, msg turnDoneMsg) (stopped bool, err error) {
	tx.busy = false
	tx.spinner = 0
	d.session.End()
	tx.layout.dirty = true
	wasStreaming := d.session.curStream >= 0 && d.session.curStream < len(tx.messages)

	if msg.stopped {
		tx.layout.dirty = true
		if wasStreaming {
			i := d.session.curStream
			tx.messages[i].content = msg.answer
			tx.messages[i].reasoning = msg.reasoning
			tx.messages[i].streaming = false
			tx.messages[i].stopped = true
			tx.clearReasoningFragments(i)
			d.commitTimeline(tx, i)
			d.session.curStream = -1
		} else if msg.answer != "" || msg.reasoning != "" {
			tx.messages = append(tx.messages, message{role: "eitri", content: msg.answer, reasoning: msg.reasoning, stopped: true, thinkingRequested: d.session.thinkingEnabled})
			d.commitNewAssistant(tx)
		}
		return true, nil
	}
	if msg.err != nil {
		if wasStreaming {
			tx.messages[d.session.curStream].streaming = false
			d.commitTimeline(tx, d.session.curStream)
		}
		d.session.curStream = -1
		tx.messages = append(tx.messages, message{role: "eitri", content: failurePrefix() + msg.err.Error(), thinkingRequested: d.session.thinkingEnabled})
		d.commitNewAssistant(tx)
		return false, msg.err
	}
	if wasStreaming {
		i := d.session.curStream
		tx.messages[i].content = msg.answer
		tx.messages[i].reasoning = msg.reasoning
		tx.messages[i].streaming = false
		tx.clearReasoningFragments(i)
		if !tx.expandAll {
			tx.clearReasoningExpandForce(i)
		}
		d.commitTimeline(tx, i)
		d.session.curStream = -1
	} else {
		tx.messages = append(tx.messages, message{role: "eitri", content: msg.answer, reasoning: msg.reasoning, thinkingRequested: d.session.thinkingEnabled})
		d.commitNewAssistant(tx)
	}
	return false, nil
}
