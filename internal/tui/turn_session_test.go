package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// A fresh session owns no live execution until Dispatch arms it; Stop before Dispatch is a no-op.
func TestTurnSessionStopBeforeDispatchNoop(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	s.Stop() // must not panic
	if s.ctx != nil {
		t.Fatalf("expected no armed context, got %v", s.ctx)
	}
}

// Dispatch installs a fresh cancelable context and its command cancels it when the turn completes.
func TestTurnSessionDispatchArmsCancelableContext(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()
	cmd := beginTurn(s, &tx, "hello", "")
	if s.ctx == nil || s.ctx == context.Background() {
		t.Fatalf("Dispatch should arm a cancelable context")
	}
	if cmd == nil {
		t.Fatal("Dispatch should return the execution command")
	}
	msg := cmd()
	tdm := msg.(turnDoneMsg)
	select {
	case <-s.ctx.Done():
	default:
		t.Fatal("the dispatch command should cancel the armed context when the turn completes")
	}
	if tdm.answer != "ok" {
		t.Errorf("answer = %q, want %q", tdm.answer, "ok")
	}
}

// Stop cancels the armed turn so an in-flight provider stream dies.
func TestTurnSessionStopCancelsArmedTurn(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "hello", "")
	ctx := s.Context()
	s.Stop()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Stop should cancel the armed context")
	}
}

// End disarms the session so a later Stop does not touch a finished turn's context.
func TestTurnSessionEndDisarms(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "hello", "")
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
	s := NewTurnSession(stubTurn("ok", nil))
	if s.thinkingEnabled {
		t.Fatal("thinking should default to disabled")
	}
	s.SetThinkingEnabled(true)
	if !s.thinkingEnabled {
		t.Fatal("SetThinkingEnabled(true) should stick")
	}
}

// Start projection appends the user message to the transcript.
func TestTranscriptProjectStartAppendsUserMessage(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()

	beginTurn(s, &tx, "hello", "")

	if len(tx.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(tx.messages))
	}
	if tx.messages[0].role != "you" {
		t.Errorf("message role = %q, want %q", tx.messages[0].role, "you")
	}
	if tx.messages[0].content != "hello" {
		t.Errorf("message content = %q, want %q", tx.messages[0].content, "hello")
	}
}

// Start projection marks the transcript busy and lays it out dirty.
func TestTranscriptProjectStartSetsBusyAndDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()

	beginTurn(s, &tx, "hello", "")

	if !tx.busy {
		t.Error("start projection did not set busy")
	}
	if !tx.layout.dirty {
		t.Error("start projection did not mark layout dirty")
	}
}

// Start projection resets the live-turn state: stream cursor, per-turn timeline, and arrival counter.
func TestTranscriptProjectStartResetsLiveTurnState(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()
	tx.flow.ObserveTool(TimelineEvent{Kind: EventAnswer, Delta: "stale"})

	beginTurn(s, &tx, "hello", "")

	if tx.curStream != -1 {
		t.Errorf("stream cursor = %d, want -1", tx.curStream)
	}
	if tx.LiveTimeline() != nil {
		t.Error("start projection did not reset the per-turn timeline")
	}
}

// Start projection anchors the tool log to the newly appended user message.
func TestTranscriptProjectStartSetsAnchor(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()

	beginTurn(s, &tx, "hello", "")

	if tx.log.curAnchor != len(tx.messages)-1 {
		t.Errorf("tool log anchor = %d, want %d", tx.log.curAnchor, len(tx.messages)-1)
	}
}

// A turn whose context dies comes back through the dispatch command as a stopped turn with partial output preserved.
func TestTurnSessionStoppedTurnKeepsPartialOutput(t *testing.T) {
	s := NewTurnSession(func(_ context.Context, _, _ string) (TurnResult, error) {
		return TurnResult{Answer: "partial"}, context.Canceled
	})
	tx := newTestTx()
	cmd := beginTurn(s, &tx, "hi", "")

	s.Stop()
	msg := cmd()
	tdm := msg.(turnDoneMsg)
	if !tdm.stopped {
		t.Error("canceled turn should report stopped")
	}
	if tdm.answer != "partial" {
		t.Errorf("partial answer = %q, want %q", tdm.answer, "partial")
	}
}

// A failed turn comes back through the dispatch command carrying the error.
func TestTurnSessionFailedTurnCarriesError(t *testing.T) {
	s := NewTurnSession(stubTurn("", errors.New("boom")))
	tx := newTestTx()
	cmd := beginTurn(s, &tx, "hi", "")

	msg := cmd()
	tdm := msg.(turnDoneMsg)
	if tdm.err == nil || tdm.err.Error() != "boom" {
		t.Errorf("err = %v, want boom", tdm.err)
	}
}

func newTestTx() Transcript {
	th := themeFor(config.DefaultTheme)
	return Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		layout:      transcriptLayout{dirty: true},
		log:         toolLog{},
	}
}

func stubTurn(answer string, err error) func(context.Context, string, string) (TurnResult, error) {
	return func(_ context.Context, _ string, _ string) (TurnResult, error) {
		return TurnResult{Answer: answer}, err
	}
}
