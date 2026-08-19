package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

// TestBusySpinner_animatesAndStops asserts the busy indicator is an animated
// braille spinner that advances on spinnerTickMsg while a turn runs, re-issues
// the tick, and stops (no re-tick, frame reset) once the turn completes
// .
func TestBusySpinner_animatesAndStops(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	if !strings.Contains(view(m), "⠋ Working") {
		t.Fatalf("busy state must show the first spinner frame, got: %q", view(m))
	}

	// Advance the spinner: the frame index moves and the tick re-issues.
	nm, cmd := m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if cmd == nil {
		t.Fatalf("spinner tick must re-issue while busy")
	}
	first := m.tx.spinner
	nm, _ = m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if m.tx.spinner != (first+1)%len(busySpinnerFrames) {
		t.Fatalf("spinner frame did not advance: %d -> %d", first, m.tx.spinner)
	}
	// The rendered line carries the current braille glyph.
	frame := string(busySpinnerFrames[m.tx.spinner])
	if !strings.Contains(view(m), frame+" Working") {
		t.Errorf("busy line must render frame %q, got: %q", frame, view(m))
	}

	// Turn completes: busy clears, the spinner resets, and no further tick is
	// issued (the completion path returns no spinner command).
	nm, _ = m.Update(turnDoneMsg{prompt: "hi", answer: "final answer"})
	m = asModel(t, nm)
	if m.tx.busy {
		t.Fatalf("turn completion must clear busy")
	}
	if m.tx.spinner != 0 {
		t.Errorf("spinner must reset after completion, got %d", m.tx.spinner)
	}
}

// TestBusySpinner_reducedMotionFallsBack asserts the EITRI_NO_MOTION opt-out
// switches the busy indicator to the static "… thinking" line (:
// reduced-motion gate; the locale/UTF-8 fallback shares the same line).
func TestBusySpinner_reducedMotionFallsBack(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	if got := newestBusyLine(m); got != "… thinking" {
		t.Fatalf("reduced-motion busy line = %q, want static %q", got, "… thinking")
	}
	// A late tick must not start an animation chain.
	nm, cmd := m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if cmd != nil {
		t.Fatalf("spinner tick must not re-issue under reduced motion")
	}
	if m.tx.spinner != 0 {
		t.Errorf("spinner frame must stay 0 under reduced motion, got %d", m.tx.spinner)
	}
	_ = time.Second // keep the import honest if assertions change
}

// newestBusyLine extracts the busy indicator's plain text from the rendered
// view: the transcript row carrying the spinner+stage verb (Working / Reasoning
// / Answering, issue #365) or the static line (the band below holds the
// composer, which is not the indicator).
func newestBusyLine(m Model) string {
	verbs := []string{"Working", "Reasoning", "Answering", "… thinking"}
	for _, row := range strings.Split(strings.TrimRight(view(m), "\n"), "\n") {
		line := strings.TrimSpace(plain(row))
		for _, verb := range verbs {
			if strings.Contains(line, verb) {
				return line
			}
		}
	}
	return ""
}

// TestToolElapsed_timerRenders asserts a completed tool whose execution took a
// second or more carries the elapsed-time readout on its entry head, while
// sub-second tools stay silent .
func TestToolElapsed_timerRenders(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: NewToolFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "ok (2.1s)", Lines: 1})
	m.tx.log.SetStart(0, m.tx.log.Entry(0).doneAt.Add(-2*time.Second))

	content := view(m)
	if !strings.Contains(content, "2s") {
		t.Errorf("tool head must carry the elapsed readout, got: %q", content)
	}
	// Sub-second tool: no timer noise.
	m2 := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: NewToolFeed(),
	})
	m2 = resize(t, m2)
	m2 = typeText(t, m2, "fast")
	m2 = submitAndWait(t, m2)
	m2 = toolStart(t, m2, "bash", `{"command":"true"}`)
	m2 = toolResult(t, m2, ToolResult{Name: "bash", Result: "ok (1ms)", Lines: 1})
	content2 := view(m2)
	if strings.Contains(content2, "0s") {
		t.Errorf("sub-second tool must not render a timer, got: %q", content2)
	}
}

// thinkingOffToolModel builds a model wired to a live tool feed with thinking
// OFF — the pulse-fallback configuration: no chain-of-thought stream, so tool
// starts are the only live-activity signal on the surface.
func thinkingOffToolModel() Model {
	return NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Tools:  NewToolFeed(),
		Config: config.Config{ThinkingEnabled: false},
	})
}

