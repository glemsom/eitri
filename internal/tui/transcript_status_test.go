package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestStatus_reportsBusyTurn(t *testing.T) {
	tx := newTestTx()
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "hi"}})

	st := tx.Status()
	if !st.Busy {
		t.Fatal("a started turn must report busy")
	}
	if st.Phase != PhaseWorking {
		t.Fatalf("phase = %v, want working", st.Phase)
	}
	if st.Frame != 0 || st.Pulse != 0 {
		t.Fatalf("fresh busy frames = frame %d pulse %d, want 0, 0", st.Frame, st.Pulse)
	}
	if st.Elapsed <= 0 {
		t.Fatalf("elapsed = %v, want a running clock", st.Elapsed)
	}
}

func TestTickBusy_advancesSpinnerAndDrainsPulse(t *testing.T) {
	tx := newTestTx()
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "hi"}})
	tx.Project(TranscriptOutcome{Tool: &ToolUpdate{Start: &ToolStart{Name: "bash"}}})

	if got := tx.Status().Pulse; got != 3 {
		t.Fatalf("tool start on the busy turn must arm the pulse to 3, got %d", got)
	}
	for want := 2; want >= 0; want-- {
		if cmd := tx.TickBusy(); cmd == nil {
			t.Fatal("busy tick must re-arm while busy")
		}
		if got := tx.Status().Pulse; got != want {
			t.Fatalf("pulse after tick = %d, want %d", got, want)
		}
	}
	if got := tx.Status().Frame; got != 3 {
		t.Fatalf("spinner frame after three ticks = %d, want 3", got)
	}
}

func TestEndTurn_parksBusyAndStopsTick(t *testing.T) {
	tx := newTestTx()
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "hi"}})
	tx.TickBusy()
	tx.EndTurn()

	st := tx.Status()
	if st.Busy || st.Frame != 0 {
		t.Fatalf("ended turn status = %+v, want not busy and frame 0", st)
	}
	if cmd := tx.TickBusy(); cmd != nil {
		t.Fatal("busy tick must not re-arm after the turn ends")
	}
}

func TestArmEmber_opensBoundedWindow(t *testing.T) {
	tx := newTestTx()
	if cmd := tx.ArmEmber(false); cmd == nil {
		t.Fatal("an idle surface must arm the ember")
	}
	if got := tx.Status().EmberRemaining; got != idleEmberWindow {
		t.Fatalf("ember remaining = %d, want %d", got, idleEmberWindow)
	}
}

func TestTickEmber_advancesThenSettles(t *testing.T) {
	tx := newTestTx()
	tx.ArmEmber(false)
	var last tea.Cmd
	for range idleEmberWindow {
		last = tx.TickEmber(false)
	}
	if last != nil {
		t.Fatal("the bounded window must stop re-arming")
	}
	if st := tx.Status(); st.EmberFrame != 0 || st.EmberRemaining != 0 {
		t.Fatalf("settled ember = frame %d remaining %d", st.EmberFrame, st.EmberRemaining)
	}
}

func TestStatus_reducedMotionParks(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	tx := newTestTx()
	tx.Project(TranscriptOutcome{Start: &TranscriptStart{Prompt: "hi"}})
	if cmd := tx.TickBusy(); cmd != nil {
		t.Fatal("reduced motion must not re-arm the busy tick")
	}
	if got := tx.Status().Frame; got != 0 {
		t.Fatalf("reduced motion left spinner frame %d", got)
	}
	if cmd := tx.ArmEmber(false); cmd != nil {
		t.Fatal("reduced motion must not arm the ember")
	}
	if st := tx.Status(); st.EmberFrame != 0 || st.EmberRemaining != 0 {
		t.Fatalf("reduced motion left ember state frame %d remaining %d", st.EmberFrame, st.EmberRemaining)
	}
}
