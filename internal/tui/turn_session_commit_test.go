package tui

import (
	"errors"
	"testing"
)

func TestCommit_successStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	s.Begin(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	s.curStream = 0

	stopped, err := s.Commit(&tx, turnDoneMsg{answer: "final answer", reasoning: "reasoned"})
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}
	msg := tx.messages[0]
	if msg.content != "final answer" || msg.reasoning != "reasoned" {
		t.Errorf("content=%q reasoning=%q", msg.content, msg.reasoning)
	}
	if msg.streaming || s.curStream != -1 || tx.busy {
		t.Error("streaming flag, cursor, and busy should clear")
	}
}

func TestCommit_successNoStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	s.Begin(&tx, "q", "")

	stopped, err := s.Commit(&tx, turnDoneMsg{answer: "the answer"})
	if stopped || err != nil {
		t.Fatalf("stopped=%v err=%v, want false/nil", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].content != "the answer" {
		t.Fatalf("messages = %+v", tx.messages)
	}
}

func TestCommit_stoppedStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	s.Begin(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	s.curStream = 0

	stopped, err := s.Commit(&tx, turnDoneMsg{stopped: true, answer: "final-partial", reasoning: "thought"})
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	msg := tx.messages[0]
	if msg.content != "final-partial" || msg.reasoning != "thought" || msg.streaming || !msg.stopped {
		t.Errorf("message = %+v", msg)
	}
}

func TestCommit_stoppedNoStreaming(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	s.Begin(&tx, "q", "")

	stopped, err := s.Commit(&tx, turnDoneMsg{stopped: true, answer: "partial"})
	if !stopped || err != nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].content != "partial" || !tx.messages[1].stopped {
		t.Fatalf("messages = %+v", tx.messages)
	}
}

func TestCommit_errorAppendsFailureMessage(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("", nil))
	tx := newTestTx()
	s.Begin(&tx, "q", "")

	stopped, err := s.Commit(&tx, turnDoneMsg{err: errors.New("provider failed")})
	if stopped || err == nil {
		t.Fatalf("stopped=%v err=%v", stopped, err)
	}
	if len(tx.messages) != 2 || tx.messages[1].role != "eitri" {
		t.Fatalf("messages = %+v", tx.messages)
	}
	if s.curStream != -1 {
		t.Errorf("curStream = %d, want -1", s.curStream)
	}
}

func TestCommit_fullCycleThroughVerbsAlone(t *testing.T) {
	t.Parallel()
	s := NewTurnSession(stubTurn("final", nil))
	f := NewFold(s)
	tx := newTestTx()

	cmd := s.Begin(&tx, "go", "")
	if cmd == nil || !tx.busy {
		t.Fatal("Begin should arm the turn and set busy")
	}
	f.Stream(&tx, AnswerStream, "par")
	f.Stream(&tx, AnswerStream, "tial")

	tdm := cmd().(turnDoneMsg)
	stopped, err := s.Commit(&tx, tdm)
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
