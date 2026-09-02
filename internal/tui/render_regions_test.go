package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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
		Events:        feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "edit it")
	m = submitAndWait(t, m)
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"ls -la"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{
		Name: "bash", Result: "a.go\nb.go\n", Lines: 2,
	}})

	m = typeText(t, m, "/")

	var hist, band strings.Builder
	m.tx.renderHistory(&hist, nil, nil)
	m.renderBand(&band)
	hs, bs := hist.String(), band.String()

	for _, want := range []string{"🔧 bash", "ls -la"} {
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

	plainBand := ansiStrip(bs)
	if !strings.Contains(plainBand, "/tmp/acme-project") {
		t.Errorf("band status row must surface the workspace path (top-of-screen header removed), got band:\n%s", bs)
	}
	if !strings.Contains(plainBand, "↑/↓ navigate") {
		t.Errorf("band missing slash status strip, got:\n%s", bs)
	}
	if !strings.Contains(plainBand, "Ask Eitri") || !strings.Contains(plainBand, "/") {
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
