package runner

import (
	"log/slog"
	"sync"
	"time"
)

// subagentStore manages sub-agent lifecycle — spawning records, collection,
// cancellation, and TTL reaping.
type subagentStore struct {
	mu     sync.Mutex
	agents map[string]*subAgentRecord

	// reapTTL is how long after a sub-agent finishes its in-memory record is
	// retained before being reaped. Defaults to subAgentReapTTL; tests shrink it
	// to exercise the post-reap collect path (issue #1200).
	reapTTL time.Duration

	// completed holds the durable result of every sub-agent that reached a
	// terminal state. Unlike agents, entries here are NOT deleted by the TTL
	// reap, so a parent may collect a completed sub-agent's result even after
	// its in-memory record has been reaped (issue #1200).
	completed map[string]completedResult

	// Parent run configs per session (for sub-agent setup)
	parentCfgMu sync.Mutex
	parentCfgs  map[string]RunConfig
}

// completedResult is a durable terminal sub-agent result, retaining the
// parent session so session-scoped cleanup can reclaim it.
type completedResult struct {
	sessionID string
	result    SubAgentResult
}

func newSubagentStore() *subagentStore {
	return &subagentStore{
		agents:     make(map[string]*subAgentRecord),
		completed:  make(map[string]completedResult),
		parentCfgs: make(map[string]RunConfig),
		reapTTL:    subAgentReapTTL,
	}
}

// storeRecord stores a sub-agent record.
func (ss *subagentStore) storeRecord(taskID string, record *subAgentRecord) {
	ss.mu.Lock()
	ss.agents[taskID] = record
	ss.mu.Unlock()
}

// getRecord returns a sub-agent record by task ID.
func (ss *subagentStore) getRecord(taskID string) *subAgentRecord {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return ss.agents[taskID]
}

// storeCompletedResult records the durable result of a terminal sub-agent
// alongside its parent session. It survives the TTL reap so a later collect
// still returns the result (issue #1200).
func (ss *subagentStore) storeCompletedResult(sessionID, taskID string, result SubAgentResult) {
	ss.mu.Lock()
	ss.completed[taskID] = completedResult{sessionID: sessionID, result: result}
	ss.mu.Unlock()
}

// getCompletedResult returns a previously-committed terminal sub-agent result
// and whether one exists (i.e. the task was a real sub-agent that has finished).
func (ss *subagentStore) getCompletedResult(taskID string) (SubAgentResult, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	res, ok := ss.completed[taskID]
	return res.result, ok
}

// CancelForSession cancels all in-flight sub-agents for a given parent session.
func (ss *subagentStore) CancelForSession(sessionID string) {
	ss.mu.Lock()
	var toCancel []*subAgentRecord
	for _, rec := range ss.agents {
		if rec.SessionID == sessionID {
			toCancel = append(toCancel, rec)
		}
	}
	ss.mu.Unlock()

	for _, rec := range toCancel {
		slog.Info("cancelling sub-agent", slog.String("task_id", rec.TaskID))
		rec.Cancel()
	}
}

// reapAfterTTL schedules deletion of a completed sub-agent record after TTL.
func (ss *subagentStore) reapAfterTTL(taskID string) {
	// Reap is done inline; the caller's goroutine calls this after delay.
	ss.mu.Lock()
	delete(ss.agents, taskID)
	ss.mu.Unlock()
}

// StoreParentCfg stores a parent run config for sub-agent setup.
func (ss *subagentStore) StoreParentCfg(sessionID string, cfg RunConfig) {
	ss.parentCfgMu.Lock()
	ss.parentCfgs[sessionID] = cfg
	ss.parentCfgMu.Unlock()
}

// GetParentCfg retrieves the parent run config for a session.
func (ss *subagentStore) GetParentCfg(sessionID string) (RunConfig, bool) {
	ss.parentCfgMu.Lock()
	cfg, ok := ss.parentCfgs[sessionID]
	ss.parentCfgMu.Unlock()
	return cfg, ok
}

// DeleteParentCfg removes the parent run config for a session and reclaims
// that session's completed sub-agent results.
func (ss *subagentStore) DeleteParentCfg(sessionID string) {
	ss.parentCfgMu.Lock()
	delete(ss.parentCfgs, sessionID)
	ss.parentCfgMu.Unlock()

	ss.mu.Lock()
	for taskID, cr := range ss.completed {
		if cr.sessionID == sessionID {
			delete(ss.completed, taskID)
		}
	}
	ss.mu.Unlock()
}

// CollectResults wraps subAgentRecordToResult for a found record.
func CollectResult(rec *subAgentRecord) SubAgentResult {
	return subAgentRecordToResult(rec)
}
