package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newTelemetryModel builds a Model wired with the given telemetry and,
// optionally, a right rail (NewRail), so a tab test can assert against the
// hints-only status strip and the rail-rendered STATS section.
func newTelemetryModel(t *testing.T, te *Telemetry, rail *Rail) Model {
	t.Helper()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "hi"}, nil
		},
		Telemetry: te,
		Rail:      rail,
	})
	return resize(t, m)
}

// TestModelStatusStripHintsOnly asserts the bottom status strip renders the
// keybinding hints and the busy spinner only — no telemetry numbers — while
// session stats live in the right rail's STATS section .
func TestModelStatusStripHintsOnly(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := newTelemetryModel(t, te, r)

	content := view(m)

	// The right rail renders the session stats (cache %, cost, turns/max,
	// elapsed); the bottom strip shows only the keybinding hints.
	if !strings.Contains(content, "cache 80%") {
		t.Errorf("right rail missing cache gauge, got: %q", content)
	}
	// 100k hit @0.0028/1M + 25k miss @0.14/1M + 10k output @0.28/1M
	// = 0.00028 + 0.0035 + 0.0028 = $0.00658.
	if !strings.Contains(content, "cost $0.00658") {
		t.Errorf("right rail missing cost, got: %q", content)
	}

	// The bottom band is hints-only: it renders the keybinding
	// hints and no telemetry readouts (those live solely in the rail).
	var band strings.Builder
	m.renderBand(&band)
	bs := band.String()
	if !strings.Contains(bs, "ctrl+s settings") {
		t.Errorf("status strip missing keybinding hints, got: %q", bs)
	}
	for _, gone := range []string{"cache:", "cost:", "0/", "elapsed", "effort:", "thinking:"} {
		if strings.Contains(bs, gone) {
			t.Errorf("bottom status strip must not show %q, got: %q", gone, bs)
		}
	}
}

// TestModelTelemetryDrainsLiveUpdates asserts feeding an update into the
// telemetry channel and running Update folds it into the telemetry surface,
// which the right rail then renders .
func TestModelTelemetryDrainsLiveUpdates(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := newTelemetryModel(t, te, r)

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

	if !strings.Contains(view(m), "cache 90%") {
		t.Errorf("right rail cache gauge did not update after drain, got: %q", view(m))
	}
}

// TestModelStatusStripHintsOnNarrow asserts the hints-only status strip renders
// on a narrow window too: hints never collapse away (the strip carries no
// telemetry to fall back on), and no telemetry numbers appear anywhere.
func TestModelStatusStripHintsOnNarrow(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	m := newTelemetryModel(t, te, nil)

	nm, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = asModel(t, nm)

	content := view(m)
	if !strings.Contains(content, "ctrl+s settings") {
		t.Errorf("narrow status strip should still show keybinding hints, got: %q", content)
	}
	for _, gone := range []string{"cache:", "cost:", "effort:", "thinking:"} {
		if strings.Contains(content, gone) {
			t.Errorf("narrow status strip must not show %q, got: %q", gone, content)
		}
	}
}

// TestModelStatusStripBusySpinner asserts that while a turn runs the bottom
// status strip keeps the busy spinner leading the keybinding hints (issue
// #228 AC1): the hints never disappear, and the spinner rides the same
// always-visible row.
func TestModelStatusStripBusySpinner(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	m := newTelemetryModel(t, te, nil)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	var band strings.Builder
	m.renderBand(&band)
	bs := band.String()
	if !strings.Contains(bs, " Working") {
		t.Errorf("busy status strip missing spinner, got: %q", bs)
	}
	if !strings.Contains(bs, "ctrl+s settings") {
		t.Errorf("busy status strip missing keybinding hints, got: %q", bs)
	}
	for _, gone := range []string{"cache:", "cost:", "0/", "effort:", "thinking:"} {
		if strings.Contains(bs, gone) {
			t.Errorf("busy status strip must not show %q, got: %q", gone, bs)
		}
	}
}
