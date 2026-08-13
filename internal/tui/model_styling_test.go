package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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

	chip := lineContaining(m.View(), "you")
	if chip == "" {
		t.Fatalf("expected a user chip in view, got: %q", m.View())
	}
	if !strings.HasPrefix(chip, " ") {
		t.Errorf("user chip must be right-aligned (leading padding), got line: %q", chip)
	}
	// The chip is a one-word label, not a full-width message: its visible text
	// is just the role, padded left to the pane width.
	if got := strings.TrimSpace(chip); got != "you" {
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

	pane := lineContaining(m.View(), "plain")
	if pane == "" {
		t.Fatalf("expected agent answer in view, got: %q", m.View())
	}
	if !strings.HasPrefix(pane, "│") {
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
	view := m.View()
	if !strings.Contains(view, "⊕ bash") {
		t.Errorf("tool glyph ⊕ must remain, got: %q", view)
	}
	if !strings.Contains(view, "✓") {
		t.Errorf("completed tool should carry a ✓ outcome tag, got: %q", view)
	}
	if strings.Contains(view, "✗") {
		t.Errorf("successful tool must not carry a ✗ tag, got: %q", view)
	}

	// A failed tool (engine error-shaped result): ✗ outcome tag.
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"/nope"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "read", Result: "error executing tool: boom"}})
	if view := m.View(); !strings.Contains(view, "✗") {
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

	view := m.View()
	if !strings.Contains(view, "⚠") {
		t.Errorf("failing turn should render the ⚠ error marker, got: %q", view)
	}
	pane := lineContaining(view, "provider")
	if pane == "" {
		t.Fatalf("expected error text in view, got: %q", view)
	}
	if !strings.HasPrefix(pane, "│") {
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

	hint := lineContaining(m.View(), "🤔")
	if hint == "" {
		t.Fatalf("expected a thinking hint in view, got: %q", m.View())
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
	if !strings.HasPrefix(borderRow, "─") {
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
		t.Errorf("user chip foreground = %q, want accent %q", got, accentColor)
	}
	if got := agentPaneStyle.GetBorderLeftForeground(); got != accentColor {
		t.Errorf("agent pane border foreground = %q, want accent %q", got, accentColor)
	}
	if got := errorPaneStyle.GetBorderLeftForeground(); got != errorColor {
		t.Errorf("error pane border foreground = %q, want error color %q", got, errorColor)
	}
	// Every palette entry is a hex color: lipgloss maps hex to the active
	// profile (256-color floor in a terminal), so no truecolor-only styling.
	for name, c := range map[string]lipgloss.Color{
		"accent": accentColor,
		"error":  errorColor,
		"ok":     okColor,
	} {
		if !strings.HasPrefix(string(c), "#") {
			t.Errorf("%s color = %q, want a hex color", name, c)
		}
	}

	// Rendered output never carries a truecolor (38;2) sequence: the surface
	// degrades to ANSI-256 or fewer colors when the terminal cannot do
	// truecolor (issue #122 AC5).
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	if strings.Contains(m.View(), "38;2;") {
		t.Errorf("view carries a truecolor sequence; must degrade to ANSI-256 floor")
	}
}
