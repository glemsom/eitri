package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func caretModel(t *testing.T) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "ok"}, nil
	}})
	return resize(t, m)
}

func caret(t *testing.T, m Model) tea.Cursor {
	t.Helper()
	c := m.View().Cursor
	if c == nil {
		t.Fatal("hardware caret must be attached while the composer is the active surface")
	}
	return *c
}

func newline(t *testing.T, m Model) Model {
	t.Helper()
	nm, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	return asModel(t, nm)
}

func TestComposer_HardwareCaretReplacesSoftwareCell(t *testing.T) {
	t.Parallel()
	m := caretModel(t)
	m = typeText(t, m, "hi")
	if strings.Contains(view(m), "\x1b[7m") {
		t.Errorf("composer must not render a software reverse-video caret cell, got:\n%s", view(m))
	}
	caret(t, m)
}

func TestComposer_CaretStylePolicy(t *testing.T) {
	t.Parallel()
	m := caretModel(t)
	c := caret(t, m)
	if c.Shape != tea.CursorBlock {
		t.Errorf("caret shape = %v, want block", c.Shape)
	}
	if c.Blink {
		t.Error("caret must be steady (no blink)")
	}
	if c.Color != nil {
		t.Errorf("caret color = %v, want nil (inherit terminal's configured cursor color); a non-nil color would emit a SetCursorColor sequence each frame", c.Color)
	}
}

func TestComposer_CaretTracksTyping(t *testing.T) {
	t.Parallel()
	m := caretModel(t)
	composerTop := lineCount(view(m)) - minComposerRows - 2
	if c := caret(t, m); c.X != 3 || c.Y != composerTop {
		t.Errorf("empty-composer caret = (%d,%d), want (3,%d)", c.X, c.Y, composerTop)
	}
	m = typeText(t, m, "hi")
	if c := caret(t, m); c.X != 5 || c.Y != composerTop {
		t.Errorf("caret after typing %q = (%d,%d), want (5,%d)", "hi", c.X, c.Y, composerTop)
	}
}

func TestComposer_CaretTracksWrappedDraft(t *testing.T) {
	t.Parallel()
	m := caretModel(t)
	m = typeText(t, m, strings.Repeat("a", 100)) // wraps to two composer rows
	if rows := composerRows(m); len(rows) < 2 {
		t.Fatalf("draft must wrap to at least two composer rows, got %d", len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "a")
}

func TestComposer_CaretTracksMultiLineDraft(t *testing.T) {
	t.Parallel()
	m := caretModel(t)
	m = typeText(t, m, "line one")
	m = newline(t, m)
	m = typeText(t, m, "line two")
	if rows := composerRows(m); len(rows) != 2 {
		t.Fatalf("two-line draft must grow the composer to 2 rows, got %d", len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "line two")
	m = asModel(t, mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyUp}))
	caretAtEndOfVisibleRow(t, m, "line one")
}

func TestComposer_CaretTracksInternalScroll(t *testing.T) {
	t.Parallel()
	m := caretModel(t)
	m = typeText(t, m, "a")
	for i := 0; i < 8; i++ {
		m = newline(t, m)
	}
	m = typeText(t, m, "z") // cursor: row 8, col 1
	if rows := composerRows(m); len(rows) != maxComposerRows {
		t.Fatalf("overflow draft must keep the composer at %d rows, got %d", maxComposerRows, len(rows))
	}
	caretAtEndOfVisibleRow(t, m, "z")
}

func TestComposer_CaretAbsentOnNonComposerSurfaces(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Models: []string{"deepseek-v4-flash"},
		Config: cfgFixture(),
	})
	m = resize(t, m)
	m = keypress(t, m, "ctrl+s")
	if c := m.View().Cursor; c != nil {
		t.Errorf("Settings surface must not attach a caret, got %+v", c)
	}
	m = asModel(t, mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}))
	m.prompting = true
	if c := m.View().Cursor; c != nil {
		t.Errorf("continuation prompt must not attach a caret, got %+v", c)
	}
}

func TestComposer_CaretHiddenWhileBusy(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	if c := m.View().Cursor; c != nil {
		t.Errorf("agent turn running must not attach a caret, got %+v", c)
	}
	m = mustUpdate(t, m, turnDoneMsg{prompt: "hi", answer: "ok"})
	if c := m.View().Cursor; c == nil {
		t.Error("completing the turn must restore the hardware caret")
	}
}

func TestComposer_CaretStaysAttachedOnCtrlD(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")

	if c := m.View().Cursor; c == nil {
		t.Fatal("composer must start with the hardware caret attached")
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if c := m.View().Cursor; c == nil {
		t.Error("ctrl+d (unbound) must not detach the composer's hardware caret")
	}
}

func caretAtEndOfVisibleRow(t *testing.T, m Model, needle string) {
	t.Helper()
	lines := frameLines(m)
	row := -1
	composerTop := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(ansiStrip(lines[i]), "Ask Eitri") {
			composerTop = i
			break
		}
	}
	for i := composerTop + 1; i < len(lines); i++ {
		plain := ansiStrip(lines[i])
		if strings.Contains(plain, "╰") || strings.Contains(plain, "+") {
			break
		}
		if strings.Contains(plain, needle) {
			row = i
		}
	}
	if row < 0 {
		t.Fatalf("no visible composer row contains %q; frame:\n%s", needle, view(m))
	}
	c := caret(t, m)
	plain := strings.TrimRight(ansiStrip(lines[row]), " ")
	plain = strings.TrimRight(strings.TrimSuffix(plain, "│"), " ")
	if want := plainWidth(plain); c.X != want {
		t.Errorf("caret X = %d, want end of visible row %d (%d); row %q", c.X, want, row, plain)
	}
	if c.Y != row {
		t.Errorf("caret Y = %d, want visible row %d; frame:\n%s", c.Y, row, view(m))
	}
}

func composerRows(m Model) []string {
	lines := frameLines(m)
	top := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(ansiStrip(lines[i]), "Ask Eitri") {
			top = i
			break
		}
	}
	if top < 0 {
		return nil
	}
	var rows []string
	for _, l := range lines[top+1:] {
		plain := strings.TrimRight(ansiStrip(l), " ")
		if strings.Contains(plain, "╰") {
			break
		}
		rows = append(rows, plain)
	}
	return rows
}

func frameLines(m Model) []string {
	return strings.Split(view(m), "\n")
}

func plainWidth(s string) int {
	return len([]rune(s))
}
