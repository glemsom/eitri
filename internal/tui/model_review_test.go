package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// mustUpdate pumps one message and returns the model as a Model value.
func mustUpdate(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	nm, _ := m.Update(msg)
	return asModel(t, nm)
}

// ctrlD delivers the previously-review-bound ctrl+d keypress (released in
// issue #276) to the model and returns the resulting view.
func ctrlD(t *testing.T, m Model) Model {
	t.Helper()
	return mustUpdate(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
}

// fileEditModel builds a model with one completed edit tool entry carrying
// before/after content, so the transcript has a changed file to inspect.
func fileEditModel(t *testing.T) Model {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: NewToolFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m, _ = submitBusy(t, m)
	feed := m.deps.Tools
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"/w/main.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "edited\n", Before: "old\n", After: "new\n", Path: "/w/main.go",
	}})
	return m
}

// TestCtrlD_unbound asserts the released Ctrl+D keybinding does nothing at
// all (issue #276): it neither opens a review surface nor disturbs the
// transcript or composer, whether or not the session has changed files. The
// regression the panel's empty-state guard used to cover (issue #90's
// "no changes" surface) is subsumed: there is no surface to open. In-flow
// file-change inspection is covered by the expanded-card tests in
// toolcard_diff_test.go and render_regions_test.go (Ctrl+E path, issue #275).
func TestCtrlD_unbound(t *testing.T) {
	t.Run("no-changed-files", func(t *testing.T) {
		m := NewModelCfg(Dependencies{
			Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
				return TurnResult{Answer: "ok"}, nil
			},
			Tools: NewToolFeed(),
		})
		m = resize(t, m)
		before := view(m)
		m = ctrlD(t, m)
		after := view(m)
		if before != after {
			t.Errorf("ctrl+d must leave the surface untouched, view changed:\nbefore: %q\nafter:  %q", before, after)
		}
		if strings.Contains(after, "Review changed files") || strings.Contains(after, "no changes") {
			t.Errorf("ctrl+d must not open any review surface, got: %q", after)
		}
	})

	t.Run("with-changed-file", func(t *testing.T) {
		m := fileEditModel(t)
		before := view(m)
		m = ctrlD(t, m)
		after := view(m)
		if before != after {
			t.Errorf("ctrl+d must leave an edit-bearing transcript untouched, view changed:\nbefore: %q\nafter:  %q", before, after)
		}
		if strings.Contains(after, "Review changed files") {
			t.Errorf("ctrl+d must not open a review surface over changed files, got: %q", after)
		}
	})
}
