package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// A fresh session owns no live turn until Begin arms it; Stop before Begin is a no-op.
func TestTurnSessionStopBeforeBeginNoop(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	s.Stop() // must not panic
	if s.ctx != nil {
		t.Fatalf("expected no armed context, got %v", s.ctx)
	}
}

// Begin installs a fresh cancelable context and Begin's command cancels it when the turn completes.
func TestTurnSessionBeginArmsCancelableContext(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()
	cmd := s.Begin(&tx, "hello", "")
	if s.ctx == nil || s.ctx == context.Background() {
		t.Fatalf("Begin should arm a cancelable context")
	}
	if cmd == nil {
		t.Fatal("Begin should return the dispatch command")
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
	s.Begin(&tx, "hello", "")
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
	s.Begin(&tx, "hello", "")
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

// Prompt submission appends the user message to the transcript.
func TestTurnSessionBeginAppendsUserMessage(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()

	s.Begin(&tx, "hello", "")

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

// Prompt submission marks the transcript busy and lays it out dirty.
func TestTurnSessionBeginSetsBusyAndDirty(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()

	s.Begin(&tx, "hello", "")

	if !tx.busy {
		t.Error("Begin did not set busy")
	}
	if !tx.layout.dirty {
		t.Error("Begin did not mark layout dirty")
	}
}

// Prompt submission resets the live-turn state: stream cursor, per-turn timeline, and arrival counter.
func TestTurnSessionBeginResetsLiveTurnState(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()
	s.flow.ObserveTool(TimelineEvent{Kind: EventAnswer, Delta: "stale"})

	s.Begin(&tx, "hello", "")

	if s.curStream != -1 {
		t.Errorf("stream cursor = %d, want -1", s.curStream)
	}
	if s.LiveTimeline() != nil {
		t.Error("Begin did not reset the per-turn timeline")
	}
}

// Prompt submission anchors the tool log to the newly appended user message.
func TestTurnSessionBeginSetsAnchor(t *testing.T) {
	s := NewTurnSession(stubTurn("ok", nil))
	tx := newTestTx()

	s.Begin(&tx, "hello", "")

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
	cmd := s.Begin(&tx, "hi", "")

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
	cmd := s.Begin(&tx, "hi", "")

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
