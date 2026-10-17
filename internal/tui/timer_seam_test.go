package tui

import (
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/tui/pulse"
)

// instantTick delivers a tick message the moment its command runs, so the
// harness advances a timer by executing the command rather than waiting out its
// real cadence.
func instantTick(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	return func() tea.Msg { return fn(time.Time{}) }
}

// TestMain installs the immediate timer schedule for the whole package: every
// model built through NewModelCfg ticks without wall-clock delay, so the suite
// no longer sleeps out the spinner (80 ms), idle ember (140 ms), clock (1 s),
// and face-draw (50 ms) timers. Production keeps the real tea.Tick default.
func TestMain(m *testing.M) {
	defaultTick = instantTick
	os.Exit(m.Run())
}

// TestTimerSeam_idleEmberTransitionIsDeterministic locks the injectable timer
// seam: a model with an injected schedule advances its idle ember by running
// the armed tick command, with no real sleep and no dependence on the 140 ms
// wall-clock cadence.
func TestTimerSeam_idleEmberTransitionIsDeterministic(t *testing.T) {
	var scheduled []time.Duration
	m := NewModelCfg(Dependencies{Tick: func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		scheduled = append(scheduled, d)
		return func() tea.Msg { return fn(time.Time{}) }
	}})

	cmd := m.armIdleEmber()
	if cmd == nil {
		t.Fatal("an idle surface must arm the ember timer")
	}
	if len(scheduled) != 1 || scheduled[0] != pulse.EmberTickInterval {
		t.Fatalf("ember armed %v, want one tick at %s", scheduled, pulse.EmberTickInterval)
	}

	msg := cmd()
	if _, ok := msg.(pulse.IdleEmberTickMsg); !ok {
		t.Fatalf("armed ember command delivered %T, want pulse.IdleEmberTickMsg", msg)
	}
	before := m.tx.Status().EmberFrame
	m = upd(t, m, msg)
	if m.tx.Status().EmberFrame != before+1 {
		t.Fatalf("ember frame = %d, want %d after one injected tick", m.tx.Status().EmberFrame, before+1)
	}
}

// TestTimerSeam_everySourceHonorsInjectedSchedule proves the clock, face draw,
// busy spinner, and idle ember all arm on the one injected schedule, each at
// its declared cadence, so injecting the schedule covers every timer.
func TestTimerSeam_everySourceHonorsInjectedSchedule(t *testing.T) {
	var scheduled []time.Duration
	spy := func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		scheduled = append(scheduled, d)
		return func() tea.Msg { return fn(time.Time{}) }
	}
	m := NewModelCfg(Dependencies{Tick: spy})

	assertMsg := func(name string, cmd tea.Cmd, want time.Duration, match func(tea.Msg) bool) {
		t.Helper()
		if cmd == nil {
			t.Fatalf("%s: no tick command armed", name)
		}
		if got := scheduled[len(scheduled)-1]; got != want {
			t.Errorf("%s scheduled %s, want %s", name, got, want)
		}
		if msg := cmd(); !match(msg) {
			t.Errorf("%s delivered %T, want its tick message", name, msg)
		}
	}

	assertMsg("clock", m.clockTick(), clockTickInterval, func(msg tea.Msg) bool {
		_, ok := msg.(clockTickMsg)
		return ok
	})
	assertMsg("face", m.faceDrawTick(), faceDrawTickInterval, func(msg tea.Msg) bool {
		_, ok := msg.(faceDrawMsg)
		return ok
	})
	assertMsg("spinner", m.tx.SpinnerTick(), pulse.SpinnerTickInterval, func(msg tea.Msg) bool {
		_, ok := msg.(pulse.SpinnerTickMsg)
		return ok
	})
	assertMsg("ember", m.armIdleEmber(), pulse.EmberTickInterval, func(msg tea.Msg) bool {
		_, ok := msg.(pulse.IdleEmberTickMsg)
		return ok
	})
}
