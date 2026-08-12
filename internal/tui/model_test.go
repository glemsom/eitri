package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModel_greetingRoundTrip drives a greeting through the model over the
// injected Turn seam (the engine stand-in) and asserts the assistant answer is
// appended to the conversation (ticket #34: "a TUI run of a greeting
// round-trips through the engine and renders the answer").
func TestModel_greetingRoundTrip(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (string, error) {
		if prompt != "hello" {
			t.Errorf("expected prompt 'hello', got %q", prompt)
		}
		return "Hello! **glad** to help.", nil
	})

	// Set a size so the composer has a width.
	m = resize(t, m)

	// Focus + type the prompt, then submit.
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	view := m.View()
	if !strings.Contains(view, "you") || !strings.Contains(view, "eitri") {
		t.Errorf("expected both role headers in view, got: %q", view)
	}
	// The assistant's markdown answer must render (bold "glad" carries SGR 1).
	if !strings.Contains(view, "glad") {
		t.Errorf("expected assistant answer text in view, got: %q", view)
	}
	if !hasSGRBold(m.View()) {
		t.Errorf("expected markdown bold to render in assistant answer, got: %q", m.View())
	}
	// The TUI renders into the primary buffer by default: the conversation view
	// must never carry a clear-screen (\x1b[2J) or alt-screen (\x1b[?1049)
	// sequence, so native scrollback/selection/search survive (docs/spec.md §9).
	if strings.Contains(view, "\x1b[2J") {
		t.Errorf("view carries a clear-screen sequence; must render to primary buffer")
	}
	if strings.Contains(view, "\x1b[?1049") {
		t.Errorf("view carries an alt-screen sequence; must render to primary buffer")
	}
}

// TestModel_errorTurn asserts a failing turn renders a visible error instead of
// silently dropping.
func TestModel_errorTurn(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("provider exploded")
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	if !strings.Contains(m.View(), "provider") || !strings.Contains(m.View(), "exploded") {
		t.Errorf("expected error words in view, got: %q", m.View())
	}
}

// resize installs a window size on the model.
func resize(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return asModel(t, nm)
}

// typeText feeds the given runes to the composer in one keypress.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return asModel(t, nm)
}

// submitAndWait feeds Enter to run the turn and then the async completion.
func submitAndWait(t *testing.T, m Model) Model {
	t.Helper()
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("turn command was nil after submit")
	}
	out := asModel(t, nm)
	// Run the command to completion synchronously: it returns the done message.
	done := cmd()
	out2, _ := out.Update(done)
	return asModel(t, out2)
}

func asModel(t *testing.T, tm tea.Model) Model {
	t.Helper()
	md, ok := tm.(Model)
	if !ok {
		t.Fatalf("tea.Model is %T, want Model", tm)
	}
	return md
}
