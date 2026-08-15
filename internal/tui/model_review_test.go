package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newReviewModel builds a model wired with a turn seam, a tool feed, and a
// recording open_in_browser hook so the review panel (issue #90) can be driven
// end to end.
func newReviewModel(t *testing.T, opened *[]string) Model {
	t.Helper()
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
		OpenInBrowser: func(ctx context.Context, target string) error {
			if opened != nil {
				*opened = append(*opened, target)
			}
			return nil
		},
	})
	return m
}

// reviewFeedEdit drives one completed edit/write tool result into the model's
// tool entries via the live-delivery path, returning the updated model, so the
// review panel can list the changed file.
func reviewFeedEdit(t *testing.T, m *Model, feed *ToolFeed, path, name, before, after string, added, removed int) Model {
	t.Helper()
	nm := feedToolUpdate(t, m, feed, ToolUpdate{Start: &ToolStart{Name: name, Args: `{"path":"` + path + `"}`}})
	nm = feedToolUpdate(t, &nm, feed, ToolUpdate{Result: &ToolResult{
		Name: name, Result: "edited\n", Added: added, Removed: removed, Before: before, After: after, Path: path,
	}})
	return nm
}

// mustUpdate pumps one message and returns the model as a Model value.
func mustUpdate(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	return asModel(t, nm)
}

// reopenReview opens the review panel (ctrl+d) and returns the model.
func reopenReview(t *testing.T, m Model) Model {
	t.Helper()
	return mustUpdate(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
}

// TestReview_togglesOpenAndClosed asserts ctrl+d opens the review panel listing
// changed files and closes it back to the transcript (issue #90 AC1).
func TestReview_togglesOpenAndClosed(t *testing.T) {
	var feed = NewToolFeed()
	m := newReviewModel(t, nil)
	m = resize(t, m)
	m = reviewFeedEdit(t, &m, feed, "/w/main.go", "edit", "old\nline\n", "old\nchanged\n", 0, 1)

	// Open: the panel header and the changed file appear in the view.
	m = reopenReview(t, m)
	content := view(m)
	if !strings.Contains(content, "Review changed files") {
		t.Errorf("review panel not open after ctrl+d, got: %q", content)
	}
	if !strings.Contains(content, "main.go") {
		t.Errorf("changed file list missing main.go, got: %q", content)
	}

	// Close back to the transcript.
	m = reopenReview(t, m)
	if strings.Contains(view(m), "Review changed files") {
		t.Errorf("review panel did not close, got: %q", view(m))
	}
}

// TestReview_listsFilesWithStatusAndDeltas asserts each touched file renders
// with its [+N,-M] deltas and status (modified/added/deleted) — the
// code-review-style summary of issue #90.
func TestReview_listsFilesWithStatusAndDeltas(t *testing.T) {
	feed := NewToolFeed()
	m := newReviewModel(t, nil)
	m = resize(t, m)
	m = reviewFeedEdit(t, &m, feed, "/w/mod.go", "edit", "a\nb\n", "a\nB\n", 0, 1) // modified
	m = reviewFeedEdit(t, &m, feed, "/w/new.go", "write", "", "x\ny\nz\n", 3, 0)   // added
	m = reviewFeedEdit(t, &m, feed, "/w/del.go", "edit", "gone\n", "", 0, 1)       // deleted

	m = reopenReview(t, m)
	content := view(m)
	for _, want := range []string{"mod.go", "+0, −1]", "new.go", "+3,", "del.go"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q:\n%s", want, content)
		}
	}
	if !strings.Contains(content, "modified") || !strings.Contains(content, "added") || !strings.Contains(content, "deleted") {
		t.Errorf("status vocabulary missing in content:\n%s", content)
	}
}

// TestReview_inlineDiffWhenExpanded asserts a focused file's inline diff (hunks
// with +/- lines) renders without leaving the terminal (issue #90 AC2).
func TestReview_inlineDiffWhenExpanded(t *testing.T) {
	feed := NewToolFeed()
	m := newReviewModel(t, nil)
	m = resize(t, m)
	m = reviewFeedEdit(t, &m, feed, "/w/mod.go", "edit", "hello\n", "world\n", 0, 1)

	m = reopenReview(t, m)
	// Expand the focused file's diff.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	content := view(m)
	if !strings.Contains(content, "+world") || !strings.Contains(content, "-hello") {
		t.Errorf("inline diff not rendered, got:\n%s", content)
	}
}

// TestReview_openInBrowserEscapeHatch asserts the escape hatch opens the focused
// changed file's host path in the browser/editor seam (issue #90 AC3).
func TestReview_openInBrowserEscapeHatch(t *testing.T) {
	var opened []string
	feed := NewToolFeed()
	m := newReviewModel(t, &opened)
	m = resize(t, m)
	m = reviewFeedEdit(t, &m, feed, "/w/mod.go", "edit", "a\n", "b\n", 0, 1)

	m = reopenReview(t, m)
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	if len(opened) != 1 || opened[0] != "/w/mod.go" {
		t.Errorf("open_in_browser = %v, want [\"/w/mod.go\"]", opened)
	}
}

// TestReview_noFilesIsGraceful asserts ctrl+d with no changed files still opens
// a panel but shows an empty message rather than panicking or hanging.
func TestReview_noFilesIsGraceful(t *testing.T) {
	m := newReviewModel(t, nil)
	m = resize(t, m)
	m = reopenReview(t, m)
	if !strings.Contains(view(m), "no changes") {
		t.Errorf("empty review panel should say 'no changes', got:\n%s", view(m))
	}
}

// TestReview_openInBrowserErrorIsOneShot asserts the open_in_browser failure
// note renders on exactly the frame after the failure, then clears — it is a
// one-shot band note (issue #90), so later frames must not redraw a stale error
// (regression: the Transcript renderReview lost the reset on the expand step,
// issue #243).
func TestReview_openInBrowserErrorIsOneShot(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:          func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools:         feed,
		OpenInBrowser: func(ctx context.Context, target string) error { return fmt.Errorf("boom") },
	})
	m = resize(t, m)
	m = reviewFeedEdit(t, &m, feed, "/w/mod.go", "edit", "a\n", "b\n", 0, 1)
	m = reopenReview(t, m)

	// Trigger the failing open_in_browser hook so the panel records the error.
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'o', Text: "o"})
	// The error note renders on the frame after the failure…
	first := view(m)
	if !strings.Contains(first, "open_in_browser: boom") {
		t.Fatalf("first frame should show the open_in_browser error, got:\n%s", first)
	}
	// …then clears so a stale error never persists across later frames.
	sub := view(m)
	if strings.Contains(sub, "open_in_browser: boom") {
		t.Errorf("open_in_browser error note must be one-shot, redrawn on:\n%s", sub)
	}
}
