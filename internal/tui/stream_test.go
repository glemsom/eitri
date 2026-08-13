package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// streamingTurn is a stand-in Turn seam that never completes on its own; the
// test finalizes it explicitly with a turnDoneMsg, mirroring how the engine
// turn and the answer-delta stream run concurrently.
func streamingTurn(ctx context.Context, prompt string) (TurnResult, error) {
	return TurnResult{Answer: "IGNORED"}, nil
}

// submitBusy feeds Enter on a non-empty composer and returns the model in the
// busy state plus the pending commands (without resolving the turn), so the
// streaming and completion messages can be applied by hand.
func submitBusy(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	if m.busy {
		t.Fatalf("model already busy")
	}
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected a turn command on submit")
	}
	out := asModel(t, nm)
	if !out.busy {
		t.Fatalf("model should be busy after submit")
	}
	return out, cmd
}

// applyDelta grows the in-progress assistant answer with one streamed answer
// delta by delivering a streamDeltaMsg through the model's Update seam.
func applyDelta(t *testing.T, m Model, delta string) Model {
	t.Helper()
	nm, _ := m.Update(streamDeltaMsg{kind: AnswerStream, delta: delta})
	return asModel(t, nm)
}

// newStreamingModel builds a model wired to a live answer Streamer (issue #83),
// the configuration the app uses for streaming; the test feeds deltas into the
// in-progress reply by hand through the Update seam.
func newStreamingModel() Model {
	return NewModelCfg(Dependencies{
		Turn:   streamingTurn,
		Stream: NewStreamer(),
	})
}

// TestModel_streamAnswerGrowsInPlace asserts streamed answer deltas grow the
// in-progress assistant message incrementally: the partial markdown renders in
// the view before the turn completes, and each delta re-renders in place rather
// than waiting for one full-reply render on completion (issue #83 AC1).
func TestModel_streamAnswerGrowsInPlace(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	// First delta creates the assistant message and renders partial markdown.
	m = applyDelta(t, m, "Hello **glad**")
	view := m.View()
	// The partial answer content is buffered on the in-progress assistant
	// message, growing in place before completion (issue #83 AC1).
	if got := m.messages[len(m.messages)-1].content; got != "Hello **glad**" {
		t.Errorf("first delta content = %q, want %q", got, "Hello **glad**")
	}
	// Partial markdown is styled through Glamour, not shown as raw syntax
	// (issue #83 AC2): bold "glad" must carry SGR emphasis in the view.
	if !hasSGRBold(view) {
		t.Errorf("expected partial markdown bold to render (issue #83 AC2), got: %q", view)
	}

	// A second delta extends the same message in place.
	m = applyDelta(t, m, " to help.")
	view2 := m.View()
	if got := m.messages[len(m.messages)-1].content; got != "Hello **glad** to help." {
		t.Errorf("second delta content = %q, want %q", got, "Hello **glad** to help.")
	}
	if strings.Contains(view2, "Hello **glad**") {
		t.Errorf("raw markdown must not leak into the view, got: %q", view2)
	}
}

// TestModel_streamFinalizeDropsRawDeltas asserts the turn's completion replaces
// the incremental buffer with the full, single-markdown-rendered answer — a
// no-op visual diff when the stream was complete (issue #83 AC1), and a
// guaranteed-correct final render when the last delta raced past completion.
func TestModel_streamFinalize(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	m = applyDelta(t, m, "Hello ")
	// Completion arrives with the full answer; the last delta never followed.
	nm, _ := m.Update(turnDoneMsg{prompt: "hi", answer: "Hello **glad** to help."})
	m = asModel(t, nm)

	view := m.View()
	if got := m.messages[len(m.messages)-1].content; got != "Hello **glad** to help." {
		t.Errorf("final content = %q, want full answer", got)
	}
	if !hasSGRBold(view) {
		t.Errorf("expected final markdown bold to render, got: %q", view)
	}
	if m.busy {
		t.Errorf("completion must clear the busy state")
	}
}

