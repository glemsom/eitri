package runner

import (
	"fmt"
	"log/slog"
	"sync"

)

// subagentStore manages sub-agent lifecycle — spawning records, collection,
// cancellation, and TTL reaping.
type subagentStore struct {
	mu         sync.Mutex
	agents     map[string]*subAgentRecord
	nextTaskID uint64

	// Parent run configs per session (for sub-agent setup)
	parentCfgMu sync.Mutex
	parentCfgs  map[string]RunConfig
}

func newSubagentStore() *subagentStore {
	return &subagentStore{
		agents:     make(map[string]*subAgentRecord),
		parentCfgs: make(map[string]RunConfig),
	}
}

// nextID generates a unique task ID.
func (ss *subagentStore) nextID() string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.nextTaskID++
	return fmt.Sprintf("task_%d", ss.nextTaskID)
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

// getRecords returns sub-agent records for the given task IDs.
func (ss *subagentStore) getRecords(taskIDs []string) (map[string]*subAgentRecord, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	records := make(map[string]*subAgentRecord, len(taskIDs))
	for _, tid := range taskIDs {
		rec, exists := ss.agents[tid]
		if !exists {
			return nil, fmt.Errorf("unknown task_id: %s", tid)
		}
		records[tid] = rec
	}
	return records, nil
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

// DeleteParentCfg removes the parent run config for a session.
func (ss *subagentStore) DeleteParentCfg(sessionID string) {
	ss.parentCfgMu.Lock()
	delete(ss.parentCfgs, sessionID)
	ss.parentCfgMu.Unlock()
}

// CollectResults wraps subAgentRecordToResult for a found record.
func CollectResult(rec *subAgentRecord) SubAgentResult {
	return subAgentRecordToResult(rec)
}
