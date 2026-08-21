package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestModel_expandedViewEditCardRendersInlineDiff(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"internal/auth.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "Edit applied\n", Added: 1, Removed: 1,
		Before: "package auth\n\nfunc Old() {}\n", After: "package auth\n\nfunc New() {}\n", Path: "internal/auth.go",
	}})

	collapsed := view(m)
	if !strings.Contains(collapsed, "[+1, −1]") {
		t.Errorf("collapsed card must keep the delta tag, got: %q", collapsed)
	}
	if strings.Contains(collapsed, "func Old") || strings.Contains(collapsed, "func New") {
		t.Errorf("collapsed card must not leak before/after content, got: %q", collapsed)
	}

	m = mustUpdate(t, m, tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	strip := ansiStrip(view(m))
	if !strings.Contains(strip, "-func Old() {}") || !strings.Contains(strip, "+func New() {}") {
		t.Errorf("expanded-view edit card must render the inline diff, got:\n%s", strip)
	}
	if !strings.Contains(strip, "@@ -1,3 +1,3 @@") {
		t.Errorf("expanded-view edit card must render git-style hunk headers, got:\n%s", strip)
	}
}

func TestRenderRegions_HistoryVsBandSeparation(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "I think first."}, nil
		},
		WorkspacePath: "/tmp/acme-project",
		Telemetry:     te,
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "edit", Args: `{"path":"internal/main.go"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "edit", Result: "Edit applied\n", Added: 2, Removed: 0,
	}})

	m = typeText(t, m, "/")

	var hist, band strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	m.renderBand(&band)
	hs, bs := hist.String(), band.String()

	for _, want := range []string{"workspace: /tmp/acme-project", "✂️ edit"} {
		if !strings.Contains(hs, want) {
			t.Errorf("scroll region missing %q, got:\n%s", want, hs)
		}
	}
	if !strings.Contains(hs, "plain") || !strings.Contains(hs, "answer") {
		t.Errorf("scroll region missing the message body, got:\n%s", hs)
	}
	if strings.Contains(hs, m.composer.View()) {
		t.Errorf("composer leaked into the scroll region, got:\n%s", hs)
	}
	if strings.Contains(hs, "ctrl+s settings") {
		t.Errorf("status strip leaked into the scroll region, got:\n%s", hs)
	}

	if !strings.Contains(bs, "ctrl+s settings") {
		t.Errorf("band missing status strip, got:\n%s", bs)
	}
	if !strings.Contains(bs, m.composer.View()) {
		t.Errorf("band missing composer, got:\n%s", bs)
	}
	if strings.Contains(bs, "plain") {
		t.Errorf("message body leaked into the band, got:\n%s", bs)
	}
}

func resizeTo(t *testing.T, m Model, width, height int) Model {
	t.Helper()
	nm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return asModel(t, nm)
}

func TestRenderPane_ComposesRegionsInOrder(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
		WorkspacePath: "/tmp/acme-project",
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	var hist, band strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	m.renderBand(&band)
	bandStr := band.String()

	got := m.renderPane()
	if !strings.HasSuffix(got, bandStr) {
		t.Errorf("renderPane must keep the band as the final (bottom-pinned) region\n--- got ---\n%s", got)
	}
	head := strings.TrimSuffix(got, bandStr)
	wantLines := normalizeRows(hist.String())
	gotLines := normalizeRows(head)
	if len(gotLines) != len(wantLines) {
		t.Fatalf("renderPane head (%d rows) != height-clamped history region (%d rows)\n--- want --------\n%s\n--- got ---------\n%s", len(gotLines), len(wantLines), hist.String(), head)
	}
	for i := range wantLines {
		if gotLines[i] != wantLines[i] {
			t.Errorf("renderPane head row %d mismatch\n want: %q\n got: %q", i, wantLines[i], gotLines[i])
		}
	}
}

func normalizeRows(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimRight(l, " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
