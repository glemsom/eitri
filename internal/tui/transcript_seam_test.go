package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/glemsom/eitri/internal/config"
	"github.com/glemsom/eitri/internal/tui/telemetry"
)

// The construction seam. NewTranscript is the single production construction
// site: it takes the varying inputs as one config and owns its own defaults
// (follow on, a fresh history viewport, a dirty layout).

// TestNewTranscriptAppliesConfigAndInvariants drives the constructor with a
// distinct theme and asserts the name reaches both the stored theme and the
// raw config-theme, and that the invariant defaults are set from the
// constructor alone.
func TestNewTranscriptAppliesConfigAndInvariants(t *testing.T) {
	cfg := TranscriptConfig{
		Theme:           "tokyo-night",
		ReasoningEffort: "high",
		Telemetry:       telemetry.NewTelemetry("deepseek-v4-flash", "high", true, 250),
		Rail:            &Rail{},
		RailWidth:       42,
		// CoTCollapsedByDefault / ToolResultsCollapsedByDefault zero -> expanded.
	}
	tx := NewTranscript(cfg)

	if tx.ConfigTheme() != "tokyo-night" {
		t.Errorf("NewTranscript configTheme = %q, want tokyo-night", tx.ConfigTheme())
	}
	if tx.Theme().accent == themeFor(config.DefaultTheme).accent {
		t.Error("NewTranscript must derive the theme from the config name, got the default accent")
	}
	if tx.reasoningEffort != "high" {
		t.Errorf("NewTranscript reasoningEffort = %q, want high", tx.reasoningEffort)
	}
	if tx.telemetry != cfg.Telemetry {
		t.Error("NewTranscript did not carry the telemetry through")
	}
	if tx.rail != cfg.Rail {
		t.Error("NewTranscript did not carry the rail through")
	}
	if tx.railWidth != 42 {
		t.Errorf("NewTranscript railWidth = %d, want 42", tx.railWidth)
	}
	if !tx.cotExpanded || !tx.toolResultsExpanded {
		t.Error("NewTranscript must default the collapse flags to expanded when not told collapsed")
	}
	// Invariant defaults owned by the constructor.
	if !tx.histFollow {
		t.Error("NewTranscript must default follow on")
	}
	if !tx.layout.dirty {
		t.Error("NewTranscript must come out with a dirty layout")
	}
	if tx.histViewport.Width() != 0 || tx.histViewport.Height() != 0 || tx.histViewport.MouseWheelEnabled {
		t.Errorf("NewTranscript must start with a fresh empty viewport, got %dx%d wheel=%v",
			tx.histViewport.Width(), tx.histViewport.Height(), tx.histViewport.MouseWheelEnabled)
	}
}

// TestNewTranscriptCollapseFlagsByDefault flips the two collapse-by-default
// inputs and asserts the constructor stores the inverse render defaults.
func TestNewTranscriptCollapseFlagsByDefault(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{
		CoTCollapsedByDefault:         true,
		ToolResultsCollapsedByDefault: true,
	})
	if tx.cotExpanded {
		t.Error("cotExpanded = true, want false when CoTCollapsedByDefault set")
	}
	if tx.toolResultsExpanded {
		t.Error("toolResultsExpanded = true, want false when ToolResultsCollapsedByDefault set")
	}
}

// TestApplySettingsAppliesReasoningEffort locks the settings seam: a settings
// save routes the reasoning-effort value through ApplySettings on the same
// path the rest of the config uses.
func TestApplySettingsAppliesReasoningEffort(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{ReasoningEffort: "low"})
	tx.ApplySettings(config.Config{ReasoningEffort: "high"})
	if tx.reasoningEffort != "high" {
		t.Errorf("ApplySettings reasoningEffort = %q, want high", tx.reasoningEffort)
	}
}

// TestThemeQueriesReportResolvedPalette locks the theme read seam: Theme is the
// resolved palette the transcript renders with, ConfigTheme the configured
// name, and a settings save moves both together.
func TestThemeQueriesReportResolvedPalette(t *testing.T) {
	tx := NewTranscript(TranscriptConfig{Theme: "dracula"})
	if got := tx.ConfigTheme(); got != "dracula" {
		t.Errorf("ConfigTheme = %q, want dracula", got)
	}
	if got := tx.Theme().accent; got != lipgloss.Color("#BD93F9") {
		t.Errorf("Theme().accent = %v, want the dracula accent", got)
	}

	tx.ApplySettings(config.Config{Theme: config.DefaultTheme})
	if got := tx.ConfigTheme(); got != config.DefaultTheme {
		t.Errorf("ConfigTheme after save = %q, want %q", got, config.DefaultTheme)
	}
	if got := tx.Theme().accent; got != lipgloss.Color("#7AA2F7") {
		t.Errorf("Theme().accent after save = %v, want the default accent", got)
	}
}

