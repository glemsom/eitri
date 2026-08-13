package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestModel_ctrlOCopiesTranscript drives a one-turn conversation, presses
// Ctrl+O, and asserts the full plain-text transcript reaches the clipboard seam
// and the band reports the copy (issue #123 AC1): the user prompt, the
// assistant answer, and the per-turn reasoning block are all copied, with no
// ANSI styling leaking into the pasted text.
func TestModel_ctrlOCopiesTranscript(t *testing.T) {
	var copied string
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "I reason first."}, nil
		},
		Clipboard: func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m)

	if !strings.Contains(copied, "you: hello") {
		t.Errorf("copied text missing the user prompt, got: %q", copied)
	}
	if !strings.Contains(copied, "eitri: plain answer") {
		t.Errorf("copied text missing the assistant answer, got: %q", copied)
	}
	if !strings.Contains(copied, "I reason first") {
		t.Errorf("copied text missing the reasoning block, got: %q", copied)
	}
	if strings.Contains(copied, "\x1b[") {
		t.Errorf("copied text must be ANSI-free plain text, got: %q", copied)
	}
	if !strings.Contains(view(m), "copied") {
		t.Errorf("expected a copy success note in view, got: %q", view(m))
	}
}

// TestModel_copySlashCopiesTranscript drives `/copy` through the slash-command
// surface: the transcript is copied and the command never reaches the engine
// seam as a prompt (issue #123 AC2).
func TestModel_copySlashCopiesTranscript(t *testing.T) {
	var copied string
	var prompted []string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string) (TurnResult, error) {
			prompted = append(prompted, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(s string) error { copied = s; return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = typeText(t, m, "/copy")
	m = keypress(t, m, "enter")

	if !strings.Contains(copied, "you: hello") {
		t.Errorf("`/copy` should copy the transcript, got: %q", copied)
	}
	for _, p := range prompted {
		if p == "/copy" {
			t.Errorf("`/copy` must not reach the engine seam, got prompt %q", p)
		}
	}
	if !strings.Contains(view(m), "copied") {
		t.Errorf("expected a copy success note in view, got: %q", view(m))
	}
}

// TestModel_copyFailureReportsNote asserts a clipboard failure surfaces as a
// visible status note instead of failing silently (issue #123 AC3).
func TestModel_copyFailureReportsNote(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(string) error { return errors.New("no display") },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)

	m = keypressCtrlO(t, m)

	if !strings.Contains(view(m), "copy failed") {
		t.Errorf("expected a copy failure note in view, got: %q", view(m))
	}
}

// TestModel_copyDoesNotMutateConversation asserts copying never touches the
// transcript or the agent loop: no message is added/removed/altered and the
// model stays out of the busy state (issue #123 AC4).
func TestModel_copyDoesNotMutateConversation(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Clipboard: func(string) error { return nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m = submitAndWait(t, m)
	before := m.messages

	m = keypressCtrlO(t, m)

	if len(m.messages) != len(before) {
		t.Errorf("copy changed the message count: got %d, want %d", len(m.messages), len(before))
	}
	for i := range before {
		if before[i] != m.messages[i] {
			t.Errorf("message %d changed by copy: got %+v, want %+v", i, m.messages[i], before[i])
		}
	}
	if m.busy {
		t.Errorf("copy must not mark the model busy")
	}
}

// TestModel_copySlashShowsInCompletion asserts a bare `/` lists the built-in
// /copy command alongside /settings, and tab cycles to it (issue #123 AC2:
// the copy command is discoverable from the command surface).
func TestModel_copySlashShowsInCompletion(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
	})
	m = resize(t, m)
	m = typeText(t, m, "/")
	if !strings.Contains(view(m), "/copy") {
		t.Errorf("bare `/` completion should list /copy, got: %q", view(m))
	}

	// Tab cycles /settings then /copy.
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/settings" {
		t.Fatalf("first tab completion = %q, want /settings", got)
	}
	m = keypress(t, m, "tab")
	if got := m.composer.Value(); got != "/copy" {
		t.Errorf("second tab completion = %q, want /copy", got)
	}
}

// keypressCtrlO sends Ctrl+O to the model and returns the updated model.
func keypressCtrlO(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	return asModel(t, nm)
}
