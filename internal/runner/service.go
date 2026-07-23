// Package runner provides RunService — the seam for run lifecycle management.
// It owns agent loop execution, SSE broadcast, session persistence,
// and auth persistence callbacks.
package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/glemsom/eitri/internal/history"
	"github.com/glemsom/eitri/internal/debug"
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// BrowserEvent is an event sent to browser-level SSE subscribers.
// Used for broadcasting events that affect the entire browser UI
// (e.g., session status changes across all sessions).
type BrowserEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

// browserSubscriber represents one browser-level SSE subscriber.
type browserSubscriber struct {
	id   uint64
	ch   chan BrowserEvent
}

// RunState holds SSE broadcast state and cancel for one run.
type RunState struct {
	SessionID string
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	SSE   *runstate.State
	Turns int // turns consumed so far, updated by agent loop
}

func (rs *RunState) finish() {
	rs.doneOnce.Do(func() {
		close(rs.Done)
	})
}

// PersistAuthFunc is the callback type for persisting refreshed auth state.
type PersistAuthFunc = provider.PersistAuthFunc

type RunServiceDeps struct {
	UISessionMgr      *uisession.Manager
	HistorySessionMgr *history.SessionManager
	SkillsService     *skills.Service
	DebugRecorder     *debug.Recorder // optional HTTP trace recorder
	CrashDumpFunc     func(err error, stack []byte) // optional; called on fatal agent error
}
// RunService owns the run lifecycle: agent loop execution,
// SSE broadcast, session persistence, and auth refresh callbacks.
type RunService struct {
	mu              sync.Mutex
	active          map[string]*RunState
	confirmMu       sync.Mutex
	confirmations   map[string]chan ConfirmationResult // sessionID → confirmation channel
	uiSessionMgr    *uisession.Manager
	skillsSvc       *skills.Service
	historySessionMgr *history.SessionManager
	debugRecorder       *debug.Recorder
	persistAuth         PersistAuthFunc
	crashDumpFunc       func(err error, stack []byte)

	// Sub-agent tracking
	subMu        sync.Mutex
	subAgents    map[string]*subAgentRecord
	nextTaskID   uint64

	// Parent run configs per session (for sub-agent setup)
	parentCfgMu sync.Mutex
	parentCfgs  map[string]RunConfig

	// Browser-level SSE subscribers: browserID → subscribers
	browserMu        sync.Mutex
	browserSubs      map[string]map[uint64]chan BrowserEvent
	nextBrowserSubID uint64

	// Batch conversation context tracking (set by BatchRun, consumed by crash dump)
	batchCtxMu      sync.Mutex
	batchLastCtx    *debug.ConversationContext
}

const completedRunRetention = 5 * time.Second

// NewRunService creates a RunService with the given dependencies.
func NewRunService(deps RunServiceDeps) *RunService {
	return &RunService{
		active:        make(map[string]*RunState),
		confirmations: make(map[string]chan ConfirmationResult),
		uiSessionMgr:  deps.UISessionMgr,
		skillsSvc:     deps.SkillsService,
		historySessionMgr: deps.HistorySessionMgr,
		debugRecorder:     deps.DebugRecorder,
		crashDumpFunc:     deps.CrashDumpFunc,
		subAgents:     make(map[string]*subAgentRecord),
		parentCfgs:    make(map[string]RunConfig),
		browserSubs:   make(map[string]map[uint64]chan BrowserEvent),
	}
}

// SetSkillsService sets the skills service.
func (s *RunService) SetSkillsService(svc *skills.Service) {
	s.skillsSvc = svc
}

// SetUISessionManager sets the UI session manager.
func (s *RunService) SetUISessionManager(mgr *uisession.Manager) {
	s.uiSessionMgr = mgr
}

// SetPersistAuth sets the auth persistence callback.
func (s *RunService) SetPersistAuth(fn PersistAuthFunc) {
	s.persistAuth = fn
}

	// SetCrashDumpFunc sets the crash dump callback.
func (s *RunService) SetCrashDumpFunc(fn func(err error, stack []byte)) {
	s.crashDumpFunc = fn
}

// SubscribeBrowser registers a browser-level SSE subscriber for the given browserID.
// Returns subscriber ID and receive-only channel.
func (s *RunService) SubscribeBrowser(browserID string) (uint64, <-chan BrowserEvent) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()

	if s.browserSubs[browserID] == nil {
		s.browserSubs[browserID] = make(map[uint64]chan BrowserEvent)
	}

	id := s.nextBrowserSubID
	s.nextBrowserSubID++

	ch := make(chan BrowserEvent, 64)
	s.browserSubs[browserID][id] = ch
	return id, ch
}

