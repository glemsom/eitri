package tui

import (
	"context"
	"strings"
	"testing"
)

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

func TestModel_questionMarkEditsEmptyComposer(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "?")

	if got := m.composer.Value(); got != "?" {
		t.Fatalf("composer = %q, want literal question mark", got)
	}
	if len(m.tx.messages) != 0 {
		t.Fatalf("`?` should not append help, got %d messages", len(m.tx.messages))
	}
}

func TestModel_questionMarkEditsComposerWhileBusy(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hello")
	m, _ = submitBusy(t, m)
	m = keypress(t, m, "?")

	if got := m.composer.Value(); got != "?" {
		t.Fatalf("composer = %q, want literal question mark", got)
	}
}

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

func TestModel_slashHelpPartialCompletion(t *testing.T) {
	cands := slashCandidates("/he", nil)
	if len(cands) != 1 || cands[0] != "/help" {
		t.Fatalf("`/he` completion should match only `/help`, got: %v", cands)
	}
}

func TestModel_helpAppendMarksLayoutDirty(t *testing.T) {
	mslash := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	mslash = resize(t, mslash)
	mslash = typeText(t, mslash, "/help")
	mslash.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	mslash = keypress(t, mslash, "enter")
	if !mslash.tx.layout.dirty {
		t.Error("`/help` must mark the transcript layout dirty so the help block re-wraps")
	}

}
