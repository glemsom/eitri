package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/glemsom/eitri/internal/persona"
)

// personaUIState is a snapshot of the header persona selector: the active
// persona label, the trigger's aria-expanded state, whether the dropdown is
// hidden, and what element currently has focus.
type personaUIState struct {
	Label          string `json:"label"`
	Expanded       string `json:"expanded"`
	DropdownHidden bool   `json:"dropdownHidden"`
	FocusText      string `json:"focusText"`
	FocusRole      string `json:"focusRole"`   // "option" for a dropdown option
	FocusTarget    string `json:"focusTarget"` // "trigger" | null
}

// readPersonaUIState inspects the live #persona-selector DOM (it always
// queries the current element, so it stays correct across htmx re-renders).
func readPersonaUIState(t *testing.T, ctx context.Context) personaUIState {
	t.Helper()
	var s personaUIState
	err := chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`(function() {
			var selector = document.getElementById('persona-selector');
			if (!selector) return {};
			var trigger = selector.querySelector('[data-ps-target="trigger"]');
			var dropdown = selector.querySelector('[data-ps-target="dropdown"]');
			var active = document.activeElement;
			var name = active && active.querySelector ? active.querySelector('.persona-option-name') : null;
			return {
				label: trigger.querySelector('.persona-trigger-label').textContent.trim(),
				expanded: trigger.getAttribute('aria-expanded'),
				dropdownHidden: !!dropdown.hidden,
				focusText: name ? name.textContent.trim() : (active && active.textContent ? active.textContent.trim() : ''),
				focusRole: active && active.getAttribute ? active.getAttribute('role') : null,
				focusTarget: active && active.getAttribute ? active.getAttribute('data-ps-target') : null
			};
		})()`, &s),
	)
	if err != nil {
		t.Fatalf("read persona UI state failed: %v", err)
	}
	return s
}

