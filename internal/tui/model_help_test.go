package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

	mq := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	mq = resize(t, mq)
	mq.tx.layout.dirty = false // isolate the append: only the seam may re-mark it
	mq = keypress(t, mq, "?")
	if !mq.tx.layout.dirty {
		t.Error("`?` must mark the transcript layout dirty so the help block re-wraps")
	}
}

func TestModel_helpPathsRenderIdenticallyAtWidth(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	const width, height = 80, 24

	helpModel := func(t *testing.T, path string) (Model, string) {
		t.Helper()
		m := NewModelCfg(Dependencies{
			Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
				return TurnResult{Answer: "ok"}, nil
			},
			Config: cfgFixture(),
		})
		m = resizeTo(t, m, width, height)
		switch path {
		case "slash":
			m = typeText(t, m, "/help")
			m = keypress(t, m, "enter")
		case "qmark":
			m = keypress(t, m, "?")
		default:
			t.Fatalf("unknown help path %q", path)
		}
		if len(m.tx.messages) == 0 {
			t.Fatalf("help path %q appended no message", path)
		}
		return m, m.tx.messages[len(m.tx.messages)-1].content
	}

	slashM, slashContent := helpModel(t, "slash")
	qM, qContent := helpModel(t, "qmark")

	if slashContent != qContent {
		t.Fatalf("`/help` and `?` must append identical help content:\n--- slash ---\n%q\n--- q ---\n%q", slashContent, qContent)
	}

	slashView := view(slashM)
	qView := view(qM)
	if slashView != qView {
		t.Fatalf("`/help` and `?` must render the same surface:\n--- slash ---\n%s\n--- q ---\n%s", slashView, qView)
	}
	for i, line := range strings.Split(slashView, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("help line %d width %d exceeds terminal %d: %q", i, w, width, line)
		}
	}
}

func TestModel_helpCopyIsEscapeFree(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "/help")
	m = keypress(t, m, "enter")

	if len(m.tx.messages) == 0 {
		t.Fatal("`/help` should append a message to the transcript")
	}
	got := m.transcriptText()
	if !strings.Contains(got, "COMMANDS") || !strings.Contains(got, "/copy") {
		t.Fatalf("copied transcript missing help sections, got: %q", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("copied transcript must be ANSI-free, got escapes:\n%q", got)
	}
}
