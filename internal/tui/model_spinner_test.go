package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func TestBusySpinner_animatesAndStops(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	if !strings.Contains(view(m), "⠋ Working") {
		t.Fatalf("busy state must show the first spinner frame, got: %q", view(m))
	}

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
	frame := string(busySpinnerFrames[m.tx.spinner])
	if !strings.Contains(view(m), frame+" Working") {
		t.Errorf("busy line must render frame %q, got: %q", frame, view(m))
	}

	nm, _ = m.Update(turnDoneMsg{prompt: "hi", answer: "final answer"})
	m = asModel(t, nm)
	if m.tx.busy {
		t.Fatalf("turn completion must clear busy")
	}
	if m.tx.spinner != 0 {
		t.Errorf("spinner must reset after completion, got %d", m.tx.spinner)
	}
}

func TestBusySpinner_reducedMotionFallsBack(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	if got := newestBusyLine(m); got != "… thinking" {
		t.Fatalf("reduced-motion busy line = %q, want static %q", got, "… thinking")
	}
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

func TestToolElapsed_timerRenders(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
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
	m2 := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
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

func thinkingOffToolModel() Model {
	return NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: config.Config{ThinkingEnabled: false},
	})
}

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

func TestToolPulse_notSetWhenThinkingOn(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
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

// busy band, Phase, tool-start pulse): on a thinking-off turn the chain of
// thought is collapsed away entirely, so the brief tool-start pulse in the
// bottom busy band is the only "something started" signal. While the pulse is
// armed the band line must flash to the accent hue; once it expires the band
// must fall back to the faint secondary style — and the band must keep
// reporting the derived Phase throughout.
func TestToolPulse_busyBandFlashesWithCollapsedCoT(t *testing.T) {
	if !motionEnabled() {
		t.Skip("motion disabled in this environment; the static fallback is covered separately")
	}
	th := themeFor(config.DefaultTheme)
	tx := &Transcript{
		theme:           th,
		configTheme:     config.DefaultTheme,
		reasoningEffort: "medium",
		width:           100,
		height:          12,
		busy:            true,
		spinner:         0,
		histFollow:      true,
		histViewport:    newHistoryViewport(),
		messages: []message{
			{role: "you", content: "run it"},
			{role: "eitri", content: "", streaming: true, thinkingRequested: false},
		},
	}
	wireLive(tx, []TimelineEvent{
		{Kind: EventReasoning, Seq: 0, Delta: "secret reasoning"},
		{Kind: EventToolStart, Seq: 1, Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}},
	})
	tx.busyPulse = 3 // the tool start armed the pulse (model.applyToolUpdate)

	renderBand := func() string {
		var hist strings.Builder
		tx.renderHistory(&hist, nil, nil)
		for _, ln := range strings.Split(hist.String(), "\n") {
			if strings.Contains(ansiStrip(ln), "Working") {
				return ln
			}
		}
		t.Fatalf("busy band must report the Working phase, got: %q", ansiStrip(hist.String()))
		return ""
	}

	const accent = "38;2;122;162;247" // default theme accent #7AA2F7

	band := renderBand()
	if strings.Contains(ansiStrip(band), "secret reasoning") {
		t.Errorf("thinking-off turn must keep its CoT collapsed (hidden), band: %q", band)
	}
	if !strings.Contains(band, accent) {
		t.Errorf("pulse-armed band must flash to the accent hue, got: %q", band)
	}

	tx.busyPulse = 0 // the pulse expired after its 3 ticks
	settled := renderBand()
	if strings.Contains(settled, accent) {
		t.Errorf("band must fall back off the pulse once it expires, got: %q", settled)
	}
	if !strings.Contains(settled, "\x1b[2m") {
		t.Errorf("settled band must render faint (statusStyle), got: %q", settled)
	}
}

func TestComposerRail_modeColor(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "done"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")

	if rail := m.composer.Styles().Focused.Prompt.GetForeground(); rail != m.tx.theme.accent {
		t.Fatalf("idle rail = %v, want the full accent", rail)
	}

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asModel(t, nm)
	if !m.tx.busy {
		t.Fatalf("model must be busy after submit")
	}
	if rail := m.composer.Styles().Focused.Prompt.GetForeground(); rail == m.tx.theme.accent {
		t.Errorf("busy rail must dim from the accent, still accent")
	}

	m = upd(t, m, turnDoneMsg{prompt: "hi", answer: "done"})
	if rail := m.composer.Styles().Focused.Prompt.GetForeground(); rail != m.tx.theme.accent {
		t.Errorf("rail must restore the accent after the turn, got %v", rail)
	}

}

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

func TestBusySpinner_pulseOnFirstDelta(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse before delta = %d, want 0", m.tx.busyPulse)
	}

	m = applyDelta(t, m, "hello")
	if m.tx.busyPulse != 3 {
		t.Fatalf("busyPulse after first delta = %d, want 3", m.tx.busyPulse)
	}

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

	nm, _ = m.Update(spinnerTickMsg{})
	m = asModel(t, nm)
	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse after 4th tick = %d, want 0", m.tx.busyPulse)
	}
}

func TestBusySpinner_pulseRendersBright(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyDelta(t, m, "hello")
	if m.tx.busyPulse == 0 {
		t.Fatal("busyPulse must be non-zero after first delta")
	}

	content := view(m)
	if !strings.Contains(content, "Answering") {
		t.Fatalf("busy line must render during pulse, got: %q", content)
	}

	for i := 0; i < 3; i++ {
		nm, _ := m.Update(spinnerTickMsg{})
		m = asModel(t, nm)
	}
	if m.tx.busyPulse != 0 {
		t.Fatalf("busyPulse must be 0 after 3 ticks, got %d", m.tx.busyPulse)
	}

	content = view(m)
	if !strings.Contains(content, "Answering") {
		t.Fatalf("busy line must still render after pulse, got: %q", content)
	}
}
