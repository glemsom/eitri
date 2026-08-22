package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/testutil"
)

func TestModel_escWhileBusyCancelsTurnAndKeepsPartial(t *testing.T) {
	var canceled atomic.Bool
	var enteredOnce sync.Once
	entered := make(chan struct{})
	var calls atomic.Int32
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		calls.Add(1)
		if calls.Load() > 1 {
			return TurnResult{Answer: "second answer"}, nil
		}
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		canceled.Store(true)
		return TurnResult{Answer: "partial answer"}, ctx.Err()
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)

	done := make(chan struct{})
	var doneMsg tea.Msg
	go func() {
		defer close(done)
		doneMsg = cmd()
	}()

	testutil.Await(t, "running turn to start", entered)

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asModel(t, nm)

	testutil.Await(t, "turn goroutine to return after esc", done)
	m = mustUpdate(t, m, doneMsg)
	if !canceled.Load() {
		t.Error("esc did not cancel the in-flight turn context")
	}

	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "partial answer" {
		t.Errorf("partial content = %q, want %q (kept after stop)", got, "partial answer")
	}
	if got := m.tx.messages[len(m.tx.messages)-1].stopped; !got {
		t.Error("stopped turn message not marked stopped")
	}
	content := view(m)
	if strings.Contains(content, "context canceled") || strings.Contains(content, failurePrefix()) {
		t.Errorf("stopped turn rendered as an error, got: %q", content)
	}
	if !strings.Contains(content, stoppedMarker()) {
		t.Errorf("stopped turn must render the stopped marker, got: %q", content)
	}
	if m.tx.busy {
		t.Error("busy state must clear after the stop")
	}
	if m.td.curStream != -1 {
		t.Errorf("stream pointer = %d, want -1 after stop", m.td.curStream)
	}

	m = typeText(t, m, "next")
	m = submitAndWait(t, m)
	last := m.tx.messages[len(m.tx.messages)-1]
	if last.content != "second answer" {
		t.Errorf("subsequent turn did not run, got content %q", last.content)
	}
}

func TestModel_escWhileBusyMarksStoppedNotError(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Stopped: true, Answer: "prior reasoning"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)

	m = runSubmitted(t, m, cmd)

	last := m.tx.messages[len(m.tx.messages)-1]
	if last.content != "prior reasoning" {
		t.Errorf("content = %q, want %q (partial kept)", last.content, "prior reasoning")
	}
	if !last.stopped {
		t.Error("message must be marked stopped")
	}
	content := view(m)
	if strings.Contains(content, failurePrefix()) {
		t.Errorf("stopped turn must not render an error prefix, got: %q", content)
	}
	if !strings.Contains(content, stoppedMarker()) {
		t.Errorf("stopped turn must render the stopped marker, got: %q", content)
	}
	if m.tx.busy {
		t.Error("busy state must clear after the stop")
	}
}

func TestModel_escIdleStaysNoop(t *testing.T) {
	var canceled atomic.Bool
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "never"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")

	before := view(m)
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asModel(t, nm)

	if canceled.Load() {
		t.Error("idle esc must not invoke a cancel handle")
	}
	if cmd != nil {
		t.Error("idle esc must not emit a command")
	}
	if view(m) != before {
		t.Error("idle esc must not change the rendered surface")
	}
	if m.tx.busy {
		t.Error("idle esc must not make the model busy")
	}
}

func TestModel_stoppedBeforeAnyDeltaAppendsPartialMessage(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Stopped: true, Answer: "first words"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)
	m = runSubmitted(t, m, cmd)

	last := m.tx.messages[len(m.tx.messages)-1]
	if last.role != "eitri" {
		t.Errorf("role = %q, want eitri", last.role)
	}
	if last.content != "first words" || !last.stopped {
		t.Errorf("stopped message = %+v, want partial content marked stopped", last)
	}
}

