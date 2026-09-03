package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/glemsom/eitri/internal/config"
)

func streamingTurn(ctx context.Context, prompt string, _ string) (TurnResult, error) {
	return TurnResult{Answer: "IGNORED"}, nil
}

func submitBusy(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	if m.tx.busy {
		t.Fatalf("model already busy")
	}
	nm, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a turn command on submit")
	}
	out := asModel(t, nm)
	if !out.tx.busy {
		t.Fatalf("model should be busy after submit")
	}
	return out, cmd
}

func applyDelta(t *testing.T, m Model, delta string) Model {
	t.Helper()
	nm, _ := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: AnswerStream, Delta: delta}}})
	return asModel(t, nm)
}

func newStreamingModel() Model {
	return NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Events: NewEventFeed(),
		Config: config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true, ToolResultsCollapsedByDefault: true},
	})
}

func TestModel_streamAnswerGrowsInPlace(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyDelta(t, m, "Hello **glad**")
	content := view(m)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "Hello **glad**" {
		t.Errorf("first delta content = %q, want %q", got, "Hello **glad**")
	}
	if !hasSGRBold(content) {
		t.Errorf("expected partial markdown bold to render, got: %q", content)
	}

	m = applyDelta(t, m, " to help.")
	view2 := view(m)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "Hello **glad** to help." {
		t.Errorf("second delta content = %q, want %q", got, "Hello **glad** to help.")
	}
	if strings.Contains(view2, "Hello **glad**") {
		t.Errorf("raw markdown must not leak into the content, got: %q", view2)
	}
}

func TestModel_streamFinalize(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyDelta(t, m, "Hello ")
	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "Hello **glad** to help."})
	m = asModel(t, nm)

	content := view(m)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "Hello **glad** to help." {
		t.Errorf("final content = %q, want full answer", got)
	}
	if !hasSGRBold(content) {
		t.Errorf("expected final markdown bold to render, got: %q", content)
	}
	if m.tx.busy {
		t.Errorf("completion must clear the busy state")
	}
}

func TestModel_streamFallbackWithoutFeed(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
		return TurnResult{Answer: "plain answer"}, nil
	}})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	if got := m.tx.messages[len(m.tx.messages)-1].content; got != "plain answer" {
		t.Errorf("expected non-streaming answer content, got %q", got)
	}
}

func applyReasoningDelta(t *testing.T, m Model, delta string) Model {
	t.Helper()
	nm, _ := m.Update(eventMsg{update: Event{Stream: &StreamUpdate{Kind: ReasoningStream, Delta: delta}}})
	return asModel(t, nm)
}

func TestModel_thinkingStreamsLive(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyReasoningDelta(t, m, "I check the env.")
	m = applyReasoningDelta(t, m, " Then I edit.")
	n := len(m.tx.messages) - 1
	if got := m.tx.messages[n].reasoning; got != "I check the env. Then I edit." {
		t.Errorf("streamed reasoning = %q, want accumulated thinking", got)
	}
	if m.tx.messages[n].content != "" {
		t.Errorf("reasoning must not write into the answer buffer, got %q", m.tx.messages[n].content)
	}
	content := view(m)
	if !strings.Contains(content, "🤔") {
		t.Errorf("live reasoning should render a thinking hint, got: %q", content)
	}
	plain := ansiStrip(content)
	// Contiguous reasoning deltas coalesce into one live fragment so the body
	// streams in place under a single header; the bodies all stay visible while
	// streaming (live auto-expand). The raw view carries per-word style spans,
	// so the body checks strip them.
	for _, frag := range []string{"I check the env.", "Then I edit."} {
		if !strings.Contains(plain, frag) {
			t.Errorf("live reasoning body should render expanded while streaming, missing %q, got: %q", frag, content)
		}
	}
	if n := strings.Count(plain, "tok"); n != 1 {
		t.Errorf("live reasoning must render ONE growing header for the contiguous run, got %d:\n%s", n, plain)
	}

	m = applyDelta(t, m, "Hi there.")
	if got := m.tx.messages[n].content; got != "Hi there." {
		t.Errorf("answer delta content = %q, want %q (reasoning not merged into answer)", got, "Hi there.")
	}
}

