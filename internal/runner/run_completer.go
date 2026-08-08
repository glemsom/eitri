// Unified per-turn run persistence (issues #1107, #1201): the single
// run-completer that serves all three run transports — UI, batch, and
// sub-agent. After each complete agent turn it persists a running-status
// snapshot of the run's conversation, runs the shared auto-compaction step,
// and re-persists when compaction rewrites the history. On every exit path
// (completed / cancelled / max-turns / error) it writes a terminal snapshot
// and the run timeline through the shared run-end seam (runExit,
// run_exit.go, issue #1238): the seam classifies the exit via the single
// taxonomy (classifyRunExit, ADR-0029 — only true failures produce
// StatusError, while cancellation, max-turns, and success produce StatusIdle),
// dispatches the transport's per-reason work through the single exit switch,
// then calls terminal here for the terminal snapshot + timeline.
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
	"fmt"
	"log/slog"
	"time"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/timeline"
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

	// terminalSnapshotSource, when set, is the facade source the run-end seam
	// (runExit, run_exit.go) uses for the TERMINAL snapshot; it defaults to
	// snapshotSource when nil. UI runs set it to a plain CopySession source
	// (issue #1238): the per-turn live-sync has already delivered every
	// completed turn by the time the run ends, so re-running it at terminal
	// time would replace the UI conversation with history-derived messages and
	// could drop UI-only messages (tool components / quick replies attached
	// during tool execution) that are not in the run history. The terminal
	// snapshot therefore copies the UI conversation exactly as it stands.
	terminalSnapshotSource func(status uisession.Status) *uisession.UISession
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
// under sessions/<id>/session.json using the per-turn snapshot source
// (snapshotSource). No-op when the persister is unavailable or the
// conversation source has no history yet.
func (c *runCompleter) persist(status uisession.Status) {
	c.persistSource(status, c.snapshotSource)
}

// persistSource writes a snapshot facade produced by the given source to disk
// under sessions/<id>/session.json. A nil source falls back to buildUISession
// (the history-derived facade). No-op when the persister is unavailable or the
// source has no history yet.
func (c *runCompleter) persistSource(status uisession.Status, source func(status uisession.Status) *uisession.UISession) {
	if c.svc.persister == nil {
		return
	}
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

// uiSnapshotSource is the UI-transport snapshot source (ADR-0028): the loop's
// session-backed history adapter already reads and writes the canonical
// conversation store directly (issue #1241), so the UI conversation IS the
// run's live history — the former per-turn history→conversation copy
// (message.SyncHistoryToConversation + ReplaceConversationMessages) is gone
// from this path. The source snapshots the UI session facade via CopySession,
// preserving the full UI-session fidelity (ActiveSkills, ClosedAt,
// RenderedMessageIDs) that the history-derived facade omits. Returns nil when
// the UI session is unavailable or the conversation has no messages yet.
//
// The presence check reads a locked copy (CopyConversation), never the live
// shared reference: manual compaction can replace the conversation's message
// list concurrently with a per-turn snapshot (issue #1241 fix round).
func (c *runCompleter) uiSnapshotSource(status uisession.Status) *uisession.UISession {
	svc := c.svc
	if svc.uiSessionMgr == nil {
		return nil
	}
	convo := svc.uiSessionMgr.CopyConversation(c.id)
	if convo == nil || len(convo.Messages) == 0 {
		return nil
	}
	return svc.uiSessionMgr.CopySession(c.id)
}

// terminal writes the terminal snapshot and the run timeline for the given
// termination. It is the terminal step of the single run-end seam (runExit,
// run_exit.go, issue #1238), called by every transport on every exit path.
// The status and termination come from the shared exit taxonomy
// (classifyRunExit, ADR-0029) so every run kind — UI, batch, sub-agent — ends
// with the same semantics: idle on completion / cancellation / max-turns and
// error on failure. The terminal snapshot uses the transport's
// terminalSnapshotSource when set (UI runs: a plain CopySession that preserves
// the UI conversation exactly) and the per-turn snapshotSource otherwise.
func (c *runCompleter) terminal(sseState *runstate.State, status uisession.Status, termination *timeline.TimelineTermination) {
	c.persistSource(status, c.terminalSnapshotSource)
	c.svc.persistRunTimeline(c.id, c.runID, c.startedAt, sseState, c.cfg, termination)
}

// buildUISession assembles the UISession facade from the conversation source's
// history, mirroring UI session snapshots. Returns nil when the source has no
// history (nothing to persist yet).
//
// Run snapshots are plain UISession facades: the system prompt is stored in
// the separate system_prompt field (matching UI snapshots) and the leading
// system message the history manager prepends is stripped from Messages (the
// strip-system-message invariant, ADR-0028). The conversion from the loop's
// LLM-boundary shape ([]EitriMessage, system prompt prepended on reads) to the
// flat []Message is inlined here — the former message.SyncHistoryToConversation
// module was deleted with the old LLM-history store (umbrella #1231,
// issue #1242), leaving EitriMessage as the single canonical message type at
// the loop's LLM boundary.
func (c *runCompleter) buildUISession(status uisession.Status) *uisession.UISession {
	hist := c.historyMgr.History()
	if hist == nil || len(hist) == 0 {
		return nil
	}

	msgs := make([]message.Message, 0, len(hist))
	for _, em := range hist {
		msgs = append(msgs, em.ToMessage())
	}
	if len(msgs) > 0 && msgs[0].Role == "system" {
		msgs = msgs[1:]
	}

	return &uisession.UISession{
		ID:           c.id,
		BrowserID:    c.browserID,
		ParentID:     c.parentID,
		Title:        c.title,
		Status:       status,
		Messages:     msgs,
		Workspace:    c.workspace,
		SystemPrompt: c.systemPrompt,
		CreatedAt:    c.startedAt,
		UpdatedAt:    time.Now(),
	}
}
