package tui

import "sync"

// LiveSessionKey is a mutable holder for the run's session key (a run GUID).
// The engine-turn seam and the rail read it as a live value, so a later `/new`
// can re-mint the session and both surfaces observe the new key on the next
// turn boundary without any closure re-wiring.
type LiveSessionKey struct {
	mu  sync.RWMutex
	key string
}

// NewLiveSessionKey builds a live-session-key holder seeded with the initial key.
func NewLiveSessionKey(initial string) *LiveSessionKey {
	return &LiveSessionKey{key: initial}
}

// Get returns the current session key.
func (l *LiveSessionKey) Get() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.key
}

// Set replaces the current session key with a new one.
func (l *LiveSessionKey) Set(v string) {
	l.mu.Lock()
	l.key = v
	l.mu.Unlock()
}
