package tui

import "testing"

// This file covers the derived agent-phase seam (issue #363): a single Phase
// enum computed from the live turn state (busy flag + what the in-progress
// assistant stream is doing), instead of scattered boolean checks. It is the
// prefactor the stage-label verb (#365) and live-reasoning panel (#364) hang
// off, so its contract is: the phases derive correctly across every
// busy/stream transition, and the busy indicator's rendering is UNCHANGED
// (no visible output change; the "working" verb stays for every active phase).

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

func TestPhase_noVisibleChangeAcrossActivePhases(t *testing.T) {
	// Issue #363 keeps the surface byte-identical: every active phase renders
	// the same spinner + "working" verb (the label split lands in #365). The
	// reduced-motion/ASCII fallback stays the static "… thinking" line.
	if !motionEnabled() {
		t.Skip("motion disabled in this environment; rendering unchanged separately")
	}
	frames := []struct {
		name string
		tx   Transcript
	}{
		{"working", phaseTx(true, false, "", "")},
		{"reasoning", phaseTx(true, true, "thinking", "")},
		{"answering", phaseTx(true, true, "", "answer")},
	}
	// Every active phase shares the first-frame busy line.
	want := string(busySpinnerFrames[0]) + " working"
	for _, f := range frames {
		if got := busyLine(0, f.tx.phase()); got != want {
			t.Errorf("%s phase busy line = %q, want unchanged %q", f.name, got, want)
		}
	}
}
