package tui

import (
	"context"
	"strings"
	"testing"
)

// TestModel_slashHelpAppendsMessage verifies `/help` appends a message to
// tx.messages containing the help content and never reaches the engine.
func TestModel_slashHelpAppendsMessage(t *testing.T) {
	var prompted string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			prompted = prompt
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "/help")
	m = keypress(t, m, "enter")

	if prompted != "" {
		t.Fatalf("`/help` must not reach the engine, got prompt %q", prompted)
	}
	if len(m.tx.messages) == 0 {
		t.Fatal("`/help` should append a message to the transcript")
	}
	last := m.tx.messages[len(m.tx.messages)-1]
	if last.role != "eitri" {
		t.Fatalf("last message role = %q, want eitri", last.role)
	}
	if !strings.Contains(last.content, "COMMANDS") || !strings.Contains(last.content, "KEYBINDINGS") {
		t.Fatalf("help message missing expected sections, got: %q", last.content)
	}
}

// TestModel_questionMarkIdleAppendsHelp verifies pressing `?` when idle and the
// composer is empty appends the help message to the transcript.
func TestModel_questionMarkIdleAppendsHelp(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "?")

	if len(m.tx.messages) == 0 {
		t.Fatal("`?` while idle with empty composer should append a help message")
	}
	last := m.tx.messages[len(m.tx.messages)-1]
	if last.role != "eitri" {
		t.Fatalf("last message role = %q, want eitri", last.role)
	}
	if !strings.Contains(last.content, "COMMANDS") {
		t.Fatalf("help message missing COMMANDS section, got: %q", last.content)
	}
}

// TestModel_questionMarkBusyDoesNothing verifies pressing `?` while a turn is
// running does nothing — no help message, no key insertion.
func TestModel_questionMarkBusyDoesNothing(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m, _ = submitBusy(t, m)

	before := len(m.tx.messages)
	m = keypress(t, m, "?")
	after := len(m.tx.messages)

	if after != before {
		t.Fatalf("`?` while busy should not append messages: before=%d after=%d", before, after)
	}
}

// TestModel_questionMarkWithTextInsertsLiteral verifies pressing `?` when the
// composer has text inserts a literal `?` character instead of showing help.
func TestModel_questionMarkWithTextInsertsLiteral(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")

	m = keypress(t, m, "?")

	got := m.composer.Value()
	if got != "hello?" {
		t.Fatalf("composer = %q, want hello?", got)
	}
	if len(m.tx.messages) != 0 {
		t.Fatalf("`?` with text should not append help messages, got %d messages", len(m.tx.messages))
	}
}

// TestModel_slashHelpInTabCompletion verifies `/help` appears in the
// tab-completion list alongside the other built-in commands.
func TestModel_slashHelpInTabCompletion(t *testing.T) {
	cands := slashCandidates("/", nil)
	found := false
	for _, c := range cands {
		if c == "/help" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bare `/` completion should list `/help`, got: %v", cands)
	}
}

// TestModel_slashHelpPartialCompletion verifies `/he` completes to `/help`.
func TestModel_slashHelpPartialCompletion(t *testing.T) {
	cands := slashCandidates("/he", nil)
	if len(cands) != 1 || cands[0] != "/help" {
		t.Fatalf("`/he` completion should match only `/help`, got: %v", cands)
	}
}
