package tui

import "testing"

func phaseTx(busy, streaming bool, reasoning, content string) Transcript {
	tx := Transcript{busy: busy}
	if streaming {
		tx.messages = append(tx.messages, message{
			role:      "eitri",
			reasoning: reasoning,
			content:   content,
			streaming: true,
		})
	}
	return tx
}

func TestPhase_idleWhenNoTurnRuns(t *testing.T) {
	if got := phaseTx(false, false, "", "").phase(); got != PhaseIdle {
		t.Errorf("phase = %v, want idle", got)
	}
}

func TestPhase_workingWhenBusyButNothingStreams(t *testing.T) {
	if got := phaseTx(true, false, "", "").phase(); got != PhaseWorking {
		t.Errorf("phase = %v, want working", got)
	}
}

func TestPhase_reasoningWhileChainOfThoughtStreams(t *testing.T) {
	if got := phaseTx(true, true, "thinking step by step", "").phase(); got != PhaseReasoning {
		t.Errorf("phase = %v, want reasoning", got)
	}
}

func TestPhase_answeringWhileAnswerStreams(t *testing.T) {
	if got := phaseTx(true, true, "", "the answer text").phase(); got != PhaseAnswering {
		t.Errorf("phase = %v, want answering", got)
	}
}

func TestPhase_answerPrecedesReasoningWhenBothPresent(t *testing.T) {
	if got := phaseTx(true, true, "thinking", "delivering").phase(); got != PhaseAnswering {
		t.Errorf("phase = %v, want answering (answer takes precedence)", got)
	}
}

func TestPhase_looksOnlyAtTheLiveStreamingMessage(t *testing.T) {
	tx := Transcript{
		busy: true,
		messages: []message{
			{role: "eitri", content: "a finished prior answer"},
			{role: "eitri", reasoning: "live chain-of-thought", streaming: true},
		},
	}
	if got := tx.phase(); got != PhaseReasoning {
		t.Errorf("phase = %v, want reasoning from the live streaming message", got)
	}
}

func TestPhase_transitionsThroughATurn(t *testing.T) {
	steps := []struct {
		tx   Transcript
		want Phase
	}{
		{phaseTx(false, false, "", ""), PhaseIdle},
		{phaseTx(true, false, "", ""), PhaseWorking},
		{phaseTx(true, true, "thinking", ""), PhaseReasoning},
		{phaseTx(true, true, "", "answer"), PhaseAnswering},
		{phaseTx(false, false, "", ""), PhaseIdle},
	}
	for i, s := range steps {
		if got := s.tx.phase(); got != s.want {
			t.Errorf("step %d: phase = %v, want %v", i, got, s.want)
		}
	}
}

func TestPhaseVerb_mapsEachBusyPhaseToItsVerb(t *testing.T) {
	cases := []struct {
		p    Phase
		want string
	}{
		{PhaseWorking, "Working"},
		{PhaseReasoning, "Reasoning"},
		{PhaseAnswering, "Answering"},
		{PhaseIdle, "Working"},
	}
	for _, c := range cases {
		if got := phaseVerb(c.p); got != c.want {
			t.Errorf("phaseVerb(%v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestPhase_busyLineVerbTracksPhase(t *testing.T) {
	if !motionEnabled() {
		t.Skip("motion disabled in this environment; static fallback covered separately")
	}
	frames := []struct {
		name string
		tx   Transcript
		want string
	}{
		{"working", phaseTx(true, false, "", ""), string(busySpinnerFrames[0]) + " Working"},
		{"reasoning", phaseTx(true, true, "thinking", ""), string(busySpinnerFrames[0]) + " Reasoning"},
		{"answering", phaseTx(true, true, "", "answer"), string(busySpinnerFrames[0]) + " Answering"},
	}
	for _, f := range frames {
		if got := busyLine(0, f.tx.phase()); got != f.want {
			t.Errorf("%s phase busy line = %q, want %q", f.name, got, f.want)
		}
	}
}

func TestPhase_busyLineReducedMotionPreserved(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	for _, p := range []Phase{PhaseWorking, PhaseReasoning, PhaseAnswering} {
		if got := busyLine(0, p); got != "… thinking" {
			t.Errorf("reduced-motion busyLine(%v) = %q, want %q", p, got, "… thinking")
		}
	}
}
