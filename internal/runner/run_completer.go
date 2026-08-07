// Unified per-turn run persistence (issues #1107, #1201): the single
// run-completer that serves all three run transports — UI, batch, and
// sub-agent. After each complete agent turn it persists a running-status
// snapshot of the run's conversation, runs the shared auto-compaction step,
// and re-persists when compaction rewrites the history. On every exit path
// (completed / cancelled / max-turns / error) it writes a terminal snapshot
// and the run timeline. Terminal status is classified by the single exit
// taxonomy in this file (classifyRunExit, ADR-0029), shared by the UI,
// batch, and sub-agent transports: only true failures produce StatusError,
// while cancellation, max-turns, and success produce StatusIdle.
//
// The conversation source is parameterized through the loop.HistoryManager
// seam: session-manager-backed history for UI/batch runs
// (loop.NewSessionHistoryManager), request-based history for sub-agents
// (loop.NewRequestHistoryManager). The snapshot facade is parameterized
// through the per-transport snapshot-source seam (ADR-0028): UI runs
// live-sync the UI conversation and snapshot via CopySession; batch and
// sub-agent runs build the facade from history via buildUISession.
// This removed the former RunService.OnTurnComplete, subAgentSnapshotter,
// and batchTurnCompleter, which were the same per-turn persistence/compaction/
// terminal logic differing only in where they read the conversation from.

package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/timeline"
	"github.com/glemsom/eitri/internal/uixt"
)

// runCompleter implements loop.TurnCompleter for UI, batch, and sub-agent
// runs. historyMgr is the conversation source the run writes and reads; the
// snapshot under <id>/ session.json is derived from historyMgr.History()
// (batch/sub-agent) or from the UI session (UI mode).
type runCompleter struct {
	svc        *RunService
	historyMgr loop.HistoryManager // conversation source (session-manager or request based)
	id         string              // session directory / task ID for history, snapshot, and timeline
	// runID is the run identifier generated exactly once at run start and
	// plumbed through the run options (loop.RunOpts.RunID) and this terminal
	// seam (issue #1234). The persisted timeline, SSE events, and HTTP traces
	// all carry this same identifier by construction, so turns stay
	// correlatable to their traces by ID (issue #988). It is never recomputed
	// from (id, startedAt) here.
	runID        string
	browserID    string // UI browser linkage (sub-agent UI mode only; empty for batch)
	parentID     string // parent session linkage (sub-agent only; empty for batch)
	title        string
	systemPrompt string
	workspace    string
	startedAt    time.Time
	cfg          RunConfig

	// snapshotSource is the per-transport seam that builds the <id>/
	// session.json facade (ADR-0028). UI runs live-sync the run's live
	// conversation into the UI session and snapshot via CopySession
	// (preserving the full UI-session fidelity — ActiveSkills, ClosedAt,
	// RenderedMessageIDs — that the history-derived facade omits); batch and
	// sub-agent runs build the facade from history via buildUISession.
	// Nil defaults to buildUISession.
	snapshotSource func(status uisession.Status) *uisession.UISession
}

// OnTurnComplete implements loop.TurnCompleter: persist a running-status
// snapshot after each complete agent turn, then run the shared auto-compaction
// step so a long run compacts its conversation below the low-water mark instead
// of overflowing the context window (issues #1093, #1096). The compacted
// history is restored into the conversation source and reflected in a follow-up
// snapshot, so the next turn's LLM request and the on-disk session.json stay
// within the same thresholds, salience ordering, and tool-call retention as
// UI runs. UI parent runs additionally surface compaction outcomes as SSE
// events (a warning toast on failure, a compaction_complete event on success);
// batch and sub-agent runs have no RunState and only log.
func (c *runCompleter) OnTurnComplete(ctx context.Context, _ string) {
	c.persist(uisession.StatusRunning)

	compactedMsgs, count, freedTokens, prunedToolCalls, err := c.svc.autoCompactAfterTurn(ctx, c.historyMgr, c.cfg)
	if err != nil {
		slog.Warn("run compaction failed, will retry on next turn",
			slog.String("id", c.id),
			slog.Any("error", err),
		)
		if runState := c.svc.get(c.id); runState != nil {
			runState.SSE.Broadcast(runstate.SSEEvent{
				Type: "toast",
				Data: map[string]any{
					"level":   "warning",
					"message": "Compaction failed: " + err.Error(),
				},
			})
		}
		return
	}
	if compactedMsgs == nil {
		return
	}

	// Re-snapshot so session.json on disk reflects the compacted history.
	c.persist(uisession.StatusRunning)
	slog.Info("run compacted conversation history",
		slog.String("id", c.id),
		slog.Int("compacted_count", count),
		slog.Int("freed_tokens", freedTokens),
		slog.Int("pruned_tool_calls", prunedToolCalls),
	)
	if runState := c.svc.get(c.id); runState != nil {
		toastMsg := fmt.Sprintf("Compacted %d messages — freed ~%dk tokens", count, freedTokens/1000)
		if prunedToolCalls > 0 {
			toastMsg += fmt.Sprintf(". %d tool calls pruned", prunedToolCalls)
		}
		runState.SSE.Broadcast(runstate.SSEEvent{
			Type: "compaction_complete",
			Data: map[string]any{
				"compacted_count":   count,
				"freed_tokens":      freedTokens,
				"pruned_tool_calls": prunedToolCalls,
				"message":           toastMsg,
			},
		})
	}
}

