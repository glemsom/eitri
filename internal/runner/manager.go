// Package runner manages ADK runner lifecycle and caching.
package runner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"

	"github.com/glemsom/eitri/internal/config"
)

// Manager caches ADK runners keyed by config hash.
type Manager struct {
	mu          sync.RWMutex
	runners     map[string]*cachedRunner
	sessionSvc  session.Service
}

type cachedRunner struct {
	runner *runner.Runner
	hash   string
}

// NewManager creates a runner manager.
func NewManager() *Manager {
	return &Manager{
		runners:    make(map[string]*cachedRunner),
		sessionSvc: session.InMemoryService(),
	}
}

// configHash computes a hash of config fields that affect the runner.
func configHash(cfg *config.Config) string {
	h := sha256.New()
	h.Write([]byte(cfg.Provider))
	h.Write([]byte(cfg.APIKey))
	h.Write([]byte(cfg.BaseURL))
	h.Write([]byte(cfg.Model))
	return fmt.Sprintf("%x", h.Sum(nil))
}

// GetOrCreate returns a cached ADK runner for the given config and agent.
func (m *Manager) GetOrCreate(cfg *config.Config, ag agent.Agent) (*runner.Runner, error) {
	hash := configHash(cfg)

	m.mu.RLock()
	cr, exists := m.runners[hash]
	m.mu.RUnlock()

	if exists && cr.runner != nil {
		return cr.runner, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if cr, exists := m.runners[hash]; exists && cr.runner != nil {
		return cr.runner, nil
	}

	r, err := runner.New(runner.Config{
		AppName:           "eitri",
		Agent:             ag,
		SessionService:    m.sessionSvc,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	m.runners[hash] = &cachedRunner{runner: r, hash: hash}
	log.Printf("Runner created for config hash %s (model=%s)", hash[:12], cfg.Model)

	return r, nil
}

// Invalidate clears all cached runners.
func (m *Manager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runners = make(map[string]*cachedRunner)
	log.Printf("All runners invalidated")
}

// SessionService returns the shared session service.
func (m *Manager) SessionService() session.Service {
	return m.sessionSvc
}

// Run runs the ADK agent and delivers events via channels.
// Returns event channel, error channel, cancel func.
func (m *Manager) Run(ctx context.Context, r *runner.Runner, userID, sessionID string, msg *genai.Content) (<-chan *session.Event, <-chan error, context.CancelFunc) {
	runCtx, cancel := context.WithCancel(ctx)

	eventCh := make(chan *session.Event, 200)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(errCh)

		seq := r.Run(runCtx, userID, sessionID, msg, agent.RunConfig{},
			runner.WithStateDelta(map[string]any{"eitri_session": sessionID}),
		)

		for evt, err := range seq {
			if err != nil {
				select {
				case errCh <- err:
				case <-runCtx.Done():
				}
				return
			}
			if evt != nil {
				select {
				case eventCh <- evt:
				case <-runCtx.Done():
					return
				}
			}
		}
	}()

	return eventCh, errCh, cancel
}
