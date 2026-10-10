package tui

import (
	"testing"

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

	if tx.configTheme != "tokyo-night" {
		t.Errorf("NewTranscript configTheme = %q, want tokyo-night", tx.configTheme)
	}
	if tx.theme.accent == themeFor(config.DefaultTheme).accent {
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
