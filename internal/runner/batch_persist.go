// Batch-mode session persistence: session ID resolution, per-turn and
// terminal snapshots, and the batch turn-completion seam (issue #1039).
// Batch runs write the same on-disk trail as UI sessions under
// ~/.eitri/sessions/<id>/: session.json snapshots, per-call HTTP traces, and
// a per-run timeline, so the same jq/cat inspection and on-demand
// load/report paths work for headless runs.

package runner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
	uisession "github.com/glemsom/eitri/internal/session"
)

// batchSessionIDEnv overrides the session ID (and thus the
// ~/.eitri/sessions/<id>/ directory) of a headless batch run, letting
// callers like the agent loop name sessions meaningfully.
const batchSessionIDEnv = "EITRI_BATCH_SESSION_ID"

// batchSessionID returns the session ID for a batch run: the value of
// EITRI_BATCH_SESSION_ID when set, otherwise batch-<unixnano>. The env
// override is validated (non-empty, no path separators, no "..") so it can
// never escape the sessions directory.
func batchSessionID() (string, error) {
	if id, ok := os.LookupEnv(batchSessionIDEnv); ok {
		if err := validateBatchSessionID(id); err != nil {
			return "", err
		}
		return id, nil
	}
	return fmt.Sprintf("batch-%d", time.Now().UnixNano()), nil
}

// validateBatchSessionID rejects session IDs that could escape the sessions
// directory or collide with directory traversal. An explicitly empty value is
// rejected too — the caller falls back to the default only when the variable
// is unset, not when it is set to nothing.
func validateBatchSessionID(id string) error {
	if id == "" {
		return fmt.Errorf("%s must not be empty", batchSessionIDEnv)
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("%s %q is invalid: must not contain path separators", batchSessionIDEnv, id)
	}
	if strings.Contains(id, "..") {
		return fmt.Errorf("%s %q is invalid: must not contain \"..\"", batchSessionIDEnv, id)
	}
	return nil
}

// batchTitle derives the batch session title from the prompt using the same
// rule as UI session titles (session.TitlePreview). Blank prompts (e.g. a
// whitespace-only or empty `-b` argument) fall back to the session ID so
// reports and listings never show a blank title.
func batchTitle(prompt, fallback string) string {
	if title := uisession.TitlePreview(prompt); title != "" {
		return title
	}
	return fallback
}

// batchTurnCompleter implements loop.TurnCompleter for headless batch runs.
// After each complete agent turn it persists a session.json snapshot via the
// existing per-turn completion seam — the same seam the UI path uses — and
// then runs the shared auto-compaction step used by UI parent runs (issue
// #1093), so long batch runs compact their conversation history below the
// low-water mark instead of overflowing the context window. The compacted
// history is restored into the session manager and reflected in a follow-up
// snapshot, so the next turn's LLM request and the on-disk session.json stay
// within the same thresholds, salience ordering, and tool-call retention as
// UI runs.
type batchTurnCompleter struct {
	svc          *RunService
	sessionMgr   *history.SessionManager
	sessionID    string
	title        string
	workspace    string
	systemPrompt string
	createdAt    time.Time
	cfg          RunConfig
}

func (b *batchTurnCompleter) OnTurnComplete(ctx context.Context, _ string) {
	b.svc.batchSnapshot(b.sessionMgr, b.sessionID, uisession.StatusRunning, b.title, b.workspace, b.systemPrompt, b.createdAt)

	// Shared auto-compaction: same thresholds, salience ordering, and tool-call
	// retention as UI runs (the settings come from the same RunConfig). The
	// compacted history replaces the session manager's history, so the next
	// turn's LLM request stays within the context window.
	compactedMsgs, count, freedTokens, prunedToolCalls, err := b.svc.autoCompactAfterTurn(ctx, b.sessionMgr, b.sessionID, b.cfg)
	if err != nil {
		slog.Warn("batch compaction failed, will retry on next turn",
			slog.String("session_id", b.sessionID),
			slog.Any("error", err),
		)
		return
	}
	if compactedMsgs == nil {
		return
	}

	// Re-snapshot so session.json on disk reflects the compacted history.
	b.svc.batchSnapshot(b.sessionMgr, b.sessionID, uisession.StatusRunning, b.title, b.workspace, b.systemPrompt, b.createdAt)
	slog.Info("batch run compacted conversation history",
		slog.String("session_id", b.sessionID),
		slog.Int("compacted_count", count),
		slog.Int("freed_tokens", freedTokens),
		slog.Int("pruned_tool_calls", prunedToolCalls),
	)
}

// batchSnapshot writes the current batch run state to disk as a session.json
// snapshot in the same shape as UI sessions, so batch runs leave the same
// reviewable trail. No-op when the persister is unavailable or the session
// has no history yet.
//
// Batch snapshots are plain UISession facades: no browser ID, no components,
// no active skills — a batch run has none. The system prompt is stored in
// the separate system_prompt field (matching UI snapshots) and the leading
// system message the history manager prepends is stripped from Messages.
func (s *RunService) batchSnapshot(sessionMgr *history.SessionManager, sessionID string, status uisession.Status, title, workspace, systemPrompt string, createdAt time.Time) {
	if s.persister == nil {
		return
	}
	sess := buildBatchUISession(sessionMgr, sessionID, status, title, workspace, systemPrompt, createdAt)
	if sess == nil {
		return
	}
	if err := s.persister.SnapshotSession(sessionID, sess); err != nil {
		slog.Warn("failed to snapshot batch session",
			slog.String("session_id", sessionID),
			slog.Any("error", err),
		)
	}
}

// buildBatchUISession assembles the UISession facade for a batch run from the
// history manager's conversation. Returns nil when the session has no history
// (nothing to persist yet).
func buildBatchUISession(sessionMgr *history.SessionManager, sessionID string, status uisession.Status, title, workspace, systemPrompt string, createdAt time.Time) *uisession.UISession {
	hist := sessionMgr.History(sessionID)
	if hist == nil {
		return nil
	}

	msgs := make([]message.Message, 0, len(hist))
	for i, em := range hist {
		// The history manager prepends the system prompt as the first
		// message; UI snapshots keep it in the system_prompt field instead.
		if i == 0 && string(em.Role) == "system" {
			continue
		}
		msgs = append(msgs, em.ToMessage())
	}

	return &uisession.UISession{
		ID:           sessionID,
		Title:        title,
		Status:       status,
		Messages:     msgs,
		Workspace:    workspace,
		SystemPrompt: systemPrompt,
		CreatedAt:    createdAt,
		UpdatedAt:    time.Now(),
	}
}