func TestModel_thinkingExpandedStreams(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyReasoningDelta(t, m, "first ")
	if !strings.Contains(ansiStrip(view(m)), "first ") {
		t.Fatalf("live reasoning should render expanded before any tab, got: %q", view(m))
	}

	// Tab focuses the live reasoning block, Enter toggles it collapsed and back.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(ansiStrip(view(m)), "first ") {
		t.Fatalf("Enter on the focused live block should collapse it, got: %q", view(m))
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansiStrip(view(m)), "first ") {
		t.Fatalf("Enter again should re-expand the live block, got: %q", view(m))
	}

	m = applyReasoningDelta(t, m, "reasoning")
	plain := ansiStrip(view(m))
	if !strings.Contains(plain, "reasoning") {
		t.Errorf("streamed reasoning should render as its own live fragment, got: %q", view(m))
	}
	// Contiguous reasoning deltas coalesce: the second delta grows the first
	// fragment's block rather than painting a separate per-delta card.
	if !strings.Contains(plain, "first reasoning") {
		t.Errorf("contiguous reasoning deltas must coalesce into one growing block, got: %q", view(m))
	}
}

func TestModel_streamViewNeverClearsPrimary(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyDelta(t, m, "streaming reply")

	content := view(m)
	if strings.Contains(content, "\x1b[2J") {
		t.Errorf("streaming content carries a clear-screen sequence")
	}
	if strings.Contains(content, "\x1b[?1049") {
		t.Errorf("streaming content carries an alt-screen sequence")
	}
}

func TestModel_expandAllKeepsThinkingExpandedOnAnswer(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyReasoningDelta(t, m, "hidden reasoning")
	if !strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Fatalf("mode ON: streaming reasoning should render expanded, got: %q", view(m))
	}

	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "final answer", reasoning: "hidden reasoning"})
	m = asModel(t, nm)
	if !strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Errorf("mode ON: thinking block should stay expanded after the answer lands, got: %q", view(m))
	}
}

func TestModel_expandAllNewTurnRendersExpanded(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})

	m = typeText(t, m, "first")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "first reasoning")
	m = asModel(t, mustUpdate(t, m, turnDoneMsg{prompt: "first", answer: "first answer", reasoning: "first reasoning"}))

	m = typeText(t, m, "second")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "second reasoning")
	if !strings.Contains(ansiStrip(view(m)), "second reasoning") {
		t.Errorf("mode ON: a newly started turn's thinking should render expanded, got: %q", view(m))
	}
}

func TestModel_expandAllOffCollapsesThinking(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "hidden reasoning")
	m = asModel(t, mustUpdate(t, m, turnDoneMsg{prompt: "hi", answer: "final answer", reasoning: "hidden reasoning"}))
	if !strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Fatalf("mode ON: block should be expanded before toggling off, got: %q", view(m))
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Errorf("toggling mode OFF should collapse thinking back to a hint, got: %q", view(m))
	}
}

func TestModel_expandAllFocusToggleIndependent(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "hidden reasoning")
	if !strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Fatalf("mode ON: block should render expanded before focusing, got: %q", view(m))
	}
	// Tab focuses the live reasoning block; Enter collapses it even in mode ON.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Errorf("Enter on the focused block should collapse it even in mode ON, got: %q", view(m))
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansiStrip(view(m)), "hidden reasoning") {
		t.Errorf("Enter again should re-expand the block even in mode ON, got: %q", view(m))
	}

	m2 := newStreamingModel()
	m2 = resize(t, m2)
	m2 = typeText(t, m2, "hi")
	m2, _ = submitBusy(t, m2)
	m2 = applyReasoningDelta(t, m2, "hidden reasoning")
	if !strings.Contains(ansiStrip(view(m2)), "hidden reasoning") {
		t.Fatalf("mode OFF: streaming reasoning should render expanded, got: %q", view(m2))
	}
	m2 = mustUpdate(t, m2, tea.KeyPressMsg{Code: tea.KeyTab})
	m2 = mustUpdate(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	if strings.Contains(ansiStrip(view(m2)), "hidden reasoning") {
		t.Errorf("mode OFF: Enter on the focused block should collapse it, got: %q", view(m2))
	}
	m2 = mustUpdate(t, m2, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(ansiStrip(view(m2)), "hidden reasoning") {
		t.Errorf("mode OFF: Enter again should re-expand the live thinking block, got: %q", view(m2))
	}
	m2 = asModel(t, mustUpdate(t, m2, turnDoneMsg{prompt: "hi", answer: "final answer", reasoning: "hidden reasoning"}))
	if strings.Contains(ansiStrip(view(m2)), "hidden reasoning") {
		t.Errorf("mode OFF: answer-land should auto-collapse thinking back to a hint, got: %q", view(m2))
	}
}
