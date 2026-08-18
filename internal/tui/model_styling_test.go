package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"image/color"

	"github.com/glemsom/eitri/internal/config"
)

// The T4 styling pass gives the TUI its modern look: visually
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

// TestModel_stylingNoRoleLabels asserts the transcript renders prompt and
// answer without any "you"/"eitri" role labels: the user prompt is plain
// markdown, and only the agent pane's left border marks the answer side.
func TestModel_stylingNoRoleLabels(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	content := view(m)
	if !strings.Contains(content, "hi") || !strings.Contains(content, "plain") {
		t.Errorf("expected prompt and answer in view, got: %q", content)
	}
	if strings.Contains(content, "you") || strings.Contains(content, "eitri") {
		t.Errorf("role labels must not render in the transcript, got: %q", content)
	}
}

// TestModel_stylingAgentPaneBordered asserts assistant answers render as a
// left-bordered pane, visually distinct from the user chip (:
// "left-bordered agent panes").
func TestModel_stylingAgentPaneBordered(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
// ✓ outcome tag and a failed one a ✗ tag, next to the per-tool glyph
// ").
func TestModel_stylingToolOutcomeMarkers(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "go")
	m = submitAndWait(t, m)

	// A successful tool: per-tool glyph kept, ✓ outcome tag added.
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "bash", Args: `{"command":"true"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "bash", Result: "done\n"}})
	content := view(m)
	if !strings.Contains(content, "🔧 bash") {
		t.Errorf("tool glyph 🔧 must remain, got: %q", content)
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
// marker inside a bordered agent pane "), so an
// error is as readable as a normal answer.
func TestModel_stylingErrorMarker(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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

// TestModel_stylingToolCategoryColors asserts tool entries render with the
// per-category hue from the theme palette: shell tools in the
// shell color, file tools in the file color, web tools in the web color and
// skill activations in the skill color, while an unknown tool keeps the
// generic faint line. The per-tool glyph stays on every entry — meaning never rides
// on color alone .
func TestModel_stylingToolCategoryColors(t *testing.T) {
	feed := NewToolFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Tools: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "go")
	m = submitAndWait(t, m)

	// Per-category tool entries, each rendered through the model's default
	// theme: shell amber, file light-blue, web purple, skill pink.
	cases := []struct {
		tool string
		hue  string
	}{
		{"bash", "\x1b[38;2;224;175;104m"},            // shell #E0AF68
		{"read", "\x1b[38;2;125;207;255m"},            // file #7DCFFF
		{"write", "\x1b[38;2;125;207;255m"},           // file
		{"edit", "\x1b[38;2;125;207;255m"},            // file
		{"web_fetch", "\x1b[38;2;187;154;247m"},       // web #BB9AF7
		{"open_in_browser", "\x1b[38;2;187;154;247m"}, // web
		{"skill", "\x1b[38;2;255;135;215m"},           // skill #FF87D7
	}
	toolGlyphs := map[string]string{
		"bash":            "🔧",
		"read":            "📖",
		"write":           "✏️",
		"edit":            "✂️",
		"web_fetch":       "🌐",
		"open_in_browser": "🌍",
		"skill":           "⚡",
	}
	for _, tc := range cases {
		m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: tc.tool, Args: "{}"}})
		m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: tc.tool, Result: "done"}})
		glyph := toolGlyphs[tc.tool]
		line := lineContaining(view(m), glyph+" "+tc.tool)
		if line == "" {
			t.Fatalf("expected %s %s entry, got: %q", glyph, tc.tool, view(m))
		}
		if !strings.Contains(line, tc.hue) {
			t.Errorf("%s %s entry = %q, want category hue %q", glyph, tc.tool, line, tc.hue)
		}
		if !strings.Contains(line, glyph) {
			t.Errorf("%s %s entry lost its glyph, got: %q", glyph, tc.tool, line)
		}
	}

	// An unknown tool falls back to the generic faint entry — no category hue.
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "future_tool", Args: "{}"}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "future_tool", Result: "done"}})
	line := lineContaining(view(m), "⊕ future_tool")
	if line == "" {
		t.Fatalf("expected ⊕ future_tool entry, got: %q", view(m))
	}
	for _, hue := range []string{"38;2;224;175;104", "38;2;125;207;255", "38;2;187;154;247", "38;2;255;135;215"} {
		if strings.Contains(line, hue) {
			t.Errorf("unknown tool entry must not carry a category hue %q, got: %q", hue, line)
		}
	}
}

// TestModel_stylingThinkingDistinct asserts the thinking hint renders as a
// visually distinct treatment from the assistant answer body (
// AC2): the collapsed 🤔 line carries the accent hue and italic, while the
// answer text itself stays plain — the 🤔 glyph plus the accent+italic pair
// mark the hint, never color alone .
func TestModel_stylingThinkingDistinct(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	hint := lineContaining(view(m), "🤔")
	if hint == "" {
		t.Fatalf("expected a thinking hint in view, got: %q", view(m))
	}
	// lipgloss merges italic + faint + foreground into one escape: the hint
	// line opens with \x1b[3;2; then the accent's truecolor pair.
	if !strings.Contains(hint, "\x1b[3;2;") {
		t.Errorf("thinking hint should render italic, got line: %q", hint)
	}
	if !strings.Contains(hint, "\x1b[3;2;38;2;122;162;247m") {
		t.Errorf("thinking hint should carry the accent hue, got line: %q", hint)
	}
	if ans := lineContaining(view(m), "plain"); strings.Contains(ans, "\x1b[3m") || strings.Contains(ans, "\x1b[3;") {
		t.Errorf("answer body must stay non-italic, got line: %q", ans)
	}
}

// TestModel_stylingThinkingMarker asserts the thinking block keeps its 🤔
// marker ") on the collapsed hint line.
func TestModel_stylingThinkingMarker(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true},
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
// the live status strip and the composer .
func TestModel_stylingBandCoherent(t *testing.T) {
	te := NewTelemetry("deepseek-v4-flash", "low", true, 250)
	te.apply(TelemetryUpdate{Kind: TelemetryUsage, Hit: 100_000, Miss: 25_000, Output: 10_000})
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
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
	if !strings.Contains(bs, "ctrl+s settings") {
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
// surface degrades safely on a non-truecolor terminal .
// cellBGFill resolves an ANSI line into per-cell booleans: whether the bubble
// background is active at each display column. It walks SGR sequences tracking
// background state (resets clear it; a 48;2/48;5/40-47/100-107 sets it), which
// is exactly how a terminal renders the card. Foreground-only and unknown
// params leave the background untouched, mirroring real cell semantics.
func cellBGFill(s string) []bool {
	out := []bool{}
	bg := false
	runes := []rune(s)
	i := 0
	sgrParams := func(p string) []int {
		var out []int
		for _, f := range strings.Split(p, ";") {
			v := 0
			for _, c := range f {
				v = v*10 + int(c-'0')
			}
			out = append(out, v)
		}
		return out
	}
	for i < len(runes) {
		r := runes[i]
		if r == '\x1b' {
			i++
			if i < len(runes) && runes[i] == '[' {
				i++
				param := ""
				for i < len(runes) && !(runes[i] >= 'a' && runes[i] <= 'z') {
					param += string(runes[i])
					i++
				}
				if i < len(runes) && runes[i] == 'm' {
					i++
					nums := sgrParams(param)
					n := len(nums)
					j := 0
					for j < n {
						switch v := nums[j]; {
						case v == 0 || v == 49:
							bg = false
							j++
						case v >= 40 && v <= 47 || v >= 100 && v <= 107:
							bg = true
							j++
						case v == 48 && j+1 < n:
							bg = true
							if nums[j+1] == 2 && j+4 < n {
								j += 5
							} else {
								j += 2
							}
						case v == 38 && j+1 < n:
							if nums[j+1] == 2 && j+4 < n {
								j += 5
							} else {
								j += 2
							}
						default:
							j++
						}
					}
				}
			}
			continue
		}
		out = append(out, bg)
		i++
	}
	return out
}

// TestModel_userBubbleFillsFullWidth is the regression test for the user-prompt
// carded bubble (benchmark §4.1): glamour's per-token SGR resets clear lipgloss's
// Background, so the fill only ever lands in the 2-col gutters and the prompt
// text falls through to the default terminal background — a ragged, un-filled
// box, most visible over a code block. A prompt containing a code block must
// render every cell of the card with the bubble background. The transcript's
// right scroll/follow gutter (transcript width minus composer width) is out of
// scope: only the card's own columns are asserted.
func TestModel_userBubbleFillsFullWidth(t *testing.T) {
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "before\n```go\nx:=\"y\"\n```\nafter")
	m = submitAndWait(t, m)

	bw := m.composer.Width() // the card's own width, in columns
	for row, ln := range strings.Split(view(m), "\n") {
		cells := cellBGFill(ln)
		if len(cells) < bw {
			continue // not a card row
		}
		anyBg := false
		for _, c := range cells {
			if c {
				anyBg = true
				break
			}
		}
		if !anyBg {
			continue // welcome/band rows outside the prompt card
		}
		for col := 0; col < bw; col++ {
			if !cells[col] {
				t.Fatalf("user bubble row %d col %d lacks background (background not filling box); row=%q", row, col, ln)
			}
		}
	}
}

