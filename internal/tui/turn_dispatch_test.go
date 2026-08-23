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

func TestTurnDispatch_startTurn_installsContext(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	tx := newTestTx()

	cmd := d.startTurn(&tx, "hello", "")
	if cmd == nil {
		t.Fatal("startTurn returned nil command")
	}
	if d.session.Context() == nil {
		t.Fatal("startTurn did not install non-nil context")
	}
	if d.session.cancel == nil {
		t.Fatal("startTurn did not install non-nil cancel func")
	}
}

func TestTurnDispatch_startTurn_appendsUserMessage(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	tx := newTestTx()

	d.startTurn(&tx, "hello", "")

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

func TestTurnDispatch_startTurn_setsBusyAndDirty(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	tx := newTestTx()

	d.startTurn(&tx, "hello", "")

	if !tx.busy {
		t.Error("startTurn did not set busy")
	}
	if !tx.layout.dirty {
		t.Error("startTurn did not mark layout dirty")
	}
}

func TestTurnDispatch_startTurn_resetsCurStream(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	tx := newTestTx()
	d.curStream = 5 // pre-set to non-default

	d.startTurn(&tx, "hello", "")

	if d.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.curStream)
	}
}

func TestTurnDispatch_startTurn_setsAnchor(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	tx := newTestTx()

	d.startTurn(&tx, "hello", "")

	if tx.log.curAnchor != 0 {
		t.Errorf("tool log anchor = %d, want 0", tx.log.curAnchor)
	}
}

func TestTurnDispatch_stopTurn_cancelsContext(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	tx := newTestTx()
	d.startTurn(&tx, "hello", "")

	if d.session.Context().Err() != nil {
		t.Fatal("context already cancelled before stop")
	}

	d.stopTurn()

	if d.session.Context().Err() == nil {
		t.Error("stopTurn did not cancel the context")
	}
}

func TestTurnDispatch_stopTurn_noopWhenNil(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("ok", nil))
	d.stopTurn() // must not panic
}

func TestTurnDispatch_turnCmd_returnsTurnDoneMsg(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("answer", nil))
	tx := newTestTx()
	d.startTurn(&tx, "prompt", "")

	cmd := d.turnCmd("prompt", "")
	if cmd == nil {
		t.Fatal("turnCmd returned nil")
	}
	msg := cmd()
	tdm, ok := msg.(turnDoneMsg)
	if !ok {
		t.Fatalf("expected turnDoneMsg, got %T", msg)
	}
	if tdm.answer != "answer" {
		t.Errorf("answer = %q, want %q", tdm.answer, "answer")
	}
}

func TestTurnDispatch_turnCmd_contextCanceledMapsToStopped(t *testing.T) {
	t.Parallel()
	turnFn := func(ctx context.Context, _ string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "partial"}, context.Canceled
	}
	d := NewTurnDispatch(turnFn)
	tx := newTestTx()
	d.startTurn(&tx, "hi", "")

	msg := d.turnCmd("hi", "")()
	tdm := msg.(turnDoneMsg)
	if !tdm.stopped {
		t.Error("context.Canceled should map to stopped")
	}
	if tdm.answer != "partial" {
		t.Errorf("answer = %q, want %q", tdm.answer, "partial")
	}
}

func TestTurnDispatch_turnCmd_errorReturnsErr(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", errors.New("boom")))
	tx := newTestTx()
	d.startTurn(&tx, "hi", "")

	msg := d.turnCmd("hi", "")()
	tdm := msg.(turnDoneMsg)
	if tdm.err == nil {
		t.Fatal("expected non-nil error")
	}
	if tdm.err.Error() != "boom" {
		t.Errorf("err = %q, want %q", tdm.err.Error(), "boom")
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
	if d.curStream != 0 {
		t.Errorf("curStream = %d, want 0", d.curStream)
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
	d.startTurn(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	d.curStream = 0

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
	if d.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.curStream)
	}
	if tx.busy {
		t.Error("busy should be false after turn done")
	}
}

func TestTurnDispatch_handleTurnDone_stoppedNoStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.startTurn(&tx, "q", "")

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
	d.startTurn(&tx, "q", "")

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
	if d.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.curStream)
	}
}

func TestTurnDispatch_handleTurnDone_successStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.startTurn(&tx, "q", "")

	tx.messages = append(tx.messages, message{role: "eitri", content: "partial", streaming: true})
	d.curStream = 0

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
	if d.curStream != -1 {
		t.Errorf("curStream = %d, want -1", d.curStream)
	}
	if tx.busy {
		t.Error("busy should be false")
	}
}

func TestTurnDispatch_handleTurnDone_successNoStreaming(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("", nil))
	tx := newTestTx()
	d.startTurn(&tx, "q", "")

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

func TestTurnDispatch_turnCmd_dispatchesTurn(t *testing.T) {
	t.Parallel()
	var calledPrompt string
	turnFn := func(_ context.Context, prompt string, _ string) (TurnResult, error) {
		calledPrompt = prompt
		return TurnResult{Answer: "ok"}, nil
	}
	d := NewTurnDispatch(turnFn)
	tx := newTestTx()
	cmd := d.startTurn(&tx, "hello", "")

	msg := cmd()
	tdm := msg.(turnDoneMsg)
	if tdm.prompt != "hello" {
		t.Errorf("prompt = %q, want %q", tdm.prompt, "hello")
	}
	if calledPrompt != "hello" {
		t.Errorf("turn not called with correct prompt, got %q", calledPrompt)
	}
}

func TestTurnDispatch_fullCycle(t *testing.T) {
	t.Parallel()
	d := NewTurnDispatch(stubTurn("final", nil))
	tx := newTestTx()

	cmd := d.startTurn(&tx, "go", "")
	if cmd == nil {
		t.Fatal("startTurn returned nil")
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