// UnsubscribeBrowser removes a browser-level SSE subscriber.
func (s *RunService) UnsubscribeBrowser(browserID string, id uint64) {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()

	subs := s.browserSubs[browserID]
	if subs == nil {
		return
	}
	ch, ok := subs[id]
	if !ok {
		return
	}
	delete(subs, id)
	close(ch)
	if len(subs) == 0 {
		delete(s.browserSubs, browserID)
	}
}

// BroadcastToBrowser sends an event to all browser-level SSE subscribers for the given browserID.
func (s *RunService) BroadcastToBrowser(browserID string, evt BrowserEvent) {
	s.browserMu.Lock()
	subs := s.browserSubs[browserID]
	if subs == nil {
		s.browserMu.Unlock()
		return
	}
	// Collect channels under lock, send outside
	channels := make([]chan BrowserEvent, 0, len(subs))
	for _, ch := range subs {
		channels = append(channels, ch)
	}
	s.browserMu.Unlock()

	for _, ch := range channels {
		select {
		case ch <- evt:
		default:
			// Subscriber too slow; drop event
		}
	}
}

// BrowserSubscribersCount returns the number of browser-level subscribers for logging/diagnostics.
func (s *RunService) BrowserSubscribersCount(browserID string) int {
	s.browserMu.Lock()
	defer s.browserMu.Unlock()
	subs := s.browserSubs[browserID]
	if subs == nil {
		return 0
	}
	return len(subs)
}

// ActiveRun returns the active RunState for a session, or nil if none.
func (s *RunService) ActiveRun(sessionID string) *RunState {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	s.mu.Unlock()
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

// lookupRun returns the run state without checking if done.
func (s *RunService) lookupRun(sessionID string) *RunState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[sessionID]
}

// Subscribe attaches an SSE subscriber for an active run.
// Returns (subscriberID, channel, ok).
func (s *RunService) Subscribe(sessionID string) (uint64, <-chan runstate.SSEEvent, bool) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return 0, nil, false
	}
	return state.SSE.Subscribe()
}

// Unsubscribe removes an SSE subscriber.
func (s *RunService) Unsubscribe(sessionID string, id uint64) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.Unsubscribe(id)
}

// AppendEvent is no longer used. The agent loop owns SSE broadcast directly.
// Deprecated: kept for backward compatibility in tests; always returns "".
func (s *RunService) AppendEvent(state *RunState) string {
	return ""
}

// Cancel cancels the active run for a session.
func (s *RunService) Cancel(sessionID string) bool {
	s.mu.Lock()
	state, exists := s.active[sessionID]
	if exists {
		delete(s.active, sessionID)
	}
	s.mu.Unlock()

	// Close any pending confirmation channel
	s.confirmMu.Lock()
	if ch, ok := s.confirmations[sessionID]; ok {
		close(ch)
		delete(s.confirmations, sessionID)
	}
	s.confirmMu.Unlock()

	if !exists {
		return false
	}

	// Cancel any sub-agents spawned by this session
	s.CancelSubAgents(sessionID)

	slog.Info("run canceled", slog.String("session_id", sessionID))
	state.SSE.BroadcastDone("", nil)
	state.Cancel()
	state.finish()
	return true
}

// CancelAll cancels every active run.
func (s *RunService) CancelAll() {
	s.mu.Lock()
	states := make([]*RunState, 0, len(s.active))
	for sessionID, state := range s.active {
		delete(s.active, sessionID)
		states = append(states, state)
	}
	s.mu.Unlock()

	for _, state := range states {
		slog.Info("run canceled", slog.String("session_id", state.SessionID))
		state.Cancel()
		state.finish()
	}
}
// ActiveRunCount returns the number of active runs.
func (s *RunService) ActiveRunCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

// LastBatchConversationContext returns the conversation context from the last
// batch run, if any. Returns nil if no batch run has completed or if no
// conversation context was captured.
func (s *RunService) LastBatchConversationContext() *debug.ConversationContext {
	s.batchCtxMu.Lock()
	defer s.batchCtxMu.Unlock()
	return s.batchLastCtx
}

// setBatchConversationContext stores the conversation context for the last batch run.
func (s *RunService) setBatchConversationContext(ctx *debug.ConversationContext) {
	s.batchCtxMu.Lock()
	defer s.batchCtxMu.Unlock()
	s.batchLastCtx = ctx
}

