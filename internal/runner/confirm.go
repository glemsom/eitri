package runner

import (
	"context"
)

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