// persist writes the current run conversation to disk as a session snapshot
// under sessions/<id>/session.json. No-op when the persister is unavailable or
// the conversation source has no history yet.
func (c *runCompleter) persist(status uisession.Status) {
	if c.svc.persister == nil {
		return
	}
	source := c.snapshotSource
	if source == nil {
		source = c.buildUISession
	}
	sess := source(status)
	if sess == nil {
		return
	}
	if err := c.svc.persister.SnapshotSession(c.id, sess); err != nil {
		slog.Warn("failed to snapshot run session",
			slog.String("id", c.id),
			slog.Any("error", err),
		)
	}
}

// uiSnapshotSource is the UI-transport snapshot source (ADR-0028): it
// live-syncs the run's live conversation (history manager) into the UI
// session — so the browser UI and snapshots show incremental progress during
// long runs instead of appearing frozen on the original user message — then
// snapshots the UI session facade via CopySession, preserving the full
// UI-session fidelity (ActiveSkills, ClosedAt, RenderedMessageIDs) that the
// history-derived facade omits. Returns nil when the UI session is unavailable
// or the conversation source has no history yet.
func (c *runCompleter) uiSnapshotSource(status uisession.Status) *uisession.UISession {
	svc := c.svc
	if svc.uiSessionMgr == nil {
		return nil
	}
	hist := c.historyMgr.History()
	if hist == nil || len(hist) == 0 {
		return nil
	}
	svc.uiSessionMgr.ReplaceConversationMessages(c.id, stripLeadingSystemMessage(historyToMessages(hist)))
	return svc.uiSessionMgr.CopySession(c.id)
}

// historyToMessages converts the conversation source's history entries into
// the flat message shape shared by every persisted facade.
func historyToMessages(hist []message.EitriMessage) []message.Message {
	msgs := make([]message.Message, 0, len(hist))
	for _, em := range hist {
		msgs = append(msgs, em.ToMessage())
	}
	return msgs
}

// stripLeadingSystemMessage removes the leading system message from a
// conversation message list — the single home of the strip-system-message
// invariant (ADR-0028): the system prompt is persisted separately
// (UISession.SystemPrompt / the history manager's system prompt), so it must
// never appear in a persisted facade's Messages list. All run transports and
// manual compaction funnel their UI/snapshot message lists through here.
func stripLeadingSystemMessage(msgs []message.Message) []message.Message {
	if len(msgs) > 0 && msgs[0].Role == "system" {
		return msgs[1:]
	}
	return msgs
}

// terminal writes the terminal snapshot and the run timeline for the given
// termination. The status and termination come from the shared exit taxonomy
// (classifyRunExit, ADR-0029) so every run kind — UI, batch, sub-agent — ends
// with the same semantics: idle on completion / cancellation / max-turns and
// error on failure.
func (c *runCompleter) terminal(sseState *runstate.State, status uisession.Status, termination *timeline.TimelineTermination) {
	c.persist(status)
	c.svc.persistRunTimeline(c.id, c.runID, c.startedAt, sseState, c.cfg, termination)
}

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

// buildUISession assembles the UISession facade from the conversation source's
// history, mirroring UI session snapshots. Returns nil when the source has no
// history (nothing to persist yet).
//
// Run snapshots are plain UISession facades: the system prompt is stored in
// the separate system_prompt field (matching UI snapshots) and the leading
// system message the history manager prepends is stripped from Messages (the
// strip-system-message invariant, see stripLeadingSystemMessage).
func (c *runCompleter) buildUISession(status uisession.Status) *uisession.UISession {
	hist := c.historyMgr.History()
	if hist == nil || len(hist) == 0 {
		return nil
	}

	return &uisession.UISession{
		ID:           c.id,
		BrowserID:    c.browserID,
		ParentID:     c.parentID,
		Title:        c.title,
		Status:       status,
		Messages:     stripLeadingSystemMessage(historyToMessages(hist)),
		Workspace:    c.workspace,
		SystemPrompt: c.systemPrompt,
		CreatedAt:    c.startedAt,
		UpdatedAt:    time.Now(),
	}
}
