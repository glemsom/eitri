package tui

import (
	"testing"
)

// TestTurnSessionFullTurnContract drives one complete turn — submit → stream →
// tool calls → complete — through Begin/Fold/Commit alone. No Bubble Tea loop,
// no direct transcript writes: the contract that the four verbs are the only
// way a turn's life touches the transcript (issue #528).
func TestTurnSessionFullTurnContract(t *testing.T) {
	s := NewTurnSession(stubTurn("final answer", nil))
	tx := newTestTx()
	f := NewFold(s)

	cmd := s.Begin(&tx, "do the thing", "")
	if !tx.busy {
		t.Fatal("Begin did not arm the turn: transcript not busy")
	}

	// The turn streams reasoning and answer text, runs one tool, then answers.
	f.Stream(&tx, ReasoningStream, "thinking…")
	f.Tool(&tx, ToolUpdate{Start: &ToolStart{Name: "bash", Args: "ls"}})
	f.Stream(&tx, AnswerStream, "final ")
	f.Stream(&tx, AnswerStream, "answer")
	f.Tool(&tx, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "file.go"}})

	if len(s.timeline) != 5 {
		t.Fatalf("live event log = %d events, want 5", len(s.timeline))
	}
	for i, ev := range s.timeline {
		if ev.Seq != i {
			t.Errorf("event %d seq = %d, want %d (arrival order)", i, ev.Seq, i)
		}
	}

	msg := cmd().(turnDoneMsg)
	stopped, err := s.Commit(&tx, msg)
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}

	// The committed assistant message owns the full arrival-ordered log; the
	// live log resets for the next turn.
	asst := tx.messages[len(tx.messages)-1]
	if asst.content != "final answer" {
		t.Errorf("committed content = %q, want %q", asst.content, "final answer")
	}
	if len(asst.events) != 5 {
		t.Errorf("committed event log = %d events, want 5", len(asst.events))
	}
	if asst.streaming {
		t.Error("committed assistant message still marked streaming")
	}
	if s.timeline != nil || s.turnSeq != 0 || tx.busy || s.curStream != -1 {
		t.Error("session did not reset live state on commit")
	}
}

// TestTranscriptHasNoLiveTurnFields guards ownership: the live timeline and
// sequence counter live on the TurnSession, not on the Transcript.
func TestTranscriptHasNoLiveTurnFields(t *testing.T) {
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	f := NewFold(s)

	s.Begin(&tx, "q", "")
	f.Stream(&tx, AnswerStream, "hello")

	// Reading through the transcript goes via the wired session accessor.
	if got := tx.LiveTimeline(); len(got) != 1 || got[0].Delta != "hello" {
		t.Fatalf("LiveTimeline = %+v, want one hello delta", got)
	}
	// A bare transcript with no wired session reads empty, not panicked.
	var bare Transcript
	if bare.LiveTimeline() != nil {
		t.Error("bare transcript LiveTimeline should be nil")
	}
}
