package tui

// Fold is the sole writer of the streaming assistant message, the live
// per-turn event log, and its sequence counter: stream deltas and tool
// observations from a running turn land in arrival order exactly as before,
// but no code outside the session folds those events directly.
type Fold struct {
	session *TurnSession
}

// NewFold creates a Fold bound to the given session, reading the thinking
// flag for messages it appends and sharing the stream cursor state.
func NewFold(session *TurnSession) *Fold {
	return &Fold{session: session}
}

// Stream grows the in-progress assistant message by one streamed delta and
// records the delta on the turn's arrival-ordered event timeline; the derived
// content/reasoning snapshots re-sync from the timeline afterwards.
func (f *Fold) Stream(tx *Transcript, kind StreamKind, delta string) {
	if delta == "" {
		return
	}
	f.recordLive(tx, TimelineEvent{Kind: streamEventKind(kind), Delta: delta})
	cur := f.session.curStream
	if cur >= 0 && cur < len(tx.messages) && tx.messages[cur].streaming {
		tx.syncStreamSnapshots(cur, f.session.timeline)
		return
	}
	tx.messages = append(tx.messages, message{role: "eitri", streaming: true, thinkingRequested: f.session.thinkingEnabled})
	f.session.curStream = len(tx.messages) - 1
	tx.syncStreamSnapshots(f.session.curStream, f.session.timeline)
	tx.busyPulse = 3
}

// Tool routes one tool observation into both the tool log and the event
// timeline: the log entry lands where renderPane reads it, and the matching
// event joins the arrival-ordered record alongside the stream deltas. While
// the turn runs the event goes to the live log; after completion it attaches
// to the most recent assistant message so trailing results stay in that
// turn's log.
func (f *Fold) Tool(tx *Transcript, u ToolUpdate) {
	tx.log.Apply(u)
	if kind, ok := toolEventKind(u); ok {
		ev := TimelineEvent{Kind: kind, Start: u.Start, Result: u.Result}
		if tx.busy {
			f.recordLive(tx, ev)
		} else {
			f.attachToLastAssistant(tx, ev)
		}
	}
	tx.layout.dirty = true // an entry changed the tool log's rendered rows
}

// recordLive appends one event to the live per-turn log in arrival order,
// stamping it with the turn's next sequence number.
func (f *Fold) recordLive(tx *Transcript, ev TimelineEvent) {
	ev.Seq = f.session.turnSeq
	f.session.turnSeq++
	f.session.timeline = append(f.session.timeline, ev)
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
