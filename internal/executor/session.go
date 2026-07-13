package executor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type managedSession struct {
	executor   CommandExecutor
	lastAccess time.Time
}

// SessionManager manages per-session CommandExecutor instances with idle timeout.
type SessionManager struct {
	workspace   string
	cmdTimeout  time.Duration
	idleTimeout time.Duration

	mu       sync.Mutex
	sessions map[string]*managedSession
	nextID   int
}

// NewSessionManager creates a new SessionManager.
// Workspace returns the initial working directory for executors.
func (sm *SessionManager) Workspace() string {
	return sm.workspace
}

// workspace is the initial working directory for new executors.
// cmdTimeout is the per-command timeout for new executors.
// idleTimeout is how long an executor can be idle before being closed (0 = no timeout).
func NewSessionManager(workspace string, cmdTimeout time.Duration, idleTimeout time.Duration) *SessionManager {
	return &SessionManager{
		workspace:   workspace,
		cmdTimeout:  cmdTimeout,
		idleTimeout: idleTimeout,
		sessions:    make(map[string]*managedSession),
	}
}

// GetOrCreate returns the executor for sessionID, creating one if needed.
func (sm *SessionManager) GetOrCreate(sessionID string) (CommandExecutor, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if ms, ok := sm.sessions[sessionID]; ok {
		ms.lastAccess = time.Now()
		return ms.executor, nil
	}

	// Generate unique tmux session name
	sm.nextID++
	tmuxSessionName := fmt.Sprintf("eitri-%s-%d", sessionID, sm.nextID)

	exe, err := NewTmuxExecutor(tmuxSessionName, sm.workspace, sm.cmdTimeout)
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	sm.sessions[sessionID] = &managedSession{
		executor:   exe,
		lastAccess: time.Now(),
	}
	return exe, nil
}

// Close closes the executor for the given sessionID and removes it.
func (sm *SessionManager) Close(sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ms, ok := sm.sessions[sessionID]
	if !ok {
		return nil
	}
	delete(sm.sessions, sessionID)
	return ms.executor.Close()
}

// CloseAll closes all managed executors.
func (sm *SessionManager) CloseAll() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	for id, ms := range sm.sessions {
		ms.executor.Close()
		delete(sm.sessions, id)
	}
}

// StartTimeoutLoop starts a background goroutine that periodically checks for idle sessions.
// Runs every tick interval until ctx is done.
func (sm *SessionManager) StartTimeoutLoop(ctx context.Context, tick time.Duration) {
	if sm.idleTimeout <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sm.closeIdleSessions()
			}
		}
	}()
}

// closeIdleSessions closes sessions that have been idle longer than idleTimeout.
func (sm *SessionManager) closeIdleSessions() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for id, ms := range sm.sessions {
		if now.Sub(ms.lastAccess) > sm.idleTimeout {
			ms.executor.Close()
			delete(sm.sessions, id)
		}
	}
}