// ActiveRunSSECounters returns subscriber and replay counts for all active runs.
// Returns map of sessionID → {subscriberCount, replayCount}.
func (s *RunService) ActiveRunSSECounters() map[string]struct{ SubscriberCount, ReplayCount uint64 } {
	s.mu.Lock()
	result := make(map[string]struct{ SubscriberCount, ReplayCount uint64 }, len(s.active))
	for sessionID, state := range s.active {
		select {
		case <-state.Done:
			// Skip finished-but-not-yet-cleaned-up runs
		default:
			result[sessionID] = struct{ SubscriberCount, ReplayCount uint64 }{
				SubscriberCount: state.SSE.SubscriberCount(),
				ReplayCount:     state.SSE.ReplayCount(),
			}
		}
	}
	s.mu.Unlock()
	return result
}

// CompletedRunRetentionMs returns the completed run retention duration in milliseconds.
func (s *RunService) CompletedRunRetentionMs() int64 {
	return completedRunRetention.Milliseconds()
}

// RunSSESnapshot holds SSE diagnostic counters and history for one active run,
// collected atomically under RunService.mu.
type RunSSESnapshot struct {
	SubscriberCount uint64
	ReplayCount     uint64
	History         []runstate.SSEEvent
	Busy            bool
	Turns           int
	PendingApproval bool
}

// ActiveRunSSESnapshot returns a snapshot of SSE counters, history, busy state,
// turn count, and pending confirmation for a session, collected atomically
// under RunService.mu. Returns nil if no active run.
func (s *RunService) ActiveRunSSESnapshot(sessionID string) *RunSSESnapshot {
	s.mu.Lock()

	state, exists := s.active[sessionID]
	if !exists {
		s.mu.Unlock()
		return nil
	}
	select {
	case <-state.Done:
		s.mu.Unlock()
		return nil
	default:
	}

	history := state.SSE.History()
	if len(history) > 50 {
		history = history[len(history)-50:]
	}

	turns := state.Turns
	busy := true
	s.mu.Unlock()

	pendingApproval := s.HasPendingConfirmation(sessionID)

	return &RunSSESnapshot{
		SubscriberCount: state.SSE.SubscriberCount(),
		ReplayCount:     state.SSE.ReplayCount(),
		History:         history,
		Busy:            busy,
		Turns:           turns,
		PendingApproval: pendingApproval,
	}
}

// HasPendingConfirmation returns true if there is a pending confirmation
// for the given session.
func (s *RunService) HasPendingConfirmation(sessionID string) bool {
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	_, ok := s.confirmations[sessionID]
	return ok
}



// CloseSession cancels the active run and closes the session.
func (s *RunService) CloseSession(sessionID string) error {
	s.Cancel(sessionID)
	if s.historySessionMgr != nil {
		s.historySessionMgr.Close(sessionID)
	}
	return nil
}

// NotifySessionClosed broadcasts a closed event for a session.
func (s *RunService) NotifySessionClosed(sessionID, message string) {
	state := s.lookupRun(sessionID)
	if state == nil {
		return
	}
	state.SSE.BroadcastClosed(message)
}

// NotifyAllStreamsClosed broadcasts a closed event to all active sessions.
func (s *RunService) NotifyAllStreamsClosed(message string) {
	s.mu.Lock()
	states := make([]*RunState, 0, len(s.active))
	for _, state := range s.active {
		states = append(states, state)
	}
	s.mu.Unlock()

	for _, state := range states {
		state.SSE.BroadcastClosed(message)
	}
}

// confirmPath implements ConfirmationFunc for RunAgent.
// It creates a channel for the session, sends a needs_confirmation SSE event,
// and blocks waiting for the user's response via the API endpoint.
func (s *RunService) confirmPath(ctx context.Context, sessionID, path, message string) (*ConfirmationResult, error) {
	s.confirmMu.Lock()
	// Check if channel already exists (should not happen in normal flow)
	if existing, ok := s.confirmations[sessionID]; ok {
		close(existing)
	}
	ch := make(chan ConfirmationResult, 1)
	s.confirmations[sessionID] = ch
	s.confirmMu.Unlock()

	// Clean up when done
	defer func() {
		s.confirmMu.Lock()
		delete(s.confirmations, sessionID)
		s.confirmMu.Unlock()
	}()

	select {
	case result := <-ch:
		return &result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ResolveConfirmation resolves a pending confirmation for a session.
// Called by the API endpoint when the user allows or denies a path.
func (s *RunService) ResolveConfirmation(sessionID, path string, approved bool) bool {
	s.confirmMu.Lock()
	ch, ok := s.confirmations[sessionID]
	s.confirmMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- ConfirmationResult{Path: path, Approved: approved}:
		return true
	default:
		return false
	}
}