func TestModel_stoppedStreamKeepsStreamedBuffer(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: config.Config{ThinkingEnabled: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyDelta(t, m, "partial ")

	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "partial more", stopped: true})
	m = asModel(t, nm)

	last := m.tx.messages[len(m.tx.messages)-1]
	if last.content != "partial more" {
		t.Errorf("content = %q, want the authoritative partial %q", last.content, "partial more")
	}
	if last.streaming {
		t.Error("streaming flag must clear after the stop")
	}
	if !last.stopped {
		t.Error("message must be marked stopped")
	}
	if m.td.curStream != -1 {
		t.Errorf("stream pointer = %d, want -1 after stop", m.td.curStream)
	}
}

func TestModel_stoppedThenNewTurnWorks(t *testing.T) {
	turns := 0
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		turns++
		if turns == 1 {
			return TurnResult{Stopped: true, Answer: "partial"}, nil
		}
		return TurnResult{Answer: "full answer"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "one")
	m, cmd := submitBusy(t, m)
	m = runSubmitted(t, m, cmd)

	if m.tx.busy {
		t.Fatal("busy must clear after the stopped turn")
	}

	m = typeText(t, m, "two")
	m = submitAndWait(t, m)

	last := m.tx.messages[len(m.tx.messages)-1]
	if last.content != "full answer" {
		t.Errorf("content = %q, want the second turn's full answer", last.content)
	}
	if last.stopped {
		t.Error("the new turn must not be marked stopped")
	}
}

func TestModel_turnCancelErrorSurfacesAsStopped(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		<-ctx.Done()
		return TurnResult{Answer: "partial"}, context.Canceled
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = runSubmitted(t, m, cmd)

	last := m.tx.messages[len(m.tx.messages)-1]
	if !last.stopped {
		t.Error("a context.Canceled turn must be marked stopped")
	}
	if last.content != "partial" {
		t.Errorf("content = %q, want the partial kept %q", last.content, "partial")
	}
	if strings.Contains(view(m), failurePrefix()) {
		t.Error("a stopped turn must not render an error prefix")
	}
	if m.tx.busy {
		t.Error("busy must clear after the stop")
	}
}

func TestRender_stoppedMarkerPin(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	if stoppedMarker() != "! stopped" {
		t.Errorf("stoppedMarker = %q, want %q", stoppedMarker(), "! stopped")
	}
}

func TestModel_ctrlCWhileBusyStopsTurnAndKeepsPartial(t *testing.T) {
	var canceled atomic.Bool
	var enteredOnce sync.Once
	entered := make(chan struct{})
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		canceled.Store(true)
		return TurnResult{Answer: "partial via ctrl+c"}, ctx.Err()
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)

	done := make(chan struct{})
	var doneMsg tea.Msg
	go func() {
		defer close(done)
		doneMsg = cmd()
	}()
	testutil.Await(t, "running turn to start", entered)

	nm, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = asModel(t, nm)

	testutil.Await(t, "turn goroutine to return after ctrl+c", done)
	m = mustUpdate(t, m, doneMsg)
	if !canceled.Load() {
		t.Error("ctrl+c did not cancel the in-flight turn context")
	}

	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "partial via ctrl+c" {
		t.Errorf("partial content = %q, want %q (kept after ctrl+c stop)", got, "partial via ctrl+c")
	}
	if !m.tx.messages[len(m.tx.messages)-1].stopped {
		t.Error("stopped turn message not marked stopped")
	}
	if m.tx.busy {
		t.Error("busy state must clear after ctrl+c stop")
	}
	content := view(m)
	if strings.Contains(content, failurePrefix()) {
		t.Errorf("stopped turn rendered as an error, got: %q", content)
	}
	if !strings.Contains(content, stoppedMarker()) {
		t.Errorf("stopped turn must render the stopped marker, got: %q", content)
	}
}

func TestModel_ctrlCWhenIdleQuits(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "never"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")

	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	asModel(t, nm)

	if cmd == nil {
		t.Fatal("idle ctrl+c must emit a quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("idle ctrl+c command returned %T, want QuitMsg", msg)
	}
}

func TestModel_ctrlCAfterStopQuits(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Stopped: true, Answer: "partial"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)
	m = runSubmitted(t, m, cmd)

	if m.tx.busy {
		t.Fatal("busy must clear after the stopped turn")
	}

	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = asModel(t, nm)

	if cmd == nil {
		t.Fatal("ctrl+c after stop must emit a quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c after stop returned %T, want QuitMsg", msg)
	}
}
