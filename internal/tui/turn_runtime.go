package tui

import tea "charm.land/bubbletea/v2"

// TurnRuntime owns one agent turn's live event acceptance: the current run
// ID, stale-event rejection, and draining/waiting on the live merged event
// feed. It wraps the existing TurnSession and Fold modules, which for now
// still own turn start/stop/commit and stream/tool projection respectively;
// later seams move that behaviour behind TurnRuntime too.
type TurnRuntime struct {
	session   *TurnSession
	fold      *Fold
	events    *EventFeed
	liveRunID int
}

// NewTurnRuntime builds a runtime bound to the given turn session, fold, and
// live merged event feed (nil when no engine event stream is wired).
func NewTurnRuntime(session *TurnSession, fold *Fold, events *EventFeed) *TurnRuntime {
	return &TurnRuntime{session: session, fold: fold, events: events, liveRunID: -1}
}

// HasEvents reports whether a live merged event feed is wired.
func (rt *TurnRuntime) HasEvents() bool { return rt.events != nil }

// Begin arms a fresh run ID, drains any stale events left over from a prior
// turn, and starts the session's turn; when a live event feed is wired the
// returned command also starts the spinner so the busy indicator animates.
func (rt *TurnRuntime) Begin(tx *Transcript, prompt, payload string) tea.Cmd {
	rt.liveRunID = -1
	if rt.events != nil {
		rt.events.Drain()
	}
	cmd := rt.session.Begin(tx, prompt, payload)
	if rt.events != nil {
		return tea.Batch(cmd, spinnerTick())
	}
	return cmd
}

// OnTurnStart records the run ID for a turn's engine-reported start; only
// events matching this run ID are accepted until the next Begin/OnTurnStart.
func (rt *TurnRuntime) OnTurnStart(runID int) { rt.liveRunID = runID }

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
