package tui

import (
	"errors"
	"testing"
)

func TestRunCommit_successStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	tx.curStream = 0

	stopped, err := projectDone(&tx, turnDoneMsg{answer: "final answer", reasoning: "reasoned"})
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}
	msg := tx.messages[0]
	if msg.content != "final answer" || msg.reasoning != "reasoned" {
		t.Errorf("content=%q reasoning=%q", msg.content, msg.reasoning)
	}
	if msg.streaming || tx.curStream != -1 || tx.busy {
		t.Error("streaming flag, cursor, and busy should clear")
	}
}

func TestRunCommit_successNoStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	stopped, err := projectDone(&tx, turnDoneMsg{answer: "the answer"})
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].content != "the answer" {
		t.Fatalf("messages = %+v", tx.messages)
	}
}

func TestRunCommit_stoppedStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	tx.curStream = 0

	stopped, err := projectDone(&tx, turnDoneMsg{stopped: true, answer: "final-partial", reasoning: "thought"})
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	msg := tx.messages[0]
	if msg.content != "final-partial" || msg.reasoning != "thought" || msg.streaming || !msg.stopped {
		t.Errorf("message = %+v", msg)
	}
}

func TestRunCommit_stoppedNoStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	stopped, err := projectDone(&tx, turnDoneMsg{stopped: true, answer: "partial"})
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].content != "partial" || !tx.messages[1].stopped {
		t.Fatalf("messages = %+v", tx.messages)
	}
}

func TestRunCommit_stoppedStreamingFallsBackToLivePartialWhenFinalEmpty(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	projectStream(&tx, AnswerStream, "partial")

	stopped, err := projectDone(&tx, turnDoneMsg{stopped: true})
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	msg := tx.messages[len(tx.messages)-1]
	if msg.content != "partial" || !msg.stopped {
		t.Fatalf("message = %+v, want stopped live partial", msg)
	}
}

func TestRunCommit_errorAppendsFailureMessage(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	beginTurn(s, &tx, "q", "")

	stopped, err := projectDone(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped || err == nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].role != "eitri" {
		t.Fatalf("messages = %+v", tx.messages)
	}
	if tx.curStream != -1 {
		t.Errorf("curStream = %d, want -1", tx.curStream)
	}
}

func TestRunCommit_fullCycleThroughVerbsAlone(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("final", nil))
	tx := newTestTx()

	cmd := beginTurn(s, &tx, "go", "")
	if cmd == nil || !tx.busy {
		t.Fatal("start projection should mark the transcript busy")
	}
	projectStream(&tx, AnswerStream, "par")
	projectStream(&tx, AnswerStream, "tial")

	tdm := cmd().(turnDoneMsg)
	stopped, err := projectDone(&tx, tdm)
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if len(tx.messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(tx.messages))
	}
	msg := tx.messages[1]
	if msg.content != "final" || msg.streaming || tx.busy {
		t.Errorf("message = %+v busy = %v", msg, tx.busy)
	}
	if len(msg.events) == 0 {
		t.Error("committed message should carry the turn's event log")
	}
}