// TestToolPulse_setOnStartThinkingOff asserts a tool starting while thinking is
// off arms the tool-activity pulse fallback: busyPulse jumps to 3 so the
// running entry flashes the agent accent while it executes.
func TestToolPulse_setOnStartThinkingOff(t *testing.T) {
	m := thinkingOffToolModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse before tool start = %d, want 0", m.tx.busyPulse)
	}
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	if m.tx.busyPulse != 3 {
		t.Fatalf("tool start (thinking off) must arm the pulse to 3, got %d", m.tx.busyPulse)
	}
}

// TestToolPulse_notSetWhenThinkingOn asserts the tool-activity pulse is a
// fallback only: with thinking on the stream delta already pulses, so a tool
// start must NOT re-arm the counter — thinking-on behavior is unchanged.
func TestToolPulse_notSetWhenThinkingOn(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Tools:  NewToolFeed(),
		Config: config.Config{ThinkingEnabled: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	if m.tx.busyPulse != 0 {
		t.Fatalf("tool start with thinking on must not pulse, got %d", m.tx.busyPulse)
	}
}

// TestToolPulse_reducedMotionStaysStatic asserts the reduced-motion opt-out
// keeps the surface static: a tool start must not arm the pulse (leaving the
// static "… thinking" indicator and the entry's normal hue untouched).
func TestToolPulse_reducedMotionStaysStatic(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := thinkingOffToolModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	if m.tx.busyPulse != 0 {
		t.Fatalf("tool start under reduced motion must not pulse, got %d", m.tx.busyPulse)
	}
	if got := newestBusyLine(m); got != "… thinking" {
		t.Fatalf("reduced-motion busy line = %q, want static %q", got, "… thinking")
	}
}

// TestToolPulse_decrementsOnTick asserts the tool-armed pulse uses the same
// frame countdown as the stream-delta pulse: 3 spinner ticks decay it to 0.
func TestToolPulse_decrementsOnTick(t *testing.T) {
	m := thinkingOffToolModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	if m.tx.busyPulse != 3 {
		t.Fatalf("tool start must arm the pulse to 3, got %d", m.tx.busyPulse)
	}
	for _, want := range []int{2, 1, 0} {
		nm, _ := m.Update(spinnerTickMsg{})
		m = asModel(t, nm)
		if m.tx.busyPulse != want {
			t.Fatalf("pulse after tick = %d, want %d", m.tx.busyPulse, want)
		}
	}
}

// TestToolPulse_runningEntryRendersAccent asserts a running (incomplete) tool
// entry renders its head in the agent accent while the pulse window is active
// and settles back to its category hue once the pulse expires — the visual
// "live activity" signal for a tool phase with thinking off.
func TestToolPulse_runningEntryRendersAccent(t *testing.T) {
	const accent = "38;2;122;162;247" // default theme accent #7AA2F7
	const shell = "38;2;224;175;104"  // default theme shell category #E0AF68

	m := thinkingOffToolModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	if m.tx.busyPulse == 0 {
		t.Fatal("pulse must be armed on tool start")
	}

	// During the pulse the running entry's head carries the accent, not the
	// shell category hue.
	line := lineContaining(view(m), "🔧 bash")
	if line == "" {
		t.Fatalf("expected a running bash entry, got: %q", view(m))
	}
	if !strings.Contains(line, accent) {
		t.Errorf("running entry during pulse = %q, want accent hue", line)
	}
	if strings.Contains(line, shell) {
		t.Errorf("running entry during pulse must not carry the shell hue, got: %q", line)
	}

	// Pulse expires: the same running entry settles back to its category hue.
	for i := 0; i < 3; i++ {
		nm, _ := m.Update(spinnerTickMsg{})
		m = asModel(t, nm)
	}
	if m.tx.busyPulse != 0 {
		t.Fatalf("pulse must expire after 3 ticks, got %d", m.tx.busyPulse)
	}
	line = lineContaining(view(m), "🔧 bash")
	if line == "" {
		t.Fatalf("running bash entry still expected, got: %q", view(m))
	}
	if !strings.Contains(line, shell) {
		t.Errorf("entry after pulse should carry its shell hue, got: %q", line)
	}
}

// TestComposerRail_modeColor asserts the composer's prompt rail encodes edit
// state as color: the full accent while idle, a dimmed accent while a turn
// runs (composer inert), and back to the full accent when the turn completes
// (benchmark §4.3 state-as-color: mode-colored composer border). The rail's
// glyph and width never change, so caret geometry is untouched.
func TestComposerRail_modeColor(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "done"}, nil
		},
		Tools: NewToolFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")

	if rail := m.composer.Styles().Focused.Prompt.GetForeground(); rail != m.tx.theme.accent {
		t.Fatalf("idle rail = %v, want the full accent", rail)
	}

	// Busy (turn in flight): the rail dims.
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, nm)
	if !m.tx.busy {
		t.Fatalf("model must be busy after submit")
	}
	if rail := m.composer.Styles().Focused.Prompt.GetForeground(); rail == m.tx.theme.accent {
		t.Errorf("busy rail must dim from the accent, still accent")
	}

	// Turn completes: rail restores.
	m = upd(t, m, turnDoneMsg{prompt: "hi", answer: "done"})
	if rail := m.composer.Styles().Focused.Prompt.GetForeground(); rail != m.tx.theme.accent {
		t.Errorf("rail must restore the accent after the turn, got %v", rail)
	}

}

