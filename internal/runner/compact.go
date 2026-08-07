// Shared auto-compaction for runs. UI, batch, and sub-agent turn completion
// all route through the unified runCompleter (run_completer.go), whose
// OnTurnComplete calls autoCompactAfterTurn so compaction settings in
// ~/.eitri/config.json are honored identically across parent and sub-agent
// runs (issues #1093, #1096, #1201).

package runner

import (
	"context"

	"github.com/glemsom/eitri/internal/message"
	"github.com/glemsom/eitri/internal/runner/loop"
)

// autoCompactAfterTurn is the shared auto-compaction step for runs (UI and
// batch parents, plus sub-agents). It runs the compactor against the run's
// current conversation history when the configured high-water mark is
// exceeded, using the same thresholds, salience ordering, and tool-call
// retention as on-demand compaction, and replaces the history with the
// compacted version via the given history manager's replace-history
// capability (both session-manager-backed and request-based histories).
//
// It is a no-op when compaction is disabled in config, when the context
// window is unset, when the run has no history, or when the estimated
// history size is at or below the high-water mark — in all those cases the
// returned compacted slice is nil and the history is left untouched.
//
// When compaction runs, the returned compacted slice is the compactor's
// output (including the leading system message when the input had one), so
// callers can sync it into mode-specific state (UI session, snapshot) without
// re-reading history. The returned count/freed/pruned stats mirror the
// compactor's report for the compaction_complete UI event.
func (s *RunService) autoCompactAfterTurn(ctx context.Context, historyMgr loop.HistoryManager, cfg RunConfig) (compacted []message.Message, compactedCount, freedTokens, prunedToolCalls int, err error) {
	if !cfg.CompactionEnabled || cfg.ContextWindowTokens <= 0 {
		return nil, 0, 0, 0, nil
	}

	historyMsgs := historyMgr.History()
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

	// Replace the run's history with the compacted version so the next turn's
	// LLM request stays within the context window (session-manager-backed and
	// request-based histories both support this via ReplaceHistory).
	historyMgr.ReplaceHistory(compactedMsgs)
	return compactedMsgs, compactedCount, freedTokens, prunedToolCalls, nil
}
