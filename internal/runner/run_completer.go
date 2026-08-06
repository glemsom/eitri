// Unified per-turn run persistence (issue #1107): the single run-completer
// that serves both batch runs and sub-agent runs. After each complete agent
// turn it persists a running-status snapshot of the run's conversation, runs
// the shared auto-compaction step, and re-persists when compaction rewrites
// the history. On every exit path (completed / cancelled / max-turns / error)
// it writes a terminal snapshot and the run timeline.
//
// The conversation source is parameterized through the loop.HistoryManager
// seam: session-manager-backed history for batch/UI runs
// (loop.NewSessionHistoryManager), request-based history for sub-agents
// (loop.NewRequestHistoryManager). This removes the former subAgentSnapshotter
// and batchTurnCompleter, which were the same per-turn persistence/compaction/
// terminal logic differing only in where they read the conversation from.

package runner

import (
	"context"
	"log/slog"
	"time"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runner/loop"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
)

// runCompleter implements loop.TurnCompleter for batch and sub-agent runs.
// historyMgr is the conversation source the run writes and reads; the
// snapshot under <id>/ session.json is derived from historyMgr.History().
type runCompleter struct {
	svc          *RunService
	historyMgr   loop.HistoryManager // conversation source (session-manager or request based)
	id           string              // session directory / task ID for history, snapshot, and timeline
	browserID    string              // UI browser linkage (sub-agent UI mode only; empty for batch)
	parentID     string              // parent session linkage (sub-agent only; empty for batch)
	title        string
	systemPrompt string
	workspace    string
	startedAt    time.Time
	cfg          RunConfig
}

// OnTurnComplete implements loop.TurnCompleter: persist a running-status
// snapshot after each complete agent turn, then run the shared auto-compaction
// step so a long run compacts its conversation below the low-water mark instead
// of overflowing the context window (issues #1093, #1096). The compacted
// history is restored into the conversation source and reflected in a follow-up
// snapshot, so the next turn's LLM request and the on-disk session.json stay
// within the same thresholds, salience ordering, and tool-call retention as
// UI runs.
func (c *runCompleter) OnTurnComplete(ctx context.Context, _ string) {
	c.persist(uisession.StatusRunning)

	compactedMsgs, count, freedTokens, prunedToolCalls, err := c.svc.autoCompactAfterTurn(ctx, c.historyMgr, c.cfg)
	if err != nil {
		slog.Warn("run compaction failed, will retry on next turn",
			slog.String("id", c.id),
			slog.Any("error", err),
		)
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
}

// persist writes the current run conversation to disk as a session snapshot
// under sessions/<id>/session.json. No-op when the persister is unavailable or
// the conversation source has no history yet.
func (c *runCompleter) persist(status uisession.Status) {
	if c.svc.persister == nil {
		return
	}
	sess := c.buildUISession(status)
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

// terminal writes the terminal snapshot and the run timeline for the given
// termination. The snapshot status is supplied by the caller so each run kind
// keeps its established exit mapping: sub-agent runs end idle on
// cancellation / max-turns and error on failure, while batch runs end error on
// any non-nil run error (including cancellation) — matching UI exit paths and
// the pre-unification behaviour (issue #1107).
func (c *runCompleter) terminal(sseState *runstate.State, status uisession.Status, termination *runstate.TimelineTermination) {
	c.persist(status)
	c.svc.persistRunTimeline(c.id, runstate.GenerateRunID(c.id, c.startedAt), c.startedAt, sseState, c.cfg, termination)
}

// runCompleterTerminalStatus maps a termination reason to the sub-agent terminal
// snapshot status: idle on completion / cancellation / max-turns and error on
// failure. Batch runs supply their own status (error on any non-nil run error).
func runCompleterTerminalStatus(reason runstate.TerminationReason) uisession.Status {
	if reason == runstate.TerminationError {
		return uisession.StatusError
	}
	return uisession.StatusIdle
}

// buildUISession assembles the UISession facade from the conversation source's
// history, mirroring UI session snapshots. Returns nil when the source has no
// history (nothing to persist yet).
//
// Run snapshots are plain UISession facades: the system prompt is stored in
// the separate system_prompt field (matching UI snapshots) and the leading
// system message the history manager prepends is stripped from Messages.
func (c *runCompleter) buildUISession(status uisession.Status) *uisession.UISession {
	hist := c.historyMgr.History()
	if hist == nil || len(hist) == 0 {
		return nil
	}

	msgs := make([]message.Message, 0, len(hist))
	for i, em := range hist {
		if i == 0 && string(em.Role) == "system" {
			continue
		}
		msgs = append(msgs, em.ToMessage())
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
