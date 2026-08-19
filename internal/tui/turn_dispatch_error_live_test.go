package tui

import (
	"errors"
	"testing"
)

// TestTurnDispatch_handleTurnDone_errorClearsLiveStream asserts that an error
// ending a still-streaming turn un-marks the partial message so a live
// reasoning panel (issue #364) is not left auto-expanded once the flawed turn
// has ended: the partial reply keeps its content but stops streaming.
func TestTurnDispatch_handleTurnDone_errorClearsLiveStream(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.startTurn(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", reasoning: "partial thought", streaming: true})
	d.curStream = 1

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped {
		t.Error("expected stopped=false on error")
	}
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if tx.messages[1].streaming {
		t.Error("an errored partial message must stop streaming so the live reasoning panel collapses")
	}
	if tx.messages[1].reasoning != "partial thought" {
		t.Errorf("partial reasoning should be preserved, got %q", tx.messages[1].reasoning)
	}
}
