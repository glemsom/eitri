package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// longStreamModel is a busy turn streaming far more content than the viewport
// can hold, so the flow keeps growing well below the fold.
func longStreamModel(t *testing.T) Model {
	t.Helper()
	m := busyStreamingModel(t)
	m = resizeTo(t, m, 60, 10)
	for i := 0; i < 40; i++ {
		m = applyDelta(t, m, fmt.Sprintf("token%d %s", i, strings.Repeat("w", 30)))
	}
	return m
}

// TestScrollFollow_longStreamViewportStaysPinned: every appended delta, not
// just the first few, keeps the newest output pinned to the bottom of the
// history region while auto-follow is on.
func TestScrollFollow_longStreamViewportStaysPinned(t *testing.T) {
	t.Parallel()
	m := longStreamModel(t)
	if !m.tx.histFollow {
		t.Fatalf("a long stream must start in follow (auto-follow default), got false")
	}

	got, histContent, vh := followRendered(m)
	if vh <= 0 {
		t.Fatalf("test needs a positive viewport height, got %d", vh)
	}
	if n := lineCount(histContent); n <= vh {
		t.Fatalf("long stream must overflow: %d history lines fit in a %d-row viewport", n, vh)
	}
	if !atBottom(m) {
		t.Errorf("following stream must pin the viewport to the newest content, offset %d (not bottom)", scrollOffset(m))
	}
	// The very tail of the live stream (the busy indicator over the newest
	// streamed content) rides at the bottom of the region, so the active content
	// is what stays on screen.
	if row := newestNonBlank(got); row != "⠋ Answering\n" {
		t.Errorf("following a long stream must hold the newest active tail at the bottom, got last row %q\n%s", row, got)
	}

	// Every further delta keeps the viewport pinned to the newest content: follow
	// never slips as the stream keeps producing.
	for i := 0; i < 10; i++ {
		m = applyDelta(t, m, "tail content ")
		followRendered(m)
		if !m.tx.histFollow {
			t.Fatalf("delta %d must keep follow engaged", i)
		}
		if !atBottom(m) {
			t.Fatalf("delta %d let the viewport slip off the newest content (offset %d)", i, scrollOffset(m))
		}
	}
}

// scrolling up to read pauses auto-follow for the rest of the turn, the reading
// position holds while the stream keeps producing below, and a keypress (End)
// resumes follow to the newest content.
func TestScrollFollow_scrollUpPausesAndHoldsWhileStreaming(t *testing.T) {
	t.Parallel()
	m := longStreamModel(t)
	followRendered(m)
	if !atBottom(m) {
		t.Fatalf("precondition: following stream must be pinned to the newest")
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.tx.histFollow {
		t.Fatalf("scrolling up must pause auto-follow, got histFollow=true")
	}
	paused := scrollOffset(m)

	// The stream keeps producing while the user reads an earlier result; the
	// reading position must not move and follow must stay paused.
	for i := 0; i < 8; i++ {
		m = applyDelta(t, m, "while reading "+strings.Repeat("y", 25)+" ")
		followRendered(m)
		if m.tx.histFollow {
			t.Fatalf("streaming while paused must not re-engage follow on delta %d", i)
		}
		if got := scrollOffset(m); got != paused {
			t.Fatalf("reading position must hold while streaming, offset %d -> %d", paused, got)
		}
	}

	// End resumes follow to the newest content.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.tx.histFollow {
		t.Fatalf("End must re-engage auto-follow after a pause")
	}
	followRendered(m)
	if !atBottom(m) {
		t.Errorf("resumed follow must pin the viewport to the newest content, got offset %d", scrollOffset(m))
	}
}

// TestScrollFollow_wheelUpPausesAndEndResumesDuringStream drives pause/resume
// through the mouse-wheel seam rather than a key: wheel up pauses auto-follow
// mid stream and End resumes it, staying inside the scroll region throughout.
func TestScrollFollow_wheelUpPausesAndEndResumesDuringStream(t *testing.T) {
	t.Parallel()
	m := longStreamModel(t)
	followRendered(m)
	start := scrollOffset(m)

	m = mustUpdate(t, m, wheelMsg(true))
	if m.tx.histFollow {
		t.Fatalf("wheel up must pause auto-follow during a long stream")
	}
	if up := scrollOffset(m); up >= start {
		t.Fatalf("wheel up must scroll up, offset %d -> %d", start, up)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.tx.histFollow {
		t.Errorf("End after a wheel-pause must re-engage auto-follow, got histFollow=false")
	}
}

// TestScrollFollow_newTurnResumesAfterPause: a pause survives the turn's
// completion, but a fresh turn re-engages follow.
func TestScrollFollow_newTurnResumesAfterPause(t *testing.T) {
	t.Parallel()
	m := longStreamModel(t)
	followRendered(m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if m.tx.histFollow {
		t.Fatalf("precondition: PgUp must pause auto-follow")
	}

	// The turn finishes while the user is still reading up.
	m = asModel(t, mustUpdate(t, m, turnDoneMsg{prompt: "hi", answer: "final answer"}))
	if m.tx.Status().Busy {
		t.Fatalf("precondition: the turn must complete")
	}

	// A fresh turn re-engages follow to the newest output.
	m = typeText(t, m, "next")
	m, _ = submitBusy(t, m)
	if !m.tx.histFollow {
		t.Fatalf("a new turn must re-engage auto-follow after a pause, got histFollow=false")
	}
	followRendered(m)
	if !atBottom(m) {
		t.Errorf("a new turn should re-follow the newest output, got offset %d", scrollOffset(m))
	}
}
