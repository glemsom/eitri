package tui

import "testing"

// Idle-ember contract: the bounded shimmer is the last piece of the forge
// ambience work. These tests lock the behaviour the model seam must keep —
// distinct timer, immediate stop on a run or overlay, re-arm on activity, and
// a per-theme render that shimmers without changing the brand's plain text —
// mirroring the per-theme gradient and meter contracts.

func TestIdleEmber_tickDistinctFromBusySpinner(t *testing.T) {
	if idleEmberTickInterval == busySpinnerTick {
		t.Fatal("idle ember and busy spinner must not share a tick interval")
	}

	// A running ember that is also busy settles on its next tick, and that
	// tick never advances the busy spinner frame.
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m.tx.ArmEmber(false)
	m.tx.setIdleEmberFrame(4)
	spinnerBefore, forgeBefore := m.tx.Status().Frame, m.tx.Status().ForgeFrame
	m = upd(t, m, idleEmberTickMsg{})
	if m.tx.Status().Frame != spinnerBefore || m.tx.Status().ForgeFrame != forgeBefore {
		t.Fatalf("idle tick changed busy spinner state: spinner %d->%d forge %d->%d",
			spinnerBefore, m.tx.Status().Frame, forgeBefore, m.tx.Status().ForgeFrame)
	}
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("busy surface must settle the ember on its tick, got frame=%d remaining=%d",
			m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}

	// Conversely, a busy spinner tick never advances an idle ember window.
	idle := idleEmberModel(t)
	idle.tx.ArmEmber(false)
	frame, remaining := idle.tx.Status().EmberFrame, idle.tx.Status().EmberRemaining
	idle = upd(t, idle, spinnerTickMsg{})
	if idle.tx.Status().EmberFrame != frame || idle.tx.Status().EmberRemaining != remaining {
		t.Fatalf("spinner tick changed idle ember state: frame %d->%d remaining %d->%d",
			frame, idle.tx.Status().EmberFrame, remaining, idle.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_runBeginStopsImmediately(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m.tx.ArmEmber(false)
	for range 5 {
		m = upd(t, m, idleEmberTickMsg{})
	}
	if m.tx.Status().EmberFrame == 0 {
		t.Fatal("precondition: the ember should be mid-sweep before the run begins")
	}

	m, _ = submitBusy(t, m)
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("run begin must settle the ember before the next tick, got frame=%d remaining=%d",
			m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
	nm, cmd := m.Update(idleEmberTickMsg{})
	m = asModel(t, nm)
	if cmd != nil {
		t.Fatal("a busy surface must not re-arm the idle ember")
	}
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("busy tick must leave the ember settled, got frame=%d remaining=%d",
			m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_overlaysStopArming(t *testing.T) {
	openOverlay := map[string]func(t *testing.T, m Model) Model{
		"settings": openSettingsForTest,
		"help": func(t *testing.T, m Model) Model {
			m = typeText(t, m, "/help")
			return keypress(t, m, "enter")
		},
		"max-turns": func(_ *testing.T, m Model) Model { m.prompting = true; return m },
	}
	for name, open := range openOverlay {
		t.Run(name, func(t *testing.T) {
			m := NewModelCfg(Dependencies{Config: cfgFixture()})
			m = resize(t, m)
			m = open(t, m)
			if !m.overlayOpen() {
				t.Fatalf("%s overlay should report open", name)
			}
			if cmd := m.armIdleEmber(); cmd != nil {
				t.Fatalf("an open %s overlay must not arm the idle ember", name)
			}
			m.tx.ArmEmber(false)
			m.tx.setIdleEmberFrame(3)
			m = upd(t, m, idleEmberTickMsg{})
			if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
				t.Fatalf("an open %s overlay must settle the ember, got frame=%d remaining=%d",
					name, m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
			}
		})
	}
}

func TestIdleEmber_turnDoneReArms(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m.tx.settleIdleEmber()
	if m.tx.Status().EmberRemaining != 0 {
		t.Fatal("precondition: ember should be settled while busy")
	}

	m = upd(t, m, turnDoneMsg{prompt: "hi", answer: "done"})
	if m.tx.Status().Busy {
		t.Fatal("turn completion should clear busy")
	}
	if m.tx.Status().EmberRemaining != idleEmberWindow {
		t.Fatalf("turn completion must re-arm the bounded ember window, got remaining=%d", m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_armExtendsWithoutSecondTimer(t *testing.T) {
	m := idleEmberModel(t)
	if cmd := m.armIdleEmber(); cmd == nil {
		t.Fatal("first activity must arm the ember tick")
	}
	m = upd(t, m, idleEmberTickMsg{})

	if cmd := m.armIdleEmber(); cmd != nil {
		t.Fatal("re-arming an already-running ember must not start a second timer")
	}
	if m.tx.Status().EmberRemaining != idleEmberWindow {
		t.Fatalf("re-arm must extend the bounded window to %d, got %d", idleEmberWindow, m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_reducedMotionRenderEqualsSettledEveryTheme(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	for _, name := range bundledThemeNames {
		t.Run(name, func(t *testing.T) {
			th := themeFor(name)
			settled := idleWelcome(th, 40, 0)
			frozen := idleWelcome(th, 40, 9)
			if settled != frozen {
				t.Errorf("reduced motion for %s must render the settled frame:\n settled=%q\n frozen =%q",
					name, settled, frozen)
			}
		})
	}
}

func TestIdleEmber_everyBundledThemeShimmersWithoutChangingText(t *testing.T) {
	if !motionEnabled() {
		t.Skip("motion is disabled in this environment")
	}
	for _, name := range bundledThemeNames {
		t.Run(name, func(t *testing.T) {
			th := themeFor(name)
			settled := brandWordmark(th, 0)
			plain := ansiStrip(settled)
			if plain != brandMark()+" Eitri" {
				t.Fatalf("settled brand plain = %q, want %q", plain, brandMark()+" Eitri")
			}
			shimmered := false
			for frame := 1; frame <= idleEmberWindow; frame++ {
				ember := brandWordmark(th, frame)
				if ansiStrip(ember) != plain {
					t.Fatalf("frame %d for %s changed the brand plain text: %q", frame, name, ansiStrip(ember))
				}
				if ember != settled {
					shimmered = true
				}
			}
			if !shimmered {
				t.Errorf("%s ember never changed the rendered brand across %d frames", name, idleEmberWindow)
			}
		})
	}
}
