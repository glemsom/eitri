package tui

import (
	"context"
	"testing"
)

// A fresh session owns no live turn until Begin arms it; Stop before Begin is a no-op.
func TestTurnSessionStopBeforeBeginNoop(t *testing.T) {
	s := NewTurnSession()
	s.Stop() // must not panic
	if s.ctx != nil {
		t.Fatalf("expected no armed context, got %v", s.ctx)
	}
}

// Begin installs a fresh cancelable context; Stop cancels the armed turn.
func TestTurnSessionStopCancelsArmedTurn(t *testing.T) {
	s := NewTurnSession()
	s.Begin()
	ctx := s.Context()
	if ctx == nil || ctx == context.Background() {
		t.Fatalf("Begin should arm a cancelable context")
	}
	s.Stop()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop should cancel the armed context")
	}
}

// End disarms the session so a later Stop does not touch a finished turn's context.
func TestTurnSessionEndDisarms(t *testing.T) {
	s := NewTurnSession()
	s.Begin()
	ctx := s.Context()
	s.End()
	s.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("Stop after End must not cancel the finished turn's context")
	default:
	}
}

// The session owns the thinking-enabled flag for messages it creates.
func TestTurnSessionThinkingFlag(t *testing.T) {
	s := NewTurnSession()
	if s.thinkingEnabled {
		t.Fatal("thinking should default to disabled")
	}
	s.SetThinkingEnabled(true)
	if !s.thinkingEnabled {
		t.Fatal("SetThinkingEnabled(true) should stick")
	}
}