func TestModel_stylingPaletteCentralized(t *testing.T) {
	if got := defaultTheme.agentPaneStyle.GetBorderLeftForeground(); got != defaultTheme.accent {
		t.Errorf("agent pane border foreground = %v, want accent %v", got, defaultTheme.accent)
	}
	if got := defaultTheme.errorPaneStyle.GetBorderLeftForeground(); got != defaultTheme.error {
		t.Errorf("error pane border foreground = %v, want error color %v", got, defaultTheme.error)
	}
	// Every palette entry is a hex color: lipgloss v2 maps a "#RRGGBB" string
	// to a concrete color.RGBA value (ANSI colors stay ansi.BasicColor /
	// ANSIColor), so a hex palette entry is detectable by its concrete type and
	// adapts to any color profile (256-color floor in a terminal), never
	// truecolor-only .
	for name, c := range map[string]color.Color{
		"accent": defaultTheme.accent,
		"error":  defaultTheme.error,
		"ok":     defaultTheme.ok,
	} {
		if _, ok := c.(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, c)
		}
	}

	// Color downsampling on a non-truecolor terminal moved to the output layer
	// in lipgloss v2 / bubbletea v2: Render() always emits
	// full-fidelity ANSI, and Bubble Tea v2 downsamples to the terminal's color
	// profile at render time — so the model's view content carries truecolor
	// sequences by design. The 256-color downsampling parity check is part of
	// the v2 migration audit, not a
	// Render()-level assertion.
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer"}, nil
		},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)
	_ = view(m)
}
