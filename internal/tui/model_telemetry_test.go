package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestModelStatusStripRenders asserts a status strip renders in the TUI view
// when telemetry is wired, showing the live cache gauge and cost pulled from
// the engine seam.
func TestModelStatusStripRenders(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})

	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "hi"}, nil
		},
		Telemetry: te,
	})
	m = resize(t, m)

	view := m.View()
	if !strings.Contains(view, "cache:80%") {
		t.Errorf("status strip missing cache gauge, got: %q", view)
	}
	if !strings.Contains(view, "cost:$") {
		t.Errorf("status strip missing cost, got: %q", view)
	}
	// 100k hit @0.0028/1M + 25k miss @0.14/1M + 10k output @0.28/1M.
	// = 0.00028 + 0.0035 + 0.0028 = $0.00658.
	if !strings.Contains(view, "cost:$0.00658") {
		t.Errorf("status strip cost mismatch, got: %q", view)
	}
}

// TestModelStatusStripDrainsLiveUpdates asserts feeding an update into the
// telemetry channel and running Update folds it into the rendered strip.
func TestModelStatusStripDrainsLiveUpdates(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 10)
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "hi"}, nil
		},
		Telemetry: te,
	})
	m = resize(t, m)

	// Feed a usage update on the telemetry channel (the app's listener path),
	// then drive it through the same live-delivery path the program uses: the
	// telemetry waiter wakes the loop with a telemetryUpdateMsg.
	te.updates <- TelemetryUpdate{Kind: TelemetryUsage, Hit: 90_000, Miss: 10_000, Output: 5_000}
	cmd := telemetryWait(te)
	if cmd == nil {
		t.Fatal("expected a telemetry waiter command")
	}
	msg := cmd()
	nm, _ := m.Update(msg)
	m = asModel(t, nm)

	if !strings.Contains(m.View(), "cache:90%") {
		t.Errorf("strip cache gauge did not update after drain, got: %q", m.View())
	}
}

// TestModelStatusStripCollapsesNarrow asserts the strip drops static session
// details on a narrow window, keeping the live telemetry, so it never
// crowds the composer.
func TestModelStatusStripCollapsesNarrow(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "hi"}, nil
		},
		Telemetry: te,
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = asModel(t, nm)

	view := m.View()
	if strings.Contains(view, "deepseek-v4-flash") {
		t.Errorf("narrow strip should drop the model name, got: %q", view)
	}
	if !strings.Contains(view, "cache:") || !strings.Contains(view, "cost:$") {
		t.Errorf("narrow strip must keep gauge+cost, got: %q", view)
	}
}
