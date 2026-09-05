package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// arrowHistoryModel builds a model with a simple recordable prompt history and
// no per-key outer surface (no menu, no continuation prompt, idle turn).
func arrowHistoryModel(t *testing.T) Model {
	t.Helper()
	m := newStreamingModel()
	m = resize(t, m)
	return m
}

// pushHistory submits the given drafts, populating the model's record ring.
func pushHistory(t *testing.T, m Model, prompts ...string) Model {
	t.Helper()
	for _, p := range prompts {
		m = typeText(t, m, p)
		m = submitAndWait(t, m)
	}
	return m
}

func TestArrowRecall_upRecallsPriorPrompt(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "first", "second")

	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "second" {
		t.Fatalf("up with empty draft should recall newest prompt, got %q", v)
	}
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "first" {
		t.Fatalf("second up should walk to the older prompt, got %q", v)
	}
}

func TestArrowRecall_upDownCycle(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "a", "b")

	m = keypress(t, m, "up")
	m = keypress(t, m, "up")
	m = keypress(t, m, "up") // a again: recalling the oldest stays put
	if v := m.composer.Value(); v != "a" {
		t.Fatalf("further up beyond oldest should keep oldest, got %q", v)
	}
	m = keypress(t, m, "down")
	if v := m.composer.Value(); v != "b" {
		t.Fatalf("down from oldest should move forward to the next newer prompt, got %q", v)
	}
}

func TestArrowRecall_downFromNeutralDoesNothing(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "saved")
	m = typeText(t, m, "draft")
	m = keypress(t, m, "down") // not recalling: down must not clobber the draft
	if v := m.composer.Value(); v != "draft" {
		t.Fatalf("down outside recall must leave the draft alone, got %q", v)
	}
}

func TestArrowRecall_downRestoresDraft(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "saved")
	m = typeText(t, m, "draft")

	m = keypress(t, m, "up") // recall "saved" into the draft
	if v := m.composer.Value(); v != "saved" {
		t.Fatalf("up should overwrite the draft with the recalled prompt, got %q", v)
	}
	m = keypress(t, m, "down") // down past the newest returns to the draft under recall
	if v := m.composer.Value(); v != "draft" {
		t.Fatalf("down should restore the pre-recall draft, got %q", v)
	}
}

func TestArrowRecall_shiftArrowsNeverRecall(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "prior")
	m = typeText(t, m, "draft")
	// caret at top of a single-line draft
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if v := m.composer.Value(); v != "draft" {
		t.Fatalf("shift+up must not recall, got draft %q", v)
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift})
	if v := m.composer.Value(); v != "draft" {
		t.Fatalf("shift+down must not recall, got draft %q", v)
	}
}

func TestArrowRecall_onlyAtCaretEdge(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "prior")
	m = typeText(t, m, "two\nlines")
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyUp}) // caret on line two -> move caret, no recall
	if v := m.composer.Value(); v != "two\nlines" {
		t.Fatalf("up off the top line must not recall, got draft %q", v)
	}
	// move caret to top line (line 0), recall on next up
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "prior" {
		t.Fatalf("up on the top line should recall, got %q", v)
	}
}

func TestArrowRecall_blockedWhileBusy(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = pushHistory(t, m, "prior")
	m = typeText(t, m, "x")
	m, _ = submitBusy(t, m) // running turn
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "" {
		t.Fatalf("recall while busy must not fire, got draft %q", v)
	}
}

func TestArrowRecall_slashLineRecalledInert(t *testing.T) {
	t.Parallel()
	var turnPrompts []string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, prompt string, _ string) (TurnResult, error) {
			turnPrompts = append(turnPrompts, prompt)
			return TurnResult{Answer: "ok"}, nil
		},
		Skills: &SkillsSurface{
			Items:    []SkillItem{{Name: "review"}},
			Activate: func(_ context.Context, name string) (string, error) { return "payload", nil },
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/review")
	m = submitAndWait(t, m)
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "/review" {
		t.Fatalf("up should recall the recorded /skill line, got %q", v)
	}
	// inert: must not route until Enter
	if m.skillPending {
		t.Fatalf("recalled /skill line must be inert until Enter")
	}
}

func TestArrowRecall_slashLineSubmitsOnEnter(t *testing.T) {
	t.Parallel()
	var activated []string
	m := NewModelCfg(Dependencies{
		Turn: func(_ context.Context, _ string, _ string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Skills: &SkillsSurface{
			Items: []SkillItem{{Name: "review"}},
			Activate: func(_ context.Context, name string) (string, error) {
				activated = append(activated, name)
				return "payload", nil
			},
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "/review")
	m = submitAndWait(t, m)
	m = keypress(t, m, "up")
	m = submitAndWait(t, m)
	// first submit activates once; the recall + Enter must route through the
	// slash path a second time rather than being submitted as a bare prompt.
	if len(activated) != 2 {
		t.Errorf("Enter on recalled /skill line should route through the slash path, got activations %v", activated)
	}
	if len(activated) == 2 && (activated[0] != "review" || activated[1] != "review") {
		t.Errorf("recalled /skill Enter routed wrong activations, got %v", activated)
	}
}

func TestArrowRecall_editingEndsRecall(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "older", "newer")
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "newer" {
		t.Fatalf("up should recall newest, got %q", v)
	}
	m = typeText(t, m, "x") // editing cancels recall
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "newer" {
		t.Fatalf("editing then up should recall newest again, got %q", v)
	}
}

func TestArrowRecall_escapeEndsRecall(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "older", "newer")
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "newer" {
		t.Fatalf("up should recall newest, got %q", v)
	}
	m = keypress(t, m, "esc")
	// esc ends the recall cursor without touching the recalled draft.
	if v := m.composer.Value(); v != "newer" {
		t.Fatalf("esc should leave the recalled draft intact, got %q", v)
	}
	m = keypress(t, m, "up")
	if v := m.composer.Value(); v != "newer" {
		t.Fatalf("esc then up should recall newest again, got %q", v)
	}
}

func TestArrowRecall_notRecordedItself(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "prior")
	m = keypress(t, m, "up") // recall "prior" but do not submit
	if v := m.composer.Value(); v != "prior" {
		t.Fatalf("up should recall, got %q", v)
	}
	if got := m.history.Entries(); !equalStrings(got, []string{"prior"}) {
		t.Fatalf("recalling without submitting must not mutate history, got %v", got)
	}
}

func TestArrowRecall_multilinePromptShowsStart(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	prompt := "first recalled line\nsecond recalled line\nthird recalled line"
	m = pushHistory(t, m, prompt)

	m = keypress(t, m, "up")
	composer := m.composer.View()
	if !strings.Contains(composer, "first recalled line") {
		t.Fatalf("recalled multiline prompt should show its start, composer view:\n%s", composer)
	}
}

func TestArrowRecall_wrappedDraftTopEdge(t *testing.T) {
	t.Parallel()
	m := arrowHistoryModel(t)
	m = pushHistory(t, m, "prior")
	m = typeText(t, m, strings.Repeat("a", 100)) // soft-wraps to >= 2 rows
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	if v := m.composer.Value(); v != "prior" {
		t.Fatalf("up on a soft-wrapped single logical line should recall, got %q", v)
	}
}
