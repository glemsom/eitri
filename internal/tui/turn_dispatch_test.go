package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

func newTestTx() Transcript {
	th := themeFor(config.DefaultTheme)
	return Transcript{
		theme:       th,
		configTheme: config.DefaultTheme,
		layout:      transcriptLayout{dirty: true},
		log:         toolLog{},
	}
}

func stubTurn(answer string, err error) Turn {
	return func(_ context.Context, _ string, _ string) (TurnResult, error) {
		return TurnResult{Answer: answer}, err
	}
}

func TestTurnDispatch_appendStreamDelta_createsMessageOnFirstDelta(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()

	d.appendStreamDelta(&tx, AnswerStream, "hello")

	if len(tx.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(tx.messages))
	}
	msg := tx.messages[0]
	if msg.role != "eitri" {
		t.Errorf("role = %q, want %q", msg.role, "eitri")
	}
	if msg.content != "hello" {
		t.Errorf("content = %q, want %q", msg.content, "hello")
	}
	if !msg.streaming {
		t.Error("message should be streaming")
	}
	if d.session.curStream != 0 {
		t.Errorf("curStream = %d, want 0", d.session.curStream)
	}
}

func TestTurnDispatch_appendStreamDelta_extendsExistingMessage(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()

	d.appendStreamDelta(&tx, AnswerStream, "hel")
	d.appendStreamDelta(&tx, AnswerStream, "lo")

	if len(tx.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(tx.messages))
	}
	if tx.messages[0].content != "hello" {
		t.Errorf("content = %q, want %q", tx.messages[0].content, "hello")
	}
}

func TestTurnDispatch_appendStreamDelta_reasoningSeparate(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()

	d.appendStreamDelta(&tx, ReasoningStream, "thinking...")
	d.appendStreamDelta(&tx, AnswerStream, "answer")

	if len(tx.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(tx.messages))
	}
	if tx.messages[0].reasoning != "thinking..." {
		t.Errorf("reasoning = %q, want %q", tx.messages[0].reasoning, "thinking...")
	}
	if tx.messages[0].content != "answer" {
		t.Errorf("content = %q, want %q", tx.messages[0].content, "answer")
	}
}

func TestTurnDispatch_appendStreamDelta_emptyNoop(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()

	d.appendStreamDelta(&tx, AnswerStream, "")

	if len(tx.messages) != 0 {
		t.Fatalf("expected 0 messages, got %d", len(tx.messages))
	}
}

func TestTurnDispatch_appendStreamDelta_setsBusyPulseOnFirstDelta(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()

	d.appendStreamDelta(&tx, AnswerStream, "hello")

	if tx.busyPulse != 3 {
		t.Errorf("busyPulse = %d, want 3", tx.busyPulse)
	}
}

func TestTurnDispatch_appendStreamDelta_doesNotResetBusyPulseOnSubsequentDelta(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()

	d.appendStreamDelta(&tx, AnswerStream, "hel")
	tx.busyPulse = 1 // simulate mid-pulse
	d.appendStreamDelta(&tx, AnswerStream, "lo")

	if tx.busyPulse != 1 {
		t.Errorf("busyPulse = %d, want 1 (should not reset on subsequent delta)", tx.busyPulse)
	}
}

func TestTurnDispatch_handleTurnDone_stoppedStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	d.session.curStream = 0

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{
		stopped:   true,
		answer:    "final-partial",
		reasoning: "thought",
	})
	if !stopped {
		t.Error("expected stopped=true")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	msg := tx.messages[0]
	if msg.content != "final-partial" {
		t.Errorf("content = %q, want %q", msg.content, "final-partial")
	}
	if msg.reasoning != "thought" {
		t.Errorf("reasoning = %q, want %q", msg.reasoning, "thought")
	}
	if msg.streaming {
		t.Error("message should not be streaming after stop")
	}
	if !msg.stopped {
		t.Error("message should be marked stopped")
	}
	if d.session.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.session.curStream)
	}
	if tx.busy {
		t.Error("busy should be false after turn done")
	}
}

func TestTurnDispatch_handleTurnDone_stoppedNoStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "q", "")

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{
		stopped: true,
		answer:  "partial",
	})
	if !stopped {
		t.Error("expected stopped=true")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if len(tx.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(tx.messages))
	}
	msg := tx.messages[1]
	if msg.content != "partial" {
		t.Errorf("content = %q, want %q", msg.content, "partial")
	}
	if !msg.stopped {
		t.Error("message should be marked stopped")
	}
}

func TestTurnDispatch_handleTurnDone_error(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "q", "")

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{
		err: errors.New("provider failed"),
	})
	if stopped {
		t.Error("expected stopped=false on error")
	}
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if len(tx.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(tx.messages))
	}
	if tx.messages[1].role != "eitri" {
		t.Errorf("role = %q, want %q", tx.messages[1].role, "eitri")
	}
	if d.session.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.session.curStream)
	}
}

func TestTurnDispatch_handleTurnDone_successStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	d.session.curStream = 0

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{
		answer:    "final answer",
		reasoning: "reasoned",
	})
	if stopped {
		t.Error("expected stopped=false")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	msg := tx.messages[0]
	if msg.content != "final answer" {
		t.Errorf("content = %q, want %q", msg.content, "final answer")
	}
	if msg.reasoning != "reasoned" {
		t.Errorf("reasoning = %q, want %q", msg.reasoning, "reasoned")
	}
	if msg.streaming {
		t.Error("message should not be streaming")
	}
	if d.session.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.session.curStream)
	}
	if tx.busy {
		t.Error("busy should be false")
	}
}

func TestTurnDispatch_handleTurnDone_successNoStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.session.Begin(&tx, "q", "")

	stopped, err := d.handleTurnDone(&tx, turnDoneMsg{
		answer: "the answer",
	})
	if stopped {
		t.Error("expected stopped=false")
	}
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if len(tx.messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(tx.messages))
	}
	msg := tx.messages[1]
	if msg.content != "the answer" {
		t.Errorf("content = %q, want %q", msg.content, "the answer")
	}
	if msg.role != "eitri" {
		t.Errorf("role = %q, want %q", msg.role, "eitri")
	}
}

func TestTurnDispatch_fullCycle(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("final", nil))
	tx := newTestTx()

	cmd := d.session.Begin(&tx, "go", "")
	if cmd == nil {
		t.Fatal("Begin returned nil command")
	}
	if !tx.busy {
		t.Fatal("should be busy")
	}

	d.appendStreamDelta(&tx, AnswerStream, "par")
	d.appendStreamDelta(&tx, AnswerStream, "tial")

	msg := cmd()
	tdm := msg.(turnDoneMsg)
	stopped, err := d.handleTurnDone(&tx, tdm)
	if stopped {
		t.Error("not stopped")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tx.messages[1].content != "final" {
		t.Errorf("final content = %q, want %q", tx.messages[1].content, "final")
	}
	if tx.messages[1].streaming {
		t.Error("should not be streaming")
	}
	if tx.busy {
		t.Error("should not be busy")
	}
}