// TestModel_streamFallbackWithoutStreamer asserts a model configured without a
// Streamer keeps the historical non-streaming behaviour: a completed turn
// appends the full answer once (issue #83 backward-compat).
func TestModel_streamFallbackWithoutStreamer(t *testing.T) {
	m := NewModel(func(ctx context.Context, prompt string) (TurnResult, error) {
		return TurnResult{Answer: "plain answer"}, nil
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	if got := m.messages[len(m.messages)-1].content; got != "plain answer" {
		t.Errorf("expected non-streaming answer content, got %q", got)
	}
}

// applyReasoningDelta grows the in-progress assistant message's reasoning
// buffer with one streamed reasoning delta by delivering a streamDeltaMsg
// through the model's Update seam (issue #85).
func applyReasoningDelta(t *testing.T, m Model, delta string) Model {
	t.Helper()
	nm, _ := m.Update(streamDeltaMsg{kind: ReasoningStream, delta: delta})
	return asModel(t, nm)
}

// TestModel_thinkingStreamsLive asserts reasoning deltas from the engine seam
// grow a distinct thinking block live during the turn, alongside (but never
// merged into) the growing answer (issue #85 AC1/AC4).
func TestModel_thinkingStreamsLive(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	// Reasoning deltas accumulate onto the in-progress message's thinking
	// buffer, distinct from the answer buffer.
	m = applyReasoningDelta(t, m, "I check the env.")
	m = applyReasoningDelta(t, m, " Then I edit.")
	n := len(m.messages) - 1
	if got := m.messages[n].reasoning; got != "I check the env. Then I edit." {
		t.Errorf("streamed reasoning = %q, want accumulated thinking", got)
	}
	if m.messages[n].content != "" {
		t.Errorf("reasoning must not write into the answer buffer, got %q", m.messages[n].content)
	}
	// The thinking block is live but auto-collapsed: a hint renders, not the
	// body, until the user expands it (issue #85 AC1/AC2).
	view := m.View()
	if !strings.Contains(view, "🤔") {
		t.Errorf("live reasoning should render a thinking hint, got: %q", view)
	}
	if strings.Contains(view, "I check the env.") {
		t.Errorf("reasoning body should stay collapsed while streaming, got: %q", view)
	}

	// An answer delta still grows the answer buffer untouched.
	m = applyDelta(t, m, "Hi there.")
	if got := m.messages[n].content; got != "Hi there." {
		t.Errorf("answer delta content = %q, want %q (reasoning not merged into answer)", got, "Hi there.")
	}
}

// TestModel_thinkingExpandedStreams asserts the expanded thinking block keeps
// streaming reasoning in place once the user expands it during a turn (issue
// #85 AC2: "expands in place").
func TestModel_thinkingExpandedStreams(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	// First reasoning delta creates the (collapsed) block, then tab expands it.
	m = applyReasoningDelta(t, m, "first ")
	toggled, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = asModel(t, toggled)
	// A subsequent reasoning delta must render live in the expanded block.
	m = applyReasoningDelta(t, m, "reasoning")
	if !strings.Contains(m.View(), "first reasoning") {
		t.Errorf("expanded block should show streamed reasoning, got: %q", m.View())
	}
}

// TestModel_streamViewNeverClearsPrimary asserts streaming renders into the
// primary buffer: no clear-screen or alt-screen escape sequence, preserving
// native selection/scrollback/search (issue #83 AC4 / spec §9).
func TestModel_streamViewNeverClearsPrimary(t *testing.T) {
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyDelta(t, m, "streaming reply")

	view := m.View()
	if strings.Contains(view, "\x1b[2J") {
		t.Errorf("streaming view carries a clear-screen sequence (issue #83 AC4)")
	}
	if strings.Contains(view, "\x1b[?1049") {
		t.Errorf("streaming view carries an alt-screen sequence (issue #83 AC4)")
	}
}
