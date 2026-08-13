package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"image/color"
)

// The T4 styling pass (issue #122) gives the TUI its modern look: visually
// distinct user vs agent messages, consistent emoji markers, a coherent
// bottom band, and a single-agent-accent palette centralized in lipgloss.
// These tests assert that look through the render seam — renderHistory /
// renderBand / renderPane and the View output — never through internal style
// plumbing, so the styling stays observable at the same Update/View surface
// the rest of the suite drives.

// lineContaining returns the first rendered line holding want, or "".
func lineContaining(s, want string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, want) {
			return l
		}
	}
	return ""
}

// TestModel_stylingUserChipRightAligned asserts user prompts render as a
// right-aligned "you" chip: the label line leads with padding to the pane
// width (issue #122 AC1: "right-aligned user chips").
func TestModel_stylingUserChipRightAligned(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	chip := lineContaining(view(m), "you")
	if chip == "" {
		t.Fatalf("expected a user chip in view, got: %q", view(m))
	}
	if !strings.HasPrefix(chip, " ") {
		t.Errorf("user chip must be right-aligned (leading padding), got line: %q", chip)
	}
	// The chip is a one-word label, not a full-width message: its visible text
	// is just the role, padded left to the pane width.
	if got := strings.TrimSpace(ansiStrip(chip)); got != "you" {
		t.Errorf("chip visible text = %q, want %q", got, "you")
	}
}

// TestModel_stylingAgentPaneBordered asserts assistant answers render as a
// left-bordered pane, visually distinct from the user chip (issue #122 AC1:
// "left-bordered agent panes").
func TestModel_stylingAgentPaneBordered(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	pane := lineContaining(view(m), "plain")
	if pane == "" {
		t.Fatalf("expected agent answer in view, got: %q", view(m))
	}
	if !strings.HasPrefix(ansiStrip(pane), "│") {
		t.Errorf("agent answer must render as a left-bordered pane, got line: %q", pane)
	}
}

// TestModel_stylingToolOutcomeMarkers asserts a completed tool entry carries a
// ✓ outcome tag and a failed one a ✗ tag, next to the persistent ⊕ tool glyph
// (issue #122 AC2: "tool outcome (✓/✗)").
func TestModel_stylingToolOutcomeMarkers(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "go")
	m = submitAndWait(t, m)

	// A successful tool: ⊕ glyph kept, ✓ outcome tag added.
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"true"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "done\n"}})
	content := view(m)
	if !strings.Contains(content, "⊕ bash") {
		t.Errorf("tool glyph ⊕ must remain, got: %q", content)
	}
	if !strings.Contains(content, "✓") {
		t.Errorf("completed tool should carry a ✓ outcome tag, got: %q", content)
	}
	if strings.Contains(content, "✗") {
		t.Errorf("successful tool must not carry a ✗ tag, got: %q", content)
	}

	// A failed tool (engine error-shaped result): ✗ outcome tag.
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"/nope"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "read", Result: "error executing tool: boom"}})
	if view := view(m); !strings.Contains(view, "✗") {
		t.Errorf("failed tool should carry a ✗ outcome tag, got: %q", view)
	}
}

// TestModel_stylingErrorMarker asserts a failing turn renders with the ⚠ error
// marker inside a bordered agent pane (issue #122 AC2: "errors (⚠)"), so an
// error is as readable as a normal answer.
func TestModel_stylingErrorMarker(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{}, errors.New("provider exploded")
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	content := view(m)
	if !strings.Contains(content, "⚠") {
		t.Errorf("failing turn should render the ⚠ error marker, got: %q", content)
	}
	pane := lineContaining(content, "provider")
	if pane == "" {
		t.Fatalf("expected error text in content, got: %q", content)
	}
	if !strings.HasPrefix(ansiStrip(pane), "│") {
		t.Errorf("error must render inside the bordered agent pane, got line: %q", pane)
	}
}

// TestModel_stylingThinkingMarker asserts the thinking block keeps its 🤔
// marker (issue #122 AC2: "thinking (🤔)") on the collapsed hint line.
func TestModel_stylingThinkingMarker(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok", Reasoning: "hidden reasoning"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	hint := lineContaining(view(m), "🤔")
	if hint == "" {
		t.Fatalf("expected a thinking hint in view, got: %q", view(m))
	}
	if !strings.Contains(hint, "tok") {
		t.Errorf("thinking hint should carry the token readout, got line: %q", hint)
	}
}

// TestModel_stylingBandCoherent asserts the bottom band reads as one coherent
// region: a top border separates it from the transcript, and it still carries
// the live status strip and the composer (issue #122 AC3).
func TestModel_stylingBandCoherent(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Telemetry: te,
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	var band strings.Builder
	m.renderBand(&band)
	bs := band.String()

	borderRow := strings.Split(bs, "\n")[0]
	if !strings.HasPrefix(ansiStrip(borderRow), "─") {
		t.Errorf("band must open with a top-border separator row, got first line: %q", borderRow)
	}
	if !strings.Contains(bs, "cache:80%") {
		t.Errorf("band missing live status strip, got: %q", bs)
	}
	if !strings.Contains(bs, m.composer.View()) {
		t.Errorf("band missing composer, got: %q", bs)
	}
}

// TestModel_stylingPaletteCentralized asserts the whole surface draws from the
// one centralized lipgloss style set: the user chip and the agent pane both
// carry the single agent accent, the error pane uses the semantic error color,
// and every color is a hex value lipgloss adapts to any color profile — so the
// surface degrades safely on a non-truecolor terminal (issue #122 AC4/AC5).
func TestModel_stylingPaletteCentralized(t *testing.T) {
	if got := userChipStyle.GetForeground(); got != accentColor {
		t.Errorf("user chip foreground = %v, want accent %v", got, accentColor)
	}
	if got := agentPaneStyle.GetBorderLeftForeground(); got != accentColor {
		t.Errorf("agent pane border foreground = %v, want accent %v", got, accentColor)
	}
	if got := errorPaneStyle.GetBorderLeftForeground(); got != errorColor {
		t.Errorf("error pane border foreground = %v, want error color %v", got, errorColor)
	}
	// Every palette entry is a hex color: lipgloss v2 maps a "#RRGGBB" string
	// to a concrete color.RGBA value (ANSI colors stay ansi.BasicColor /
	// ANSIColor), so a hex palette entry is detectable by its concrete type and
	// adapts to any color profile (256-color floor in a terminal), never
	// truecolor-only (issue #122 AC4/AC5).
	for name, c := range map[string]color.Color{
		"accent": accentColor,
		"error":  errorColor,
		"ok":     okColor,
	} {
		if _, ok := c.(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, c)
		}
	}

	// Color downsampling on a non-truecolor terminal moved to the output layer
	// in lipgloss v2 / bubbletea v2 (issue #148): Render() always emits
	// full-fidelity ANSI, and Bubble Tea v2 downsamples to the terminal's color
	// profile at render time — so the model's view content carries truecolor
	// sequences by design. The 256-color downsampling parity check is part of
	// the v2 migration audit (issue #149 AC3, manual TUI smoke test), not a
	// Render()-level assertion.
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	_ = view(m)
}
