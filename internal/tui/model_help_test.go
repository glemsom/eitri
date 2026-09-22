package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestModel_slashHelpOpensOverlay(t *testing.T) {
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
	if m.help == nil {
		t.Fatal("`/help` should open the help overlay")
	}
	if len(m.tx.messages) != 0 {
		t.Fatalf("`/help` must not append to the transcript, got %d messages", len(m.tx.messages))
	}
	ref := strings.Join(m.help.lines, "\n")
	if !strings.Contains(ref, "COMMANDS") || !strings.Contains(ref, "KEYBINDINGS") {
		t.Fatalf("help overlay missing expected sections, got: %q", ref)
	}
	if v := view(m); !strings.Contains(v, "COMMANDS") {
		t.Fatalf("opened help overlay view missing its first section, got: %q", v)
	}

	m = keypress(t, m, "esc")
	if m.help != nil {
		t.Fatal("esc should close the help overlay")
	}
	if len(m.tx.messages) != 0 {
		t.Fatalf("closing help must not append to the transcript, got %d messages", len(m.tx.messages))
	}
}

func TestModel_helpOverlaySwallowsTyping(t *testing.T) {
	m := NewModelCfg(Dependencies{Config: cfgFixture()})
	m = resize(t, m)
	m = typeText(t, m, "/help")
	m = keypress(t, m, "enter")
	if m.help == nil {
		t.Fatal("help overlay should be open")
	}
	m = typeText(t, m, "zzz")
	if m.help == nil {
		t.Fatal("typing must not close the help overlay")
	}
	if got := m.composer.Value(); got != "" {
		t.Fatalf("typing under the help overlay reached the composer: %q", got)
	}
}

func TestModel_helpOverlayScrolls(t *testing.T) {
	m := NewModelCfg(Dependencies{Config: cfgFixture()})
	m = resize(t, m)
	m = typeText(t, m, "/help")
	m = keypress(t, m, "enter")
	if m.help == nil {
		t.Fatal("help overlay should be open")
	}
	m.help.height = 6 // a short viewport so the reference overflows and can scroll
	if m.help.maxOffset() == 0 {
		t.Fatal("test precondition: reference should overflow a 6-row viewport")
	}
	if m.help.offset != 0 {
		t.Fatalf("fresh overlay offset = %d, want 0", m.help.offset)
	}
	m = keypress(t, m, "down")
	if m.help.offset != 1 {
		t.Fatalf("down should scroll by one line, offset = %d", m.help.offset)
	}
	m = keypress(t, m, "up")
	if m.help.offset != 0 {
		t.Fatalf("up should scroll back to 0, offset = %d", m.help.offset)
	}
	nm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = asModel(t, nm)
	if m.help.offset != m.help.viewRows() {
		t.Fatalf("pgdown offset = %d, want one viewport %d", m.help.offset, m.help.viewRows())
	}
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = asModel(t, nm)
	if m.help.offset != m.help.maxOffset() {
		t.Fatalf("end offset = %d, want max %d", m.help.offset, m.help.maxOffset())
	}
	nm, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = asModel(t, nm)
	if m.help.offset != 0 {
		t.Fatalf("home offset = %d, want 0", m.help.offset)
	}
	if v := m.help.View(); !strings.Contains(v, "scroll") {
		t.Fatalf("help footer missing scroll hint, got: %q", v)
	}
}

func TestHelpOverlayViewFitsNarrowTerminal(t *testing.T) {
	for _, tc := range []struct {
		width, height int
	}{
		{0, 1},
		{0, 3},
		{1, 1},
		{1, 3},
		{12, 1},
		{12, 3},
		{20, 1},
		{20, 3},
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.width, tc.height), func(t *testing.T) {
			h := &HelpOverlay{
				width:  tc.width,
				height: tc.height,
				lines:  []string{strings.Repeat("x", 40), "second", "third"},
			}
			got := h.View()
			if tc.width == 0 {
				if got != "" {
					t.Fatalf("zero-width View() = %q, want empty", got)
				}
				return
			}
			if rows := lineCount(got); rows != tc.height {
				t.Fatalf("View() occupies %d rows, want height budget %d: %q", rows, tc.height, got)
			}
			for _, row := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
				if width := lipgloss.Width(row); width > tc.width {
					t.Errorf("row width %d exceeds terminal width %d: %q", width, tc.width, row)
				}
			}
		})
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

func TestModel_questionMarkFrozenWhileBusy(t *testing.T) {
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

	if got := m.composer.Value(); got != "" {
		t.Fatalf("composer = %q, want frozen empty draft (typing while busy must not mutate)", got)
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

func TestModel_helpOverlayLeavesTranscriptUntouched(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = typeText(t, m, "/help")
	m.tx.layout.dirty = false // isolate the open: the overlay must not dirty the transcript
	m = keypress(t, m, "enter")
	if m.help == nil {
		t.Fatal("`/help` should open the overlay")
	}
	if m.tx.layout.dirty {
		t.Error("opening `/help` as an overlay must not dirty the transcript layout")
	}
}