// TestBrowser_PersonaDropdownKeyboard verifies the header persona selector is
// fully operable by keyboard (issue #1074). A second persona ("assistant") is
// seeded so arrow navigation is meaningful; the active persona is "generic"
// which sorts last, so the listbox opens on the selected option and arrows
// wrap through both options.
func TestBrowser_PersonaDropdownKeyboard(t *testing.T) {
	llmURL := testLLMURL(t)
	m := newManagedTestServerWithRuns(t)
	server := m.server
	configureProvider(t, server, llmURL)

	if err := persona.SaveToHome(m.homeDir, &persona.PersonaDefinition{
		Name:         "assistant",
		SystemPrompt: "You are an assistant.",
	}); err != nil {
		t.Fatalf("seed assistant persona: %v", err)
	}

	ctx, cancel := newBrowserCtx(t, server.URL)
	defer cancel()

	const (
		trigger = "#persona-selector [data-ps-target=trigger]"
		listbox = "#persona-selector #persona-listbox"
	)

	err := chromedp.Run(ctx,
		chromedp.Navigate(server.URL+"/"),
		chromedp.WaitVisible("#chat-view", chromedp.ByQuery),
		chromedp.WaitVisible("#persona-selector", chromedp.ByQuery),
	)
	if err != nil {
		t.Fatalf("navigate to chat failed: %v", err)
	}
	waitForComposerReady(t, ctx)

	// AC2 — the trigger announces the popup and the options expose their
	// selection state to screen readers.
	var a11y struct {
		HasPopup      string `json:"hasPopup"`
		Expanded      string `json:"expanded"`
		Controls      string `json:"controls"`
		OptionCount   int    `json:"optionCount"`
		SelectedCount int    `json:"selectedCount"`
		FirstSelected string `json:"firstSelected"`
	}
	err = chromedp.Run(ctx, chromedp.EvaluateAsDevTools(`(function() {
		var trigger = document.querySelector('[data-ps-target="trigger"]');
		var options = Array.prototype.slice.call(document.querySelectorAll('#persona-selector [role="option"]'));
		var selected = options.filter(function (o) { return o.getAttribute('aria-selected') === 'true'; });
		return {
			hasPopup: trigger.getAttribute('aria-haspopup'),
			expanded: trigger.getAttribute('aria-expanded'),
			controls: trigger.getAttribute('aria-controls'),
			optionCount: options.length,
			selectedCount: selected.length,
			firstSelected: selected.length > 0 ? selected[0].querySelector('.persona-option-name').textContent.trim() : ''
		};
	})()`, &a11y))
	if err != nil {
		t.Fatalf("read trigger ARIA attributes failed: %v", err)
	}
	if a11y.HasPopup != "listbox" {
		t.Errorf("trigger aria-haspopup = %q, want %q", a11y.HasPopup, "listbox")
	}
	if a11y.Expanded != "false" {
		t.Errorf("closed trigger aria-expanded = %q, want %q", a11y.Expanded, "false")
	}
	if a11y.Controls != "persona-listbox" {
		t.Errorf("trigger aria-controls = %q, want %q", a11y.Controls, "persona-listbox")
	}
	if a11y.OptionCount != 2 {
		t.Errorf("dropdown option count = %d, want 2 (assistant + generic)", a11y.OptionCount)
	}
	if a11y.SelectedCount != 1 || a11y.FirstSelected != "generic" {
		t.Errorf("exactly one option must be aria-selected (the active persona), selected=%d first=%q", a11y.SelectedCount, a11y.FirstSelected)
	}

	// AC1 — ArrowDown on the closed trigger opens the listbox and moves focus
	// to the selected option.
	if err := chromedp.Run(ctx,
		chromedp.SendKeys(trigger, kb.ArrowDown, chromedp.ByQuery),
		chromedp.WaitVisible(listbox, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open dropdown with ArrowDown failed: %v", err)
	}
	state := readPersonaUIState(t, ctx)
	if state.FocusRole != "option" || state.FocusText != "generic" {
		t.Fatalf("after opening, focus should be on the selected option 'generic', got role=%q text=%q", state.FocusRole, state.FocusText)
	}
	if state.Expanded != "true" {
		t.Errorf("open dropdown aria-expanded = %q, want %q", state.Expanded, "true")
	}

	// Arrow navigation: ArrowDown wraps to the first option, ArrowUp returns,
	// Home/End jump to the extremes.
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.ArrowDown)); err != nil {
		t.Fatalf("ArrowDown failed: %v", err)
	}
	state = readPersonaUIState(t, ctx)
	if state.FocusText != "assistant" {
		t.Errorf("ArrowDown should move to 'assistant', focus is on %q", state.FocusText)
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.ArrowUp)); err != nil {
		t.Fatalf("ArrowUp failed: %v", err)
	}
	state = readPersonaUIState(t, ctx)
	if state.FocusText != "generic" {
		t.Errorf("ArrowUp should move back to 'generic', focus is on %q", state.FocusText)
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.Home)); err != nil {
		t.Fatalf("Home failed: %v", err)
	}
	state = readPersonaUIState(t, ctx)
	if state.FocusText != "assistant" {
		t.Errorf("Home should jump to the first option 'assistant', focus is on %q", state.FocusText)
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.End)); err != nil {
		t.Fatalf("End failed: %v", err)
	}
	state = readPersonaUIState(t, ctx)
	if state.FocusText != "generic" {
		t.Errorf("End should jump to the last option 'generic', focus is on %q", state.FocusText)
	}

	// AC1 — Escape closes the dropdown and returns focus to the trigger.
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.Escape)); err != nil {
		t.Fatalf("Escape failed: %v", err)
	}
	state = readPersonaUIState(t, ctx)
	if !state.DropdownHidden {
		t.Error("Escape should close the dropdown")
	}
	if state.Expanded != "false" {
		t.Errorf("after Escape aria-expanded = %q, want %q", state.Expanded, "false")
	}
	if state.FocusTarget != "trigger" {
		t.Errorf("Escape should return focus to the trigger, focus is on target=%q text=%q", state.FocusTarget, state.FocusText)
	}

	// Space reopens from the trigger; select the other persona with the
	// keyboard (ArrowUp wraps from the selected option, Enter activates).
	if err := chromedp.Run(ctx,
		chromedp.SendKeys(trigger, " ", chromedp.ByQuery),
		chromedp.WaitVisible(listbox, chromedp.ByQuery),
		chromedp.KeyEvent(kb.ArrowUp),
		chromedp.KeyEvent(kb.Enter),
	); err != nil {
		t.Fatalf("select persona with keyboard failed: %v", err)
	}

	// The activation htmx-swaps #persona-selector; wait for the fresh trigger
	// to show the new label with focus handed back to it.
	pollForCondition(t, 10*time.Second, 50*time.Millisecond, func() bool {
		state = readPersonaUIState(t, ctx)
		return state.Label == "assistant" && state.FocusTarget == "trigger" && state.DropdownHidden
	})
	if state.Label != "assistant" {
		t.Errorf("after Enter selection the trigger label = %q, want %q", state.Label, "assistant")
	}
	if state.FocusTarget != "trigger" {
		t.Errorf("after selection focus should return to the re-created trigger, focus is on target=%q text=%q", state.FocusTarget, state.FocusText)
	}
	if !state.DropdownHidden {
		t.Error("after selection the dropdown should be closed")
	}

	// AC3 — click-outside still closes the open dropdown (no regression), and
	// the fresh selector remains keyboard-operable afterwards.
	if err := chromedp.Run(ctx,
		chromedp.SendKeys(trigger, kb.ArrowDown, chromedp.ByQuery),
		chromedp.WaitVisible(listbox, chromedp.ByQuery),
		chromedp.Click("body", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("open + click outside failed: %v", err)
	}
	pollForCondition(t, 5*time.Second, 50*time.Millisecond, func() bool {
		state = readPersonaUIState(t, ctx)
		return state.DropdownHidden
	})
	if !state.DropdownHidden {
		t.Error("clicking outside the open dropdown should close it (issue #1069 regression check)")
	}
}
