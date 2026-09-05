package tui

import (
	"context"

	"charm.land/lipgloss/v2"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func feedToolUpdate(t *testing.T, m *Model, f *EventFeed, u ToolUpdate) Model {
	t.Helper()
	// Deliver through the live merged seam: push the observation onto the feed
	// channel and let the model's eventWait command pick it up as an eventMsg.
	f.updates <- Event{Tool: &u}
	cmd := eventWait(f)
	if cmd == nil {
		t.Fatal("expected an event waiter command")
	}
	msg := cmd()
	nm, _ := m.Update(msg)
	return asModel(t, nm)
}

func TestModel_toolEditEntryRenders(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "bash", Result: "a.go\nb.go\n", Lines: 2,
	}})

	content := view(m)
	if !strings.Contains(content, "🔧 bash") {
		t.Errorf("expected a one-line bash entry, got: %q", content)
	}
	if !strings.Contains(content, "ls") {
		t.Errorf("expected the command in the entry args, got: %q", content)
	}
}

func TestModel_toolEntryCollapsedThenExpandable(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "bash", Result: "a.go\nb.go\nc.go\n+3 more\n", Lines: 4, Dropped: 3, Compressed: true,
	}})

	content := view(m)
	if !strings.Contains(content, "🔧 bash") {
		t.Errorf("expected a one-line bash entry, got: %q", content)
	}
	if !strings.Contains(content, "+3 more") {
		t.Errorf("expected the compression tail marker in the collapsed summary, got: %q", content)
	}
	if strings.Contains(content, "c.go\n+3 more\n") && !m.tx.expandAll {
		t.Errorf("collapsed entry must not dump the raw result body, got: %q", content)
	}

	expanded, _ := m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = asModel(t, expanded)
	ex := view(m)
	if !strings.Contains(ex, "a.go") || !strings.Contains(ex, "+3 more") {
		t.Errorf("expanded entry should show the full result, got: %q", ex)
	}
}

func TestModel_eventFeedDrainsLiveToolUpdates(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "contents", Lines: 1}})

	if m.tx.log.Len() != 1 {
		t.Fatalf("expected one tool entry after start+result, got %d", m.tx.log.Len())
	}
	if !m.tx.log.Entry(0).complete {
		t.Errorf("tool entry should be complete after its result arrives")
	}
	if e := m.tx.log.Entry(0); e.name != "bash" || e.result != "contents" {
		t.Errorf("tool entry = %+v, want bash/contents", e)
	}
}

func TestModel_stylingToolHeadSplitsLabelAndArgs(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "ok (1ms)", Lines: 1})

	line := lineContaining(view(m), "🔧 bash")
	if line == "" {
		t.Fatalf("tool head row missing, got: %q", view(m))
	}
	if !strings.Contains(line, "\x1b[38;2;224;175;104m🔧 bash\x1b[m\x1b[2m  go test ./...") {
		t.Errorf("tool head must color the label and dim the args, got line: %q", line)
	}
}

func TestModel_stylingExpandedResultFramed(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "ok (2.1s)\n  PASS  TestLogin", Lines: 2})
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})

	line := lineContaining(view(m), "PASS  TestLogin")
	if line == "" {
		t.Fatalf("expanded result missing, got: %q", view(m))
	}
	if !strings.Contains(line, "\x1b[38;2;224;175;104m│\x1b[m") && !strings.Contains(line, "\x1b[38;2;224;175;104m│") {
		t.Errorf("expanded result must carry the category-colored left border, got line: %q", line)
	}
}

func TestModel_toolArgsTruncateToWidth(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resizeTo(t, m, 80, 24)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"curl --fail --max-time 30 https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After"}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "error: fetch failed", Lines: 1})

	line := lineContaining(view(m), "🔧 bash")
	if line == "" {
		t.Fatalf("tool row missing, got: %q", view(m))
	}
	if !strings.Contains(line, g("…", "...")) {
		t.Errorf("long args must truncate with an ellipsis, got: %q", line)
	}
	if width := lipgloss.Width(ansiStrip(line)); width > 78 {
		t.Errorf("tool row %d cols overflows the 80-col pane, want <= 78", width)
	}
}

func TestModel_ctrlETogglesExpandedViewMode(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"ls"}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2})

	if m.tx.expandAll {
		t.Fatal("expanded view must start off")
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if !m.tx.expandAll {
		t.Error("Ctrl+E must turn the expanded view on")
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if m.tx.expandAll {
		t.Error("second Ctrl+E must turn the expanded view off")
	}
	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if !m.tx.expandAll {
		t.Error("third Ctrl+E must turn the expanded view on again")
	}
}

func TestModel_expandedViewModeAppliesToNewlyDeliveredEntries(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = toolStart(t, m, "bash", `{"command":"ls"}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "a.go\nb.go", Lines: 2})

	content := view(m)
	if !m.tx.expandAll {
		t.Fatal("expanded view should be on")
	}
	if !strings.Contains(content, "a.go") {
		t.Errorf("newly delivered entry must render its full result with the mode ON, got: %q", content)
	}
}
