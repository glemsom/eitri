// Shared auto-compaction for parent runs. UI turn completion (run.go) and
// batch turn completion (batch_persist.go) both call autoCompactAfterTurn so
// compaction settings in ~/.eitri/config.json are honored identically in both
// modes (issue #1093).

package runner

import (
	"context"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/message"
)

// autoCompactAfterTurn is the shared auto-compaction step for parent runs (UI
// and batch). It runs the compactor against the session's current conversation
// history when the configured high-water mark is exceeded, using the same
// thresholds, salience ordering, and tool-call retention as on-demand
// compaction, and replaces the in-memory history with the compacted version
// via the given history manager.
//
// It is a no-op when compaction is disabled in config, when the context
// window is unset, when the session has no history, or when the estimated
// history size is at or below the high-water mark — in all those cases the
// returned compacted slice is nil and the history is left untouched.
//
// When compaction runs, the returned compacted slice is the compactor's
// output (including the leading system message when the input had one), so
// callers can sync it into mode-specific state (UI session, snapshot) without
// re-reading history. The returned count/freed/pruned stats mirror the
// compactor's report for the compaction_complete UI event.
func (s *RunService) autoCompactAfterTurn(ctx context.Context, sessionMgr *history.SessionManager, sessionID string, cfg RunConfig) (compacted []message.Message, compactedCount, freedTokens, prunedToolCalls int, err error) {
	if !cfg.CompactionEnabled || cfg.ContextWindowTokens <= 0 {
		return nil, 0, 0, 0, nil
	}

	historyMsgs := sessionMgr.History(sessionID)
	if historyMsgs == nil {
		return nil, 0, 0, 0, nil
	}

	highWater := cfg.ContextWindowTokens * cfg.CompactionThresholdPercent / 100
	lowWater := cfg.ContextWindowTokens * cfg.CompactionLowWaterPercent / 100

	// Build a throwaway LLM service for summarization.
	client, err := newCompactLLMService(ctx, cfg, s.persistAuth)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	// Convert to flat messages for the compactor.
	flatMsgs := make([]message.Message, len(historyMsgs))
	for i, em := range historyMsgs {
		flatMsgs[i] = em.ToMessage()
	}

	compactedMsgs, compactedCount, freedTokens, prunedToolCalls, compErr := compactSessionHistory(ctx, flatMsgs, client, s.calibrationStore, highWater, lowWater, cfg.CompactionMessageSizeThreshold, cfg.CompactionToolCallRetentionTurns, cfg.CompactionSalienceEnabled, cfg.ModelName)
	if compErr != nil {
		return nil, 0, 0, 0, compErr
	}
	if compactedMsgs == nil || (compactedCount == 0 && prunedToolCalls == 0) {
		return nil, 0, 0, 0, nil
	}

	// Replace in-memory history with compacted version so the next turn's LLM
	// request stays within the context window.
	sessionMgr.RestoreHistory(sessionID, compactedMsgs)
	return compactedMsgs, compactedCount, freedTokens, prunedToolCalls, nil
}
