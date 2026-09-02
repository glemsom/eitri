package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"image/color"

	"github.com/glemsom/eitri/internal/config"
)

func lineContaining(s, want string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, want) {
			return l
		}
	}
	return ""
}

// reasoningPaneRows returns the consecutive left-bordered rows of an expanded reasoning block in a
// rendered view: the rows that follow the turn's thinking header and precede the next non-pane row
// (the pane's own bottom spacer, the busy line, or the next message).
func reasoningPaneRows(t *testing.T, content string) []string {
	t.Helper()
	lines := strings.Split(content, "\n")
	start := -1
	for i, ln := range lines {
		if strings.Contains(ln, "🤔") {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatalf("no thinking header in content: %q", content)
	}
	var rows []string
	for i := start; i < len(lines); i++ {
		ln := lines[i]
		if !strings.HasPrefix(ansiStrip(ln), "│") && !strings.HasPrefix(ansiStrip(ln), "|") {
			if len(rows) > 0 {
				break // the pane's bottom spacer ends the body
			}
			continue // the pane's top spacer precedes the first content row
		}
		rows = append(rows, ln)
	}
	if len(rows) == 0 {
		t.Fatalf("no thinking pane rows after header in content: %q", content)
	}
	return rows
}

func TestModel_stylingNoRoleLabels(t *testing.T) {
	t.Parallel()
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

func TestModel_stylingAgentPaneBordered(t *testing.T) {
	t.Parallel()
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
	if strings.Contains(ansiStrip(pane), g("│", "|")) {
		t.Errorf("agent answer must not render a copy-hostile left bar, got line: %q", pane)
	}
}

func TestModel_stylingToolOutcomeMarkers(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "go")
	m = submitAndWait(t, m)

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

	m = feedToolUpdate(t, &m, feed, ToolUpdate{Start: &ToolStart{Name: "read", Args: `{"path":"/nope"}`}})
	m = feedToolUpdate(t, &m, feed, ToolUpdate{Result: &ToolResult{Name: "read", Result: "error executing tool: boom"}})
	if view := view(m); !strings.Contains(view, "✗") {
		t.Errorf("failed tool should carry a ✗ outcome tag, got: %q", view)
	}
}

func TestModel_stylingErrorMarker(t *testing.T) {
	t.Parallel()
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
	if strings.Contains(ansiStrip(pane), g("│", "|")) {
		t.Errorf("error must not render a copy-hostile left bar, got line: %q", pane)
	}
}

func TestModel_stylingToolCategoryColors(t *testing.T) {
	t.Parallel()
	feed := NewEventFeed()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok"}, nil
		},
		Events: feed,
	})
	m = resize(t, m)
	m = typeText(t, m, "go")
	m = submitAndWait(t, m)

	cases := []struct {
		tool string
		hue  string
	}{
		{"bash", "\x1b[38;2;224;175;104m"},            // shell #E0AF68
		{"open_in_browser", "\x1b[38;2;187;154;247m"}, // web
	}
	toolGlyphs := map[string]string{
		"bash":            "🔧",
		"open_in_browser": "🌍",
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

func TestModel_stylingThinkingDistinct(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	hint := lineContaining(view(m), "🤔")
	if hint == "" {
		t.Fatalf("expected a thinking hint in view, got: %q", view(m))
	}
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

func TestModel_stylingThinkingMarker(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "ok", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true},
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

func TestModel_stylingThinkingPaneDistinctFromAnswer(t *testing.T) {
	t.Parallel()
	m := NewModelCfg(Dependencies{
		Turn: func(ctx context.Context, prompt string, _ string) (TurnResult, error) {
			return TurnResult{Answer: "plain answer", Reasoning: "hidden reasoning"}, nil
		},
		Config: config.Config{ThinkingEnabled: true, CoTCollapsedByDefault: true},
	})
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m = submitAndWait(t, m)

	// Tab focuses the reasoning block; Enter expands it.
	m = keypress(t, m, "tab")
	m = keypress(t, m, "enter")
	content := view(m)

	thinkRows := reasoningPaneRows(t, content)
	if !strings.Contains(thinkRows[0], "\x1b[38;2;73;97;148m") {
		t.Errorf("thinking pane border should carry the dimmed accent (73;97;148), got row: %q", thinkRows[0])
	}
	if !strings.Contains(strings.Join(thinkRows, "\n"), "\x1b[3m") {
		t.Errorf("thinking pane body should render italic, got: %q", thinkRows)
	}

	answerRow := lineContaining(content, "plain")
	if !strings.Contains(answerRow, "\x1b[38;2;122;162;247m") {
		t.Errorf("answer pane border should carry the full accent (122;162;247), got row: %q", answerRow)
	}
	if strings.Contains(answerRow, "\x1b[3m") {
		t.Errorf("answer pane body must not render italic, got row: %q", answerRow)
	}
}

func TestModel_stylingStreamingThinkingPaneVariant(t *testing.T) {
	t.Parallel()
	m := newStreamingModel()
	m = resize(t, m)
	m = typeText(t, m, "hi")
	m, _ = submitBusy(t, m)
	m = applyReasoningDelta(t, m, "hidden reasoning")

	content := view(m)
	thinkRows := reasoningPaneRows(t, content)
	body := strings.Join(thinkRows, "\n")
	if !strings.Contains(ansiStrip(body), "hidden reasoning") {
		t.Fatalf("live reasoning body should render expanded while streaming, got: %q", content)
	}
	if !strings.Contains(thinkRows[0], "\x1b[38;2;54;72;111m") {
		t.Errorf("streaming thinking pane border should carry the dimmed-accent streaming variant (54;72;111), got row: %q", thinkRows[0])
	}
	if !strings.Contains(body, "\x1b[3m") {
		t.Errorf("streaming thinking pane body should render italic, got: %q", body)
	}
}

func TestModel_stylingBandCoherent(t *testing.T) {
	t.Parallel()
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
	if !strings.Contains(ansiStrip(borderRow), "Ask Eitri") {
		t.Errorf("band must open with the composer panel, got first line: %q", borderRow)
	}
	if !strings.Contains(ansiStrip(bs), "ctrl+s settings") {
		t.Errorf("band missing status strip key hints, got: %q", bs)
	}
	plainBand := ansiStrip(bs)
	if !strings.Contains(plainBand, "Ask Eitri") {
		t.Errorf("band missing composer panel, got: %q", bs)
	}
}

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

func TestModel_userBubbleFillsFullWidth(t *testing.T) {
	t.Parallel()
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
		start := -1
		for col, c := range cells {
			if c {
				start = col
				break
			}
		}
		if start < 0 || start+bw > len(cells) {
			t.Fatalf("user bubble row %d background run cannot cover card width %d; row=%q", row, bw, ln)
		}
		for col := start; col < start+bw; col++ {
			if !cells[col] {
				t.Fatalf("user bubble row %d col %d lacks background (background not filling box); row=%q", row, col, ln)
			}
		}
	}
}

func TestModel_stylingPaletteCentralized(t *testing.T) {
	t.Parallel()
	if got := defaultTheme.agentPaneStyle.GetBorderLeftForeground(); got != defaultTheme.accent {
		t.Errorf("agent pane border foreground = %v, want accent %v", got, defaultTheme.accent)
	}
	if got := defaultTheme.errorPaneStyle.GetBorderLeftForeground(); got != defaultTheme.error {
		t.Errorf("error pane border foreground = %v, want error color %v", got, defaultTheme.error)
	}
	for name, c := range map[string]color.Color{
		"accent": defaultTheme.accent,
		"error":  defaultTheme.error,
		"ok":     defaultTheme.ok,
	} {
		if _, ok := c.(color.RGBA); !ok {
			t.Errorf("%s color = %T, want a hex-derived color.RGBA", name, c)
		}
	}

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
