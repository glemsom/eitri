package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestModel_blockFocusWorksWhileBusy locks the empty-composer Tab/Enter path:
// navigating transcript blocks while a turn streams is deliberate and must
// survive the busy gate (the gate swallows only composer-mutating keys).
func TestModel_blockFocusWorksWhileBusy(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m, _ = submitBusy(t, m)

	// A streamed reasoning delta gives the busy transcript a collapsible block.
	m = applyReasoningDelta(t, m, "reasoning while busy")

	m = keypress(t, m, "tab")
	if _, ok := m.tx.focused(); !ok {
		t.Fatal("Tab with an empty composer must focus a collapsible block while busy")
	}
	m = keypress(t, m, "enter")
	if _, ok := m.tx.focused(); !ok {
		t.Fatal("Enter must keep the focused block while busy")
	}
	if v := m.composer.Value(); v != "" {
		t.Errorf("block-focus navigation must not touch the composer, value = %q", v)
	}
}

// TestModel_composerFrozenWhileBusy is the regression lock for the zombie
// draft: while a turn streams, the composer is hidden behind the forge panel
// and its caret is gone, so composer-mutating keys used to edit an invisible
// draft that resurfaced once the turn committed. Typing during a run must
// mutate nothing; after the commit the composer shows exactly what submit time
// left behind (cleared by submitPrompt).
func TestModel_composerFrozenWhileBusy(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m, _ = submitBusy(t, m)

	if v := m.composer.Value(); v != "" {
		t.Fatalf("composer must be cleared at submit time, value = %q", v)
	}

	// Keys that would edit the hidden composer while the turn runs: printable
	// text, space, a literal-tab insert (non-empty draft path), and deletion.
	presses := []tea.KeyPressMsg{
		{Code: tea.KeyExtended, Text: "z"},
		{Code: tea.KeySpace},
		{Code: tea.KeyTab},
		{Code: tea.KeyBackspace},
	}
	for _, press := range presses {
		m = asModel(t, mustUpdate(t, m, press))
		if v := m.composer.Value(); v != "" {
			t.Fatalf("key %v mutated the composer while busy, value = %q", press, v)
		}
	}

	// Commit the turn: no zombie draft may resurface in the now-visible composer.
	m = asModel(t, mustUpdate(t, m, turnDoneMsg{prompt: "hello", answer: "final answer"}))
	if m.tx.busy {
		t.Fatal("turn commit must clear the busy state")
	}
	if v := m.composer.Value(); v != "" {
		t.Errorf("zombie draft resurfaced after commit, value = %q", v)
	}
}
