package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func idleEmberModel(t *testing.T) Model {
	t.Helper()
	return NewModelCfg(Dependencies{})
}

func TestIdleEmber_tickAdvancesFrame(t *testing.T) {
	m := idleEmberModel(t)
	if cmd := m.armIdleEmber(); cmd == nil {
		t.Fatal("idle ember must arm on an idle surface")
	}
	if m.tx.Status().EmberRemaining != idleEmberWindow {
		t.Fatalf("remaining = %d, want %d", m.tx.Status().EmberRemaining, idleEmberWindow)
	}
	m = upd(t, m, idleEmberTickMsg{})
	if m.tx.Status().EmberFrame != 1 {
		t.Fatalf("idle ember frame = %d, want 1 after one tick", m.tx.Status().EmberFrame)
	}
}

func TestIdleEmber_boundedWindowSettlesAndStops(t *testing.T) {
	m := idleEmberModel(t)
	m.tx.ArmEmber(false)
	var lastCmd tea.Cmd = func() tea.Msg { return nil }
	for i := 0; i < idleEmberWindow; i++ {
		nm, cmd := m.Update(idleEmberTickMsg{})
		m = asModel(t, nm)
		lastCmd = cmd
	}
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("bounded window must settle to the static mark, got frame=%d remaining=%d", m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
	if lastCmd != nil {
		t.Fatal("bounded window must stop re-arming the ember tick")
	}
}

func TestIdleEmber_typingReArms(t *testing.T) {
	m := idleEmberModel(t)
	m.tx.settleIdleEmber()
	m = typeText(t, m, "x")
	if m.tx.Status().EmberRemaining == 0 {
		t.Fatal("typing must re-arm the idle ember window")
	}
}

func TestIdleEmber_reducedMotionNeverArms(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := idleEmberModel(t)
	if cmd := m.armIdleEmber(); cmd != nil {
		t.Fatal("reduced motion must not arm the idle ember")
	}
	if m.tx.Status().EmberRemaining != 0 || m.tx.Status().EmberFrame != 0 {
		t.Fatalf("reduced motion left ember state frame=%d remaining=%d", m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_reducedMotionTickSettles(t *testing.T) {
	t.Setenv("EITRI_NO_MOTION", "1")
	m := idleEmberModel(t)
	m.tx.ArmEmber(false)
	m = upd(t, m, idleEmberTickMsg{})
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("reduced motion tick must settle, got frame=%d remaining=%d", m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_busySettles(t *testing.T) {
	m := idleEmberModel(t)
	m.tx.ArmEmber(false)
	m.tx.BeginBusy()
	m = upd(t, m, idleEmberTickMsg{})
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("busy surface must settle the ember, got frame=%d remaining=%d", m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_overlaySettles(t *testing.T) {
	m := idleEmberModel(t)
	m.tx.ArmEmber(false)
	m.prompting = true
	m = upd(t, m, idleEmberTickMsg{})
	if m.tx.Status().EmberFrame != 0 || m.tx.Status().EmberRemaining != 0 {
		t.Fatalf("open overlay must settle the ember, got frame=%d remaining=%d", m.tx.Status().EmberFrame, m.tx.Status().EmberRemaining)
	}
}

func TestIdleEmber_settledFrameEqualsReducedMotion(t *testing.T) {
	th := newDefaultTheme()
	settled := idleWelcome(th, 40, 0)
	t.Setenv("EITRI_NO_MOTION", "1")
	frozen := idleWelcome(th, 40, 9)
	if settled != frozen {
		t.Errorf("reduced-motion brand must equal the settled frame:\n settled=%q\n frozen =%q", settled, frozen)
	}
}

func TestIdleEmber_changesRenderedWelcome(t *testing.T) {
	m := idleEmberModel(t)
	m = resizeTo(t, m, 120, 40)
	settled := view(m)
	m.tx.ArmEmber(false)
	for range 5 {
		m = upd(t, m, idleEmberTickMsg{})
	}
	ember := view(m)
	if settled == ember {
		t.Fatal("idle ember frame must change the rendered welcome, not just its state")
	}
	if ansiStrip(settled) != ansiStrip(ember) {
		t.Error("idle ember must not change the welcome's plain text")
	}
}
