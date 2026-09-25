package tui

import tea "charm.land/bubbletea/v2"

// TurnRuntime is the sole TUI-facing owner of one live Run: lifecycle, event
// acceptance, event projection, and completion. TurnSession only executes
// the cancellable provider call behind this seam.
type TurnRuntime struct {
	session    *TurnSession
	events     *EventFeed
	transcript *Transcript
	liveRunID  int
}

// NewTurnRuntime builds a runtime bound to the given turn session and live
// merged event feed (nil when no engine event stream is wired). The transcript
// projection helper is internal to the runtime seam.
func NewTurnRuntime(session *TurnSession, events *EventFeed) *TurnRuntime {
	return &TurnRuntime{session: session, events: events, liveRunID: -1}
}

// SetTranscript binds the live transcript context owned by this runtime.
// Callers configure the runtime once; lifecycle and projection operations then
// use the bound transcript rather than coordinating projection state.
func (rt *TurnRuntime) SetTranscript(tx *Transcript) { rt.transcript = tx }

// HasEvents reports whether a live merged event feed is wired.
func (rt *TurnRuntime) HasEvents() bool { return rt.events != nil }

// SetSession replaces the provider session while retaining the runtime seam
// and its event feed.
func (rt *TurnRuntime) SetSession(session *TurnSession) {
	rt.session = session
	rt.liveRunID = -1
}

// Begin arms a fresh run ID, drains any stale events left over from a prior
// turn, and starts the session's turn; when a live event feed is wired the
// returned command also starts the spinner so the busy indicator animates.
func (rt *TurnRuntime) Begin(prompt, payload string) tea.Cmd {
	tx := rt.transcript
	rt.liveRunID = -1
	if rt.events != nil {
		rt.events.Drain()
	}
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: prompt, ThinkingEnabled: rt.session.ThinkingEnabled()}})
	cmd := rt.session.Dispatch(prompt, payload)
	if rt.events != nil {
		return tea.Batch(cmd, spinnerTick())
	}
	return cmd
}

// OnTurnStart records the first engine-reported run ID for a turn; only
// events matching it are accepted until the next Begin. A later mismatched
// start belongs to a stale run and cannot replace the active run ID.
func (rt *TurnRuntime) OnTurnStart(runID int) {
	if runID != 0 && (rt.liveRunID == -1 || rt.liveRunID == runID) {
		rt.liveRunID = runID
	}
}

// Accept reports whether a live event belongs to the current run: direct
// events with RunID == 0 (tests and package-local callers) are always
// accepted, and engine-sourced events must match the current run ID.
func (rt *TurnRuntime) Accept(u Event) bool {
	if u.RunID == 0 {
		return true // tests and package-local callers can deliver direct events.
	}
	return rt.liveRunID == u.RunID
}

// Wait returns the command that blocks for the next merged event, or nil
// when no event feed is wired.
func (rt *TurnRuntime) Wait() tea.Cmd {
	if rt.events == nil {
		return nil
	}
	return eventWait(rt.events)
}

// Handle accepts one feed event and schedules the next one. Run-ID policy,
// turn-start handling, observation, and non-blocking backlog batching all live
// here so callers only need to forward feed messages to the runtime.
func (rt *TurnRuntime) Handle(u Event) tea.Cmd {
	if u.TurnStart {
		rt.OnTurnStart(u.RunID)
		return rt.Wait()
	}
	if rt.Accept(u) {
		rt.project(u)
	}
	rt.drainReady()
	return rt.Wait()
}

// Commit reconciles one turn completion into the transcript for the live Run.
func (rt *TurnRuntime) Commit(msg turnDoneMsg) (stopped bool, err error) {
	rt.drainReady()
	rt.session.End()
	return rt.transcript.Project(TranscriptOutcome{Complete: &TranscriptCompletion{Answer: msg.answer, Reasoning: msg.reasoning, Err: msg.err, Stopped: msg.stopped}})
}

// Stop cancels the in-flight Run.
func (rt *TurnRuntime) Stop() {
	rt.session.Stop()
}

// SetThinkingEnabled sets the thinking-enabled flag used when the turn
// creates messages.
func (rt *TurnRuntime) SetThinkingEnabled(v bool) {
	if rt.session != nil {
		rt.session.SetThinkingEnabled(v)
	}
}

// ThinkingEnabled reports the thinking-enabled flag used when the turn
// creates messages.
func (rt *TurnRuntime) ThinkingEnabled() bool { return rt.session.ThinkingEnabled() }

// LiveTimeline exposes the in-progress turn's arrival-ordered event log for
// read-only rendering.
func (rt *TurnRuntime) LiveTimeline() []TimelineEvent { return rt.transcript.LiveTimeline() }

// DrainReady applies every additional live event already queued on the feed
// (non-blocking), so a fast-arriving burst of small deltas (a real reasoning
// provider often streams one token per SSE event) does not force one
// render per delta: a render is the expensive step (it re-renders the live
// turn's markdown from scratch), so while one render is in flight the feed's
// buffered channel accumulates a backlog, and applying that whole backlog
// before the next render batches the work instead of paying its cost once per
// token. Order is preserved: events are still applied one at a time, in
// arrival order, through the same Accept/Observe path a single event would
// take.
func (rt *TurnRuntime) drainReady() {
	if rt.events == nil {
		return
	}
	for {
		u, ok := rt.events.TryNext()
		if !ok {
			return
		}
		if u.TurnStart {
			rt.OnTurnStart(u.RunID)
			continue
		}
		if rt.Accept(u) {
			rt.project(u)
		}
	}
}

// project projects one live event onto the transcript.
//
// stream deltas grow the streaming assistant message, and tool observations
// land in the tool log and transcript event log. Stream deltas arriving
// while no turn runs are dropped, matching the pre-timeline stream
// behavior. Tool starts arm the busy pulse when thinking is off and motion
// is enabled, so a thinking-off turn still shows visible progress. Observe
// never returns a command today; the return type matches the turn runtime's
// runtime shape so callers do not need to change if projection later needs
// to trigger one.
func (rt *TurnRuntime) project(u Event) tea.Cmd {
	rt.transcript.Project(TranscriptOutcome{Stream: u.Stream, Tool: u.Tool})
	return nil
}
