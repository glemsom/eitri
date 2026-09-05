package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

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

func TestModelStatusStripHintsOnly(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := newTelemetryModel(t, te, r)

	content := view(m)

	if !strings.Contains(content, "cache 80%") {
		t.Errorf("right rail missing cache gauge, got: %q", content)
	}
	if strings.Contains(content, "cost") {
		t.Errorf("right rail must not render a cost readout, got: %q", content)
	}

	var band strings.Builder
	m.renderBand(&band)
	bs := band.String()
	// The status strip is the band status row: key hints on the left; the right
	// rail stays the only home for provider/model/elapsed/token readouts.
	if !strings.Contains(bs, "ctrl+e expand") {
		t.Errorf("status strip missing keybinding hints, got: %q", bs)
	}
	for _, gone := range []string{"cache:", "cost:", "0/", "elapsed", "effort:", "thinking:"} {
		if strings.Contains(bs, gone) {
			t.Errorf("bottom status strip must not show %q, got: %q", gone, bs)
		}
	}
}

func TestModelTelemetryDrainsLiveUpdates(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	r := NewRail("opencode-go", "deepseek-v4-flash", "low", true, "eitri-1", "/tmp/eitri-1")
	m := newTelemetryModel(t, te, r)

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

func TestModelStatusStripHintsOnNarrow(t *testing.T) {
	t.Parallel()
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

func TestModelStatusStripBusySpinner(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	m := newTelemetryModel(t, te, nil)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)

	var band strings.Builder
	m.renderBand(&band)
	bs := band.String()
	if !strings.Contains(bs, "  Striking the anvil") {
		t.Errorf("busy status strip missing spinner with double-spaced label, got: %q", bs)
	}
	if !strings.Contains(ansiStrip(bs), "⚒  Eitri is forging") {
		t.Errorf("busy band missing double-spaced locked panel title, got: %q", bs)
	}
	if !strings.Contains(ansiStrip(bs), "Striking the anvil · Hold steady — composer locked during forging") {
		t.Errorf("busy band must be a single body line joining the phase verb and the lock copy, got: %q", ansiStrip(bs))
	}
	if !strings.Contains(ansiStrip(bs), "elapsed") {
		t.Errorf("busy title missing live elapsed readout, got: %q", bs)
	}
	if !strings.Contains(bs, "Hold steady — composer locked during forging") {
		t.Errorf("busy band missing warm locked composer copy, got: %q", bs)
	}
	if !strings.Contains(bs, "ctrl+c stop") || !strings.Contains(bs, "pgup read history") || !strings.Contains(bs, "end follow") {
		t.Errorf("busy status strip missing busy keybinding hints, got: %q", bs)
	}
	for _, gone := range []string{"cache:", "cost:", "0/", "effort:", "thinking:"} {
		if strings.Contains(bs, gone) {
			t.Errorf("busy status strip must not show %q, got: %q", gone, bs)
		}
	}
}
