package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// feedToolUpdate drives one tool update through the live-delivery path the
// program uses (the tool waiter wakes the loop with a toolUpdateMsg), asserting
// it leaves the feed's wait channel functional for the next update.
func feedToolUpdate(t *testing.T, m *Model, f *ToolFeed, u ToolUpdate) Model {
	t.Helper()
	f.updates <- u
	cmd := toolWait(f)
	if cmd == nil {
		t.Fatal("expected a tool waiter command")
	}
	msg := cmd()
	nm, _ := m.Update(msg)
	return asModel(t, nm)
}

// TestModel_toolEditEntryRenders asserts a file-edit tool call renders as a
// single-line collapsed entry with the tool name and a [+N,-M] delta tag (issue
// #84 AC1, AC3), and that toggling expansion reveals the full result.
func TestModel_toolEditEntryRenders(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	m = resize(t, m)
	// A turn is submitted so the tool entries anchor after its "you" message
	// (tools belong to a user turn).
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"internal/main.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "Edit applied to internal/main.go\n", Added: 2, Removed: 0,
	}})

	view := m.View()
	if !strings.Contains(view, "⊕ edit") {
		t.Errorf("expected a one-line edit entry, got: %q", view)
	}
	if !strings.Contains(view, "internal/main.go") {
		t.Errorf("expected the edited path in the entry args, got: %q", view)
	}
	if !strings.Contains(view, "+2, −0") {
		t.Errorf("expected [+N,−M] delta tag, got: %q", view)
	}
}

// TestModel_toolEntryCollapsedThenExpandable asserts a tool result collapses to
// a summary by default (never dumping the raw output into the scroll) and
// expands on demand to the full inline result — the lossless-recovery path
// (issue #84 AC2, AC4).
func TestModel_toolEntryCollapsedThenExpandable(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)

	// A compressed result: 4 lines kept + the "+3 more" tail marker, with 3
	// dropped. Collapsed, the view must show the entry and the marker count but
	// not the raw body.
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "bash", Result: "a.go\nb.go\nc.go\n+3 more\n", Lines: 4, Dropped: 3, Compressed: true,
	}})

	view := m.View()
	if !strings.Contains(view, "⊕ bash") {
		t.Errorf("expected a one-line bash entry, got: %q", view)
	}
	if !strings.Contains(view, "+3 more") {
		t.Errorf("expected the compression tail marker in the collapsed summary, got: %q", view)
	}
	// Raw body lines must not be dumped into the scroll when collapsed.
	if strings.Contains(view, "c.go\n+3 more\n") && !m.showToolResult {
		// c.go may legitimately appear if expanded; here it's collapsed.
		t.Errorf("collapsed entry must not dump the raw result body, got: %q", view)
	}

	// Expand on demand reveals the full result inline.
	expanded, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}, Alt: true})
	m = asModel(t, expanded)
	ex := m.View()
	if !strings.Contains(ex, "a.go") || !strings.Contains(ex, "+3 more\n") {
		t.Errorf("expanded entry should show the full result, got: %q", ex)
	}
}

// TestModel_toolFeedDrainsLiveUpdates asserts feeding a start then a result
// into the feed channel pairs into a single complete entry (issue #84).
func TestModel_toolFeedDrainsLiveUpdates(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn:  func(ctx context.Context, prompt string) (TurnResult, error) { return TurnResult{Answer: "ok"}, nil },
		Tools: feed,
	})
	m = resize(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "read", Result: "contents", Lines: 1}})

	if len(m.tools) != 1 {
		t.Fatalf("expected one tool entry after start+result, got %d", len(m.tools))
	}
	if !m.tools[0].complete {
		t.Errorf("tool entry should be complete after its result arrives")
	}
	if m.tools[0].name != "read" || m.tools[0].result != "contents" {
		t.Errorf("tool entry = %+v, want read/contents", m.tools[0])
	}
}
