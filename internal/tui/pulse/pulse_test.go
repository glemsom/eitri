package pulse

import (
	"testing"
	"time"
)

// The interface is the test surface: phase derivation, the elapsed clock, and
// the animation cadences are exercised through Pulse's own methods.

func TestStatus_derivesPhaseFromBusyAndStream(t *testing.T) {
	cases := []struct {
		name            string
		busy, streaming bool
		reasoning       string
		content         string
		want            Phase
	}{
		{"idle", false, false, "", "", PhaseIdle},
		{"working", true, false, "", "", PhaseWorking},
		{"reasoning", true, true, "thinking", "", PhaseReasoning},
		{"answering", true, true, "", "answer", PhaseAnswering},
		{"answer precedes reasoning", true, true, "thinking", "delivering", PhaseAnswering},
	}
	for _, c := range cases {
		p := New(nil)
		if c.busy {
			p.BeginBusy()
		}
		p.NoteStream(c.streaming, c.reasoning != "", c.content != "")
		if got := p.Status().Phase; got != c.want {
			t.Errorf("%s: phase = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStatus_idleWhenNoTurnRuns(t *testing.T) {
	var p *Pulse
	if !p.Status().Phase.isIdle() {
		t.Error("a nil pulse must read idle")
	}
}

func TestVerb_mapsEachBusyPhaseToItsVerb(t *testing.T) {
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
		if got := Verb(c.p); got != c.want {
			t.Errorf("Verb(%v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestStatus_elapsedReadsTheClock(t *testing.T) {
	now := time.Unix(1000, 0)
	p := New(func() time.Time { return now })
	p.Start()
	now = now.Add(90 * time.Second)
	if got := p.Status().Elapsed; got != 90*time.Second {
		t.Errorf("elapsed = %v, want 90s", got)
	}
}

func TestTickBusy_advancesFramesAndExpiresGlint(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "")
	p := New(nil)
	p.Start()
	p.Arm()
	if got := p.Status().Pulse; got != 3 {
		t.Fatalf("after Arm, pulse = %d, want 3", got)
	}
	for i := 0; i < 3; i++ {
		p.TickBusy()
	}
	if got := p.Status().Pulse; got != 0 {
		t.Errorf("after 3 ticks, pulse = %d, want 0", got)
	}
	if got := p.Status().Frame; got != 3%len(SpinnerFrames) {
		t.Errorf("frame = %d, want %d", got, 3%len(SpinnerFrames))
	}
}

func TestEndTurn_parksFramesAndClearsBusy(t *testing.T) {
	p := New(nil)
	p.Start()
	p.TickBusy()
	p.EndTurn()
	st := p.Status()
	if st.Busy || st.Frame != 0 || st.ForgeFrame != 0 {
		t.Errorf("EndTurn left %+v", st)
	}
}

func TestTickEmber_settlesWhenMotionOff(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	p := New(nil)
	if cmd := p.ArmEmber(false); cmd != nil {
		t.Error("ArmEmber must not arm under reduced motion")
	}
	if got := p.Status().EmberRemaining; got != 0 {
		t.Errorf("EmberRemaining = %d, want 0", got)
	}
}

func TestEmber_boundedWindowSettles(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "")
	p := New(nil)
	if cmd := p.ArmEmber(false); cmd == nil {
		t.Fatal("ArmEmber must arm the timer")
	}
	steps := 0
	for p.Status().EmberRemaining > 0 {
		p.TickEmber(false)
		steps++
		if steps > EmberWindow+2 {
			t.Fatal("ember window never closed")
		}
	}
	if got := p.Status().EmberFrame; got != 0 {
		t.Errorf("settled ember frame = %d, want 0", got)
	}
}

func (p Phase) isIdle() bool { return p == PhaseIdle }
