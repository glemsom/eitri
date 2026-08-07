// run_exit.go — the single terminal seam for UI, batch, and sub-agent runs
// (ADR-0028/0029, issue #1238): exit classification, the per-reason exit
// switch, and the terminal snapshot + timeline write all live in this file,
// so every transport finishes every exit path through exactly the same code.
// The UI transport previously re-implemented the status snapshot + timeline
// writes itself on every exit path and both the UI and sub-agent transports
// hand-rolled the per-reason exit switch; here the switch exists in exactly
// one place (exitWork.run), so adding a termination reason touches exactly one
// file. runExit is directly callable, so exit-path behaviour is unit-testable
// without launching a full run.

package runner

import (
	"context"
	"errors"

	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/timeline"
	"github.com/glemsom/eitri/internal/uixt"
)

// exitOutcome is the result of the single exit taxonomy (ADR-0029): a run's
// terminal snapshot status paired with its timeline termination reason. The
// UI, batch, and sub-agent transports all derive their terminal state from
// classifyRunExit, so the same outcome classification is used everywhere.
type exitOutcome struct {
	Status      uisession.Status
	Termination *timeline.TimelineTermination
}

// classifyRunExit is the single exit taxonomy shared by the UI, batch, and
// sub-agent transports (ADR-0029). It classifies a finished run's error and
// run context into the terminal snapshot status and the timeline termination
// reason. Only true failures produce StatusError; cancellation, max-turns,
// and success produce StatusIdle — aligning batch with the UI/sub-agent
// semantics that previously diverged (issue #1107 introduced a batch-only
// error status for cancelled / max-turns runs; #1202 realigns them).
//
// The classification order matches the pre-unification exit paths: a run
// whose context was cancelled is reported as cancelled even when the returned
// error is a different (wrapped) error; otherwise max-turns is recognized
// before falling through to a generic error.
func classifyRunExit(runErr error, runCtx context.Context) exitOutcome {
	switch {
	case runErr == nil:
		return exitOutcome{
			Status:      uisession.StatusIdle,
			Termination: &timeline.TimelineTermination{Reason: timeline.TerminationCompleted},
		}

	case runCtx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded):
		return exitOutcome{
			Status: uisession.StatusIdle,
			Termination: &timeline.TimelineTermination{
				Reason:  timeline.TerminationCancelled,
				Message: "Run cancelled by user or context deadline exceeded",
			},
		}

	default:
		var maxTurnsErr *loop.MaxTurnsExceededError
		if errors.As(runErr, &maxTurnsErr) {
			return exitOutcome{
				Status: uisession.StatusIdle,
				Termination: &timeline.TimelineTermination{
					Reason:  timeline.TerminationMaxTurns,
					Message: uixt.MaxTurnsMessage(maxTurnsErr.Limit),
				},
			}
		}
		return exitOutcome{
			Status: uisession.StatusError,
			Termination: &timeline.TimelineTermination{
				Reason:  timeline.TerminationError,
				Message: runErr.Error(),
			},
		}
	}
}

// exitWork carries the transport-specific work a run transport performs on
// each termination reason. The dispatch switch (exitWork.run) is the single
// per-reason exit switch shared by all transports (issue #1238): it lives in
// exactly one place, so adding a termination reason touches the taxonomy
// (classifyRunExit) and the switch in this file — no transport hand-rolls its
// own cancelled / max-turns / error branching. Transports whose exit work is
// uniform across reasons (batch — none) pass a nil exitWork.
//
// Each handler receives the classified outcome; transports that need the raw
// run error (e.g. the sub-agent's record.Err) capture it in the handler
// closure at construction.
type exitWork struct {
	completed func(outcome exitOutcome)
	cancelled func(outcome exitOutcome)
	maxTurns  func(outcome exitOutcome)
	error     func(outcome exitOutcome)

	// afterTerminal, when set, runs once AFTER the terminal snapshot + timeline
	// write. Transports use it for transport-specific work that must not reach
	// observers before the terminal write lands — e.g. the UI's session_status
	// broadcast, so browser subscribers never observe the terminal status
	// before the terminal snapshot is on disk (issue #1238).
	afterTerminal func(outcome exitOutcome)
}

// run is the single per-reason exit switch: it dispatches exactly the
// transport's handler for the classified termination reason. Unhandled
// reasons (a nil handler) run no transport-specific work; the terminal
// snapshot + timeline write is performed by the run-end seam regardless.
func (w *exitWork) run(outcome exitOutcome) {
	switch outcome.Termination.Reason {
	case timeline.TerminationCompleted:
		if w.completed != nil {
			w.completed(outcome)
		}
	case timeline.TerminationCancelled:
		if w.cancelled != nil {
			w.cancelled(outcome)
		}
	case timeline.TerminationMaxTurns:
		if w.maxTurns != nil {
			w.maxTurns(outcome)
		}
	case timeline.TerminationError:
		if w.error != nil {
			w.error(outcome)
		}
	}
}

// runExit is the single terminal seam every transport — UI, batch, sub-agent —
// uses on every exit path (completed / cancelled / max-turns / error): it
// classifies the finished run through the shared exit taxonomy, dispatches the
// transport's per-reason work through the single exit switch, writes the
// terminal snapshot and the run timeline, then runs the transport's optional
// afterTerminal work. Transports keep only their transport-specific work
// (message appending, SSE events, crash dumps, sub-agent record status) in
// exitWork handlers; nothing in a transport exit path classifies termination
// or writes the terminal snapshot/timeline itself. It is directly callable so
// exit-path tests run as unit tests without launching a full service run
// (issue #1238).
func (c *runCompleter) runExit(sseState *runstate.State, runErr error, runCtx context.Context, work *exitWork) {
	outcome := classifyRunExit(runErr, runCtx)
	if work != nil {
		work.run(outcome)
	}
	c.terminal(sseState, outcome.Status, outcome.Termination)
	if work != nil && work.afterTerminal != nil {
		work.afterTerminal(outcome)
	}
}
