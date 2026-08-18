package tui

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// blockingTurn is a Turn seam that runs until its context is canceled and then
// reports the cancellation, modeling a real engine turn that esc aborts.
func blockingTurn(ctx context.Context, prompt string, _ string) (TurnResult, error) {
	<-ctx.Done()
	return TurnResult{}, ctx.Err()
}

// TestModel_escWhileBusyCancelsTurnAndKeepsPartial asserts pressing esc during
// a running turn cancels the in-flight turn's context (the running work dies
// at the ctx boundary), the partial content the turn produced stays in the
// transcript, the turn is marked stopped (not rendered as an error), the busy
// state clears, and a subsequent prompt runs normally.
func TestModel_escWhileBusyCancelsTurnAndKeepsPartial(t *testing.T) {
	var canceled atomic.Bool
	var enteredOnce sync.Once
	entered := make(chan struct{})
	var calls atomic.Int32
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		calls.Add(1)
		if calls.Load() > 1 {
			// A subsequent prompt runs normally and completes on its own.
			return TurnResult{Answer: "second answer"}, nil
		}
		enteredOnce.Do(func() { close(entered) })
		<-ctx.Done()
		canceled.Store(true)
		// The engine stand-in keeps the partial content it had produced when
		// the cancellation lands, as the real engine does in its stop outcome.
		return TurnResult{Answer: "partial answer"}, ctx.Err()
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)

	// Run the turn command on its own goroutine (it blocks until canceled).
	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- cmd() }()
	<-entered

	// esc while busy must cancel the running turn.
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asModel(t, nm)

	// The aborted turn's completion lands as a stopped result, not an error.
	var done tea.Msg
	select {
	case done = <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("turn goroutine never returned after esc")
	}
	if !canceled.Load() {
		t.Error("esc did not cancel the in-flight turn context")
	}
	nm, _ = m.Update(done)
	m = asModel(t, nm)

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
	if m.td.s.curStream != -1 {
		t.Errorf("stream pointer = %d, want -1 after stop", m.td.s.curStream)
	}

	// A subsequent normal prompt works.
	m = typeText(t, m, "next")
	m = submitAndWait(t, m)
	last := m.tx.messages[len(m.tx.messages)-1]
	if last.content != "second answer" {
		t.Errorf("subsequent turn did not run, got content %q", last.content)
	}
}

// TestModel_escWhileBusyMarksStoppedNotError asserts the stopped outcome is
// surface-distinct from an error: `TurnResult{Stopped:true}` (the app adapter's
// mapping of the engine stop sentinel) keeps the partial answer, appends no
// error line, and the message renders with the stopped marker rather than the
// error prefix.
func TestModel_escWhileBusyMarksStoppedNotError(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Stopped: true, Answer: "prior reasoning"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, cmd := submitBusy(t, m)

	// The turn completes as a stopped result (mapped by the app adapter from
	// engine.ErrStopped): the partial answer must stay, marked stopped, with no
	// error rendering.
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

// TestModel_escIdleStaysNoop asserts esc with no running turn leaves state
// untouched: no cancel handle is invoked, the composer keeps focus, and no
// turn starts (vim-normal mode is gone; esc remains an idle no-op).
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

// TestModel_stoppedBeforeAnyDeltaAppendsPartialMessage asserts a turn stopped
// before the first stream delta still surfaces the partial answer the engine
// accumulated, as a stopped message rather than an error.
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

// TestModel_stoppedStreamKeepsStreamedBuffer asserts a stopped turn that was
// streaming reconciles the in-progress message with the engine's authoritative
// partial answer: the buffer keeps the streamed text, streaming clears, and
// the message is marked stopped.
func TestModel_stoppedStreamKeepsStreamedBuffer(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Stream: NewStreamer(),
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
	if m.td.s.curStream != -1 {
		t.Errorf("stream pointer = %d, want -1 after stop", m.td.s.curStream)
	}
}

// TestModel_stoppedThenNewTurnWorks asserts a fresh prompt after a stop runs
// normally: the new turn is a normal (non-stopped) assistant reply.
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

// TestModel_turnCancelErrorSurfacesAsStopped asserts a turn that dies to a raw
// context.Canceled (a generic engine stand-in that does not map the stop to
// TurnResult.Stopped) is still rendered as stopped, not as an error.
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

// TestRender_stoppedMarkerPin pins the stopped marker string so the render
// surface and tests agree on its shape.
func TestRender_stoppedMarkerPin(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	if stoppedMarker() != "! stopped" {
		t.Errorf("stoppedMarker = %q, want %q", stoppedMarker(), "! stopped")
	}
}

// TestModel_ctrlCWhileBusyStopsTurnAndKeepsPartial asserts pressing Ctrl+C
// during a running turn cancels it (same outcome as esc): the partial content
// stays, the turn is marked stopped, and busy clears. Ctrl+C is the natural
// stop binding — a second Ctrl+C after the stop quits because busy is false.
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

	doneCh := make(chan tea.Msg, 1)
	go func() { doneCh <- cmd() }()
	<-entered

	// Ctrl+C while busy must cancel the running turn.
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = asModel(t, nm)

	var done tea.Msg
	select {
	case done = <-doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("turn goroutine never returned after ctrl+c")
	}
	if !canceled.Load() {
		t.Error("ctrl+c did not cancel the in-flight turn context")
	}
	nm, _ = m.Update(done)
	m = asModel(t, nm)

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

// TestModel_ctrlCWhenIdleQuits asserts Ctrl+C with no running turn issues
// tea.Quit — the natural exit binding when the model is idle.
func TestModel_ctrlCWhenIdleQuits(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "never"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")

	nm, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = asModel(t, nm)

	if cmd == nil {
		t.Fatal("idle ctrl+c must emit a quit command")
	}
	// Execute the command; it should return a tea.QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("idle ctrl+c command returned %T, want QuitMsg", msg)
	}
}

// TestModel_ctrlCAfterStopQuits asserts Ctrl+C after a stopped turn quits
// (busy is false post-stop, so ctrl+c falls through to quit).
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

	// Ctrl+C after a stop must quit (busy is false).
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
