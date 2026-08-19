package tui

import "testing"

// This file covers the derived agent-phase seam (issues #363/#365): a single
// Phase enum computed from the live turn state (busy flag + what the
// in-progress assistant stream is doing), instead of scattered boolean checks.
// #363 prefactored the enum with no visible change; #365 splits the busy
// indicator verb by stage — Reasoning while chain-of-thought streams, Working
// while tools run, Answering while answer text flows — while preserving the
// reduced-motion/static-line fallback.

// phaseTx builds a Transcript in a given live-turn state: busy on/off, with an
// optional in-progress streaming assistant message carrying the given
// reasoning/content.
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
	// Busy with no assistant message yet — the tool-heavy gap between the user
	// prompt and the first streamed token.
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
	// A backend may stream reasoning then answer; once answer text flows the
	// surface is answering regardless of trailing reasoning.
	if got := phaseTx(true, true, "thinking", "delivering").phase(); got != PhaseAnswering {
		t.Errorf("phase = %v, want answering (answer takes precedence)", got)
	}
}

func TestPhase_looksOnlyAtTheLiveStreamingMessage(t *testing.T) {
	// A committed, non-streaming prior message must not shape the phase; the
	// derivation reads only the in-progress streaming message.
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
	// One full turn drives every phase transition: idle -> working (tools) ->
	// reasoning -> answering -> idle once the turn lands.
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
	// Issue #365: the stage verb tracks the derived phase — Reasoning while
	// chain-of-thought streams, Working during the tool gap, Answering once
	// answer text flows. Idle never renders through the busy line, so it falls
	// back to the busy verb.
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
	// The animated busy line carries the stage verb under the spinner frame,
	// so the status strip reads the live stage while a turn runs (issue #365).
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
	// The reduced-motion/ASCII fallback stays the static "… thinking" line
	// regardless of phase (issue #365 AC4).
	t.Setenv("EITRI_NO_MOTION", "1")
	for _, p := range []Phase{PhaseWorking, PhaseReasoning, PhaseAnswering} {
		if got := busyLine(0, p); got != "… thinking" {
			t.Errorf("reduced-motion busyLine(%v) = %q, want %q", p, got, "… thinking")
		}
	}
}
