package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestRailRegressionTruncationAtMinimumAndWideWidths(t *testing.T) {
	t.Parallel()
	log := &toolLog{}
	log.Apply(ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"python3 -c 'print(1234567890123456789012345678901234567890)'"}`}})
	r := NewRail("provider-with-a-long-name", "model-with-a-very-long-name-that-must-not-overflow", "high", true, "session-with-a-very-long-id", "/tmp/session-with-a-very-long-id/and/a/long/path")

	for _, width := range []int{minWidthRail, 60} {
		view := ansiStrip(r.renderLiveWithTools(NewTelemetry("deepseek", "low", true, 250), defaultTheme, width, PhaseWorking, 0, log))
		if !strings.Contains(view, "…") {
			t.Fatalf("width %d should truncate long rail values:\n%s", width, view)
		}
		assertRailRowsFit(t, view, width)
	}
}

func TestRailRegressionMetersAndModelSessionTruncation(t *testing.T) {
	t.Parallel()
	te := NewTelemetry("deepseek", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 4, Miss: 1, Output: 10, Ctx: liveContextWarnThreshold})
	r := NewRail("opencode-go", "deepseek-v4-flash-with-extra-suffix", "medium", true, "eitri-session-with-extra-suffix", "/tmp/eitri-session-with-extra-suffix")

	view := r.render(te, defaultTheme, minWidthRail)
	plainView := ansiStrip(view)
	for _, want := range []string{"cache", "80%", "[=====", "ctx", "150.0k", "MODEL", "CONTEXT", "…"} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("rail regression view missing %q:\n%s", want, plainView)
		}
	}
	if ctx := lineContaining(view, "150.0k"); !strings.Contains(ctx, "38;2;247;118;142") {
		t.Fatalf("ctx warning line missing warning color: %q", ctx)
	}
	assertRailRowsFit(t, plainView, minWidthRail)
}

func TestRailRegressionClampBesideTranscriptAboveBottomBand(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn:      fakeSess("hi"),
		Telemetry: NewTelemetry("deepseek", "low", true, 250),
		Rail:      NewRail("opencode-go", "deepseek-v4-flash", "low", true, "sid", "/tmp/sid"),
	})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 72, Height: 9})
	m = asModel(t, nm)

	if !m.tx.railVisible() {
		t.Fatal("rail must remain beside the transcript on short/narrow layouts")
	}
	if got := len(strings.Split(strings.TrimRight(view(m), "\n"), "\n")); got > 9 {
		t.Fatalf("view height %d exceeds terminal height 9:\n%s", got, ansiStrip(view(m)))
	}
	if !strings.Contains(ansiStrip(view(m)), "STATS") {
		t.Fatalf("clamped rail should preserve top stats section:\n%s", ansiStrip(view(m)))
	}
}

func assertRailRowsFit(t *testing.T, view string, width int) {
	t.Helper()
	for _, row := range strings.Split(strings.TrimRight(view, "\n"), "\n") {
		if w := ansi.StringWidth(row); w > width {
			t.Fatalf("rail row width %d > %d: %q\n%s", w, width, row, view)
		}
	}
}