// TestIdleWelcome_showsOnEmptyHidesAfterTurn asserts the empty transcript
// renders the designed welcome block (brand mark + hints) and it disappears
// once the first turn lands — the idle surface reads as designed, not blank,
// and never competes with real content .
func TestIdleWelcome_showsOnEmptyHidesAfterTurn(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)

	content := view(m)
	if !strings.Contains(content, "your terminal coding agent") {
		t.Fatalf("empty transcript must show the welcome, got: %q", content)
	}
	if !strings.Contains(content, "ctrl+s settings") {
		t.Errorf("welcome must carry the keybinding hints, got: %q", content)
	}

	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	if strings.Contains(view(m), "your terminal coding agent") {
		t.Errorf("welcome must disappear after the first turn, got: %q", view(m))
	}
}

// TestBusySpinner_pulseOnFirstDelta asserts the busyPulse counter is set to 3
// when the first stream delta arrives and decrements on each spinner tick,
// reaching 0 and staying there.
func TestBusySpinner_pulseOnFirstDelta(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	// Before any delta, busyPulse must be 0.
	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse before delta = %d, want 0", m.tx.busyPulse)
	}

	// First delta sets the pulse counter.
	m = applyDelta(t, m, "hello")
	if m.tx.busyPulse != 3 {
		t.Fatalf("busyPulse after first delta = %d, want 3", m.tx.busyPulse)
	}

	// Spinner tick decrements the pulse.
	nm, _ := m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if m.tx.busyPulse != 2 {
		t.Fatalf("busyPulse after 1st tick = %d, want 2", m.tx.busyPulse)
	}

	nm, _ = m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if m.tx.busyPulse != 1 {
		t.Fatalf("busyPulse after 2nd tick = %d, want 1", m.tx.busyPulse)
	}

	nm, _ = m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse after 3rd tick = %d, want 0", m.tx.busyPulse)
	}

	// Further ticks keep pulse at 0.
	nm, _ = m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse after 4th tick = %d, want 0", m.tx.busyPulse)
	}
}

// TestBusySpinner_pulseRendersBright asserts the busy spinner renders
// the accent-styled bandStatusStyle during the pulse window and falls
// back to the faint statusStyle after the pulse expires.
func TestBusySpinner_pulseRendersBright(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	// First delta triggers the pulse.
	m = applyDelta(t, m, "hello")
	if m.tx.busyPulse == 0 {
		t.Fatal("busyPulse must be non-zero after first delta")
	}

	// The spinner text is present in the view during pulse. The first delta is
	// answer content, so the stage label reads Answering (issue #365).
	content := view(m)
	if !strings.Contains(content, "Answering") {
		t.Fatalf("busy line must render during pulse, got: %q", content)
	}

	// Decrement pulse to 0.
	for i := 0; i < 3; i++ {
		nm, _ := m.Update(spinnerTickMsg{})
		m = asModel(t, nm)
	}
	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse must be 0 after 3 ticks, got %d", m.tx.busyPulse)
	}

	// Spinner still renders after pulse expires (now faint style).
	content = view(m)
	if !strings.Contains(content, "Answering") {
		t.Fatalf("busy line must still render after pulse, got: %q", content)
	}
}
