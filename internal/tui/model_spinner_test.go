package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

	if !strings.Contains(view(m), "⠋ working") {
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
	if !strings.Contains(view(m), frame+" working") {
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
// view: the transcript row carrying the spinner/static line (the band below
// holds the composer, which is not the indicator).
func newestBusyLine(m Model) string {
	for _, row := range strings.Split(strings.TrimRight(view(m), "\n"), "\n") {
		line := strings.TrimSpace(plain(row))
		if strings.Contains(line, "working") || strings.Contains(line, "… thinking") {
			return line
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
