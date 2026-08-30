package tui

// Fold routes stream deltas and tool observations into the transcript in
// arrival order without owning their state itself: streamed text lives in
// TurnSession's TurnFlow, tool events live in the session's tool log, and
// Fold only stitches transcript-visible effects (message growth, tool-log
// entries, layout invalidation) onto those two owners.
type Fold struct {
	session *TurnSession
}

// NewFold creates a Fold bound to the given session, reading the thinking
// flag for messages it appends and sharing the stream cursor state.
func NewFold(session *TurnSession) *Fold {
	return &Fold{session: session}
}

// Stream grows the in-progress assistant message by one streamed delta,
// records it on the turn's arrival-ordered event timeline, and copies the
// TurnFlow-derived live snapshots onto the streaming message.
func (f *Fold) Stream(tx *Transcript, kind StreamKind, delta string) {
	if !f.session.flow.Observe(kind, delta) {
		return
	}
	f.session.recordStream()
	cur := f.session.curStream
	if cur >= 0 && cur < len(tx.messages) && tx.messages[cur].streaming {
		tx.syncStreamSnapshots(cur, f.session.flow.Content(), f.session.flow.Reasoning())
		return
	}
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: f.session.thinkingEnabled})
	f.session.curStream = len(tx.messages) - 1
	tx.syncStreamSnapshots(f.session.curStream, f.session.flow.Content(), f.session.flow.Reasoning())
	tx.busyPulse = 3
}

// Tool routes one tool observation into both the tool log and the event
// timeline: the log entry lands where renderPane reads it, and the matching
// event joins the arrival-ordered record alongside the stream deltas. While
// the turn runs the event goes to the live log; after completion it attaches
// to the most recent assistant message so trailing results stay in that
// turn's log.
func (f *Fold) Tool(tx *Transcript, u ToolUpdate) {
	tx.applyTool(u)
	if kind, ok := toolEventKind(u); ok {
		ev := TimelineEvent{Kind: kind, Start: u.Start, Result: u.Result}
		if tx.busy {
			f.session.recordLive(ev)
		} else {
			f.attachToLastAssistant(tx, ev)
		}
	}
}

// attachToLastAssistant appends an event to the newest committed assistant
// message's event log, continuing the sequence wherever that log left off so
// post-turn appends stay arrival-ordered with the turn's own events.
func (f *Fold) attachToLastAssistant(tx *Transcript, ev TimelineEvent) {
	for i := len(tx.messages) - 1; i >= 0; i-- {
		if tx.messages[i].role == "eitri" {
			ev.Seq = len(tx.messages[i].events)
			tx.messages[i].events = append(tx.messages[i].events, ev)
			return
		}
	}
}
