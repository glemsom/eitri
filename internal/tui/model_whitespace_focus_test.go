package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel_whitespaceComposerTabCyclesBlockFocus(t *testing.T) {
	t.Parallel()
	m := resize(t, newStreamingModel())
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "reasoning")
	m.composer.SetValue(" \n")

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, nm)
	if !got.tx.focus.on {
		t.Fatal("whitespace-only composer should let Tab focus a transcript block")
	}
}

func TestModel_whitespaceComposerTabCyclesWhileBusy(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "reasoning")
	m.composer.SetValue(" \n")

	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	got := asModel(t, nm)
	if !got.tx.focus.on {
		t.Fatal("whitespace-only composer should let Tab focus a block while busy")
	}
}
