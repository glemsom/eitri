package runner

import (
	"sync"

	uisession "github.com/glemsom/eitri/internal/session"
)

// runTracker manages the active runs map with concurrency-safe access.
// It provides Cancel, CancelAll, active-run lookups, and snapshot methods.
type runTracker struct {
	mu     sync.Mutex
	active map[string]*RunState

	// Batch conversation context tracking (set by BatchRun, consumed by crash dump)
	batchCtxMu   sync.Mutex
	batchLastCtx *debugConversationContext
}

// debugConversationContext mirrors debug.ConversationContext to avoid importing debug.
type debugConversationContext struct {
	LastUserMessage      string
	LastAssistantMessage string
	TurnNumber           int
}

func newRunTracker() *runTracker {
	return &runTracker{
		active: make(map[string]*RunState),
	}
}

// get returns the RunState for a session without checking if done.
func (rt *runTracker) get(sessionID string) *RunState {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.active[sessionID]
}

// getActive returns the RunState for a session if it exists and is not done.
func (rt *runTracker) getActive(sessionID string) *RunState {
	rt.mu.Lock()
	state, exists := rt.active[sessionID]
	rt.mu.Unlock()
	if !exists {
		return nil
	}
	select {
	case <-state.Done:
		return nil
	default:
		return state
	}
}

// store inserts a RunState for a session.
func (rt *runTracker) store(sessionID string, state *RunState) {
	rt.mu.Lock()
	rt.active[sessionID] = state
	rt.mu.Unlock()
}

// remove deletes the RunState for a session if it matches the given pointer.
func (rt *runTracker) remove(sessionID string, state *RunState) {
	rt.mu.Lock()
	if rt.active[sessionID] == state {
		delete(rt.active, sessionID)
	}
	rt.mu.Unlock()
}

// exchangeIfDone deletes the run state if it's done, returning true if removed.
func (rt *runTracker) exchangeIfDone(sessionID string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if existing, exists := rt.active[sessionID]; exists {
		select {
		case <-existing.Done:
			delete(rt.active, sessionID)
			return true
		default:
			return false
		}
	}
	return false
}

// removeRun removes and returns the RunState for a session.
func (rt *runTracker) removeRun(sessionID string) *RunState {
	rt.mu.Lock()
	state, exists := rt.active[sessionID]
	if exists {
		delete(rt.active, sessionID)
	}
	rt.mu.Unlock()
	if !exists {
		return nil
	}
	return state
}

// removeAll returns all RunStates and clears the map.
func (rt *runTracker) removeAll() []*RunState {
	rt.mu.Lock()
	states := make([]*RunState, 0, len(rt.active))
	for sessionID, state := range rt.active {
		delete(rt.active, sessionID)
		states = append(states, state)
	}
	rt.mu.Unlock()
	return states
}

// count returns the number of active runs.
func (rt *runTracker) count() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return len(rt.active)
}

// allActiveStates returns all non-done RunStates.
func (rt *runTracker) allActiveStates() []*RunState {
	rt.mu.Lock()
	states := make([]*RunState, 0, len(rt.active))
	for _, state := range rt.active {
		select {
		case <-state.Done:
		default:
			states = append(states, state)
		}
	}
	rt.mu.Unlock()
	return states
}

// sseCounters returns subscriber and replay counts for all active runs.
func (rt *runTracker) sseCounters() map[string]struct{ SubscriberCount, ReplayCount uint64 } {
	rt.mu.Lock()
	result := make(map[string]struct{ SubscriberCount, ReplayCount uint64 }, len(rt.active))
	for sessionID, state := range rt.active {
		select {
		case <-state.Done:
		default:
			result[sessionID] = struct{ SubscriberCount, ReplayCount uint64 }{
				SubscriberCount: state.SSE.SubscriberCount(),
				ReplayCount:     state.SSE.ReplayCount(),
			}
		}
	}
	rt.mu.Unlock()
	return result
}

// sseSnapshot returns a snapshot of SSE counters, history, and run state for a session.
func (rt *runTracker) sseSnapshot(sessionID string) *RunSSESnapshot {
	rt.mu.Lock()
	state, exists := rt.active[sessionID]
	if !exists {
		rt.mu.Unlock()
		return nil
	}
	select {
	case <-state.Done:
		rt.mu.Unlock()
		return nil
	default:
	}

	history := state.SSE.History()
	if len(history) > 50 {
		history = history[len(history)-50:]
	}

	turns := state.Turns
	rt.mu.Unlock()

	return &RunSSESnapshot{
		SubscriberCount: state.SSE.SubscriberCount(),
		ReplayCount:     state.SSE.ReplayCount(),
		History:         history,
		Busy:            true,
		Turns:           turns,
	}
}

// setBatchCtx stores the conversation context for the last batch run.
func (rt *runTracker) setBatchCtx(ctx *debugConversationContext) {
	rt.batchCtxMu.Lock()
	defer rt.batchCtxMu.Unlock()
	rt.batchLastCtx = ctx
}

// getBatchCtx returns the conversation context from the last batch run.
func (rt *runTracker) getBatchCtx() *debugConversationContext {
	rt.batchCtxMu.Lock()
	defer rt.batchCtxMu.Unlock()
	return rt.batchLastCtx
}

// notifyAllClosed broadcasts a closed event to all active sessions.
func (rt *runTracker) notifyAllClosed(message string) {
	for _, state := range rt.allActiveStates() {
		state.SSE.BroadcastClosed(message)
	}
}

// broadcastStatusUpdate broadcasts a session status change to browser-level subscribers.
func (rt *runTracker) broadcastStatusUpdate(sessionID string, status uisession.Status, uiSessionMgr *uisession.Manager, bb *browserBroadcaster) {
	if uiSessionMgr == nil {
		return
	}
	uiSessionMgr.UpdateStatus(sessionID, status)
	sess := uiSessionMgr.Get(sessionID)
	if sess == nil || sess.BrowserID == "" {
		return
	}
	bb.Broadcast(sess.BrowserID, BrowserEvent{
		Type: "session_status",
		Data: map[string]any{
			"session_id": sessionID,
			"status":     string(sess.Status),
		},
	})
}