// TestPlainLinesQueryReturnsCopySpace locks the copy-space read: PlainLines is
// the history content stripped of styling, one row per rendered row, which is
// the space drag selection maps into.
func TestPlainLinesQueryReturnsCopySpace(t *testing.T) {
	tx := newTestTx()
	tx.SetSize(100, 30)
	projectStart(&tx, "run the thing", false)
	projectStream(&tx, AnswerStream, "final answer")

	lines := tx.PlainLines()
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"run the thing", "final answer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("PlainLines must carry the history text %q, got:\n%s", want, joined)
		}
	}
	for i, line := range lines {
		if ansiStrip(line) != line {
			t.Errorf("PlainLines row %d = %q carries styling; the copy space must be ANSI-stripped", i, line)
		}
	}
}

// historyRowIndex returns the index of the first plain history row carrying
// marker, failing the test when no row does.
func historyRowIndex(t *testing.T, lines []string, marker string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, marker) {
			return i
		}
	}
	t.Fatalf("marker %q not found in the history:\n%s", marker, strings.Join(lines, "\n"))
	return -1
}

// TestToolCardAtQueryResolvesTheEntry locks the tool-card hit-test: every
// content row the entry draws resolves to that entry, ordinary answer text
// resolves to none, and the index names the entry a click toggles.
func TestToolCardAtQueryResolvesTheEntry(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(context.Context, string, string) (TurnResult, error) {
			return TurnResult{Answer: "final answer"}, nil
		},
		Events: NewEventFeed(),
	})
	m = resize(t, m)
	m = typeText(t, m, "run it")
	m = submitAndWait(t, m)
	m = toolStart(t, m, "bash", `{"command":"go test ./..."}`)
	m = toolResult(t, m, ToolResult{Name: "bash", Result: "full output line one", Lines: 1})
	view(m)

	lines := m.tx.PlainLines()
	head := historyRowIndex(t, lines, "bash")
	body := historyRowIndex(t, lines, "full output line one")
	answer := historyRowIndex(t, lines, "final answer")

	idx, ok := m.tx.ToolCardAt(head)
	if !ok || idx != 0 {
		t.Errorf("ToolCardAt(tool head line %d) = (%d,%v), want the single entry 0", head, idx, ok)
	}
	if got, ok := m.tx.ToolCardAt(body); !ok || got != idx {
		t.Errorf("ToolCardAt(tool body line %d) = (%d,%v), want the same entry %d", body, got, ok, idx)
	}
	if _, ok := m.tx.ToolCardAt(answer); ok {
		t.Errorf("ToolCardAt(answer line %d) reported a tool card; answer text is not one", answer)
	}

	m.tx.toggleToolEntry(idx)
	if collapsed := strings.Join(m.tx.PlainLines(), "\n"); strings.Contains(collapsed, "full output line one") {
		t.Errorf("toggling the entry ToolCardAt resolved must collapse that card, got:\n%s", collapsed)
	}
}

// TestRenderedLineQueryCarriesHyperlink locks the rendered read: the row the
// query returns is the same content row as the plain copy space, and it still
// carries the OSC 8 target a Ctrl+click resolves.
func TestRenderedLineQueryCarriesHyperlink(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(context.Context, string, string) (TurnResult, error) {
			return TurnResult{Answer: "[Example](https://example.com/path)"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "show a link")
	m = submitAndWait(t, m)

	lines := m.tx.PlainLines()
	line := historyRowIndex(t, lines, "Example")

	rendered := m.tx.RenderedLine(line)
	if ansiStrip(rendered) != lines[line] {
		t.Errorf("RenderedLine(%d) = %q, want the plain copy-space row %q", line, rendered, lines[line])
	}
	col := lipgloss.Width(lines[line][:strings.Index(lines[line], "Example")])
	if target, linked := hyperlinkAt(rendered, col); !linked || target != "https://example.com/path" {
		t.Errorf("hyperlinkAt(RenderedLine(%d), %d) = %q/%v, want the row's OSC 8 target", line, col, target, linked)
	}
	if past := m.tx.RenderedLine(len(lines) + 3); past != "" {
		t.Errorf("RenderedLine past the content = %q, want empty", past)
	}
}
