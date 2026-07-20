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
	"github.com/glemsom/eitri/internal/provider"
	"github.com/glemsom/eitri/internal/runstate"
	uisession "github.com/glemsom/eitri/internal/session"
	"github.com/glemsom/eitri/internal/skills"
)

// RunState holds SSE broadcast state and cancel for one run.
type RunState struct {
	SessionID string
	Cancel    context.CancelFunc
	StartedAt time.Time
	Done      chan struct{}
	doneOnce  sync.Once

	SSE *runstate.State
}

func (rs *RunState) finish() {
	rs.doneOnce.Do(func() {
		close(rs.Done)
	})
}

// PersistAuthFunc is the callback type for persisting refreshed auth state.
type PersistAuthFunc = provider.PersistAuthFunc

// RunServiceDeps holds the dependencies for RunService.
type RunServiceDeps struct {
	UISessionMgr      *uisession.Manager
	HistorySessionMgr *history.SessionManager
	SkillsService     *skills.Service
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
	persistAuth         PersistAuthFunc
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
	slog.Info("run canceled", slog.String("session_id", sessionID))
	state.SSE.Broadcast(runstate.SSEEvent{Type: "done", Kind: runstate.RenderKindMarkdown})
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
