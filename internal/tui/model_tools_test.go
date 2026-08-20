package tui

import (
	"context"

	"charm.land/lipgloss/v2"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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

func TestModel_toolEditEntryRenders(t *testing.T) {
	t.Parallel()
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"internal/main.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "Edit applied to internal/main.go\n", Added: 2, Removed: 0,
	}})

	content := view(m)
	if !strings.Contains(content, "✂️ edit") {
		t.Errorf("expected a one-line edit entry, got: %q", content)
	}
	if !strings.Contains(content, "internal/main.go") {
		t.Errorf("expected the edited path in the entry args, got: %q", content)
	}
	if !strings.Contains(content, "+2, −0") {
		t.Errorf("expected [+N,−M] delta tag, got: %q", content)
	}
}

func TestModel_toolEntryCollapsedThenExpandable(t *testing.T) {
	t.Parallel()
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
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

func TestModel_toolFeedDrainsLiveUpdates(t *testing.T) {
	t.Parallel()
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
	})
	m = resize(t, m)

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"a.txt"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "read", Result: "contents", Lines: 1}})

	if m.tx.log.Len() != 1 {
		t.Fatalf("expected one tool entry after start+result, got %d", m.tx.log.Len())
	}
	if !m.tx.log.Entry(0).complete {
		t.Errorf("tool entry should be complete after its result arrives")
	}
	if e := m.tx.log.Entry(0); e.name != "read" || e.result != "contents" {
		t.Errorf("tool entry = %+v, want read/contents", e)
	}
}

func TestModel_stylingToolHeadSplitsLabelAndArgs(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: NewToolFeed(),
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
		Tools: NewToolFeed(),
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
		Tools: NewToolFeed(),
	})
	m = resizeTo(t, m, 80, 24)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "web_fetch", `{"url":"https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Retry-After"}`)
	m = toolResult(t, m, ToolResult{Name: "web_fetch", Result: "error: fetch failed", Lines: 1})

	line := lineContaining(view(m), "🌐 web_fetch")
	if line == "" {
		t.Fatalf("tool row missing, got: %q", view(m))
	}
	if !strings.Contains(line, g("…", "...")) {
		t.Errorf("long args must truncate with an ellipsis, got: %q", line)
	}
	if width := lipgloss.Width(ansiStrip(line)); width > 78 {
		t.Errorf("tool row %d cols overflows the 80-col pane, want <= 78", width)
	}
	if !strings.Contains(m.transcriptText(), "Retry-After") {
		t.Errorf("copy must keep the full args, got: %q", m.transcriptText())
	}
}

func TestModel_ctrlETogglesExpandedViewMode(t *testing.T) {
	t.Parallel()
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
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
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
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
