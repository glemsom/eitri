package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func renderSurfaceTestTheme() Theme {
	var plain lipgloss.Style // zero-value style: Render passes text through
	return Theme{
		headerStyle:     plain,
		statusStyle:     plain,
		thinkingStyle:   plain,
		bandStatusStyle: plain,
	}
}

func TestRender_idleWelcome(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()

	cases := []struct {
		name   string
		width  int
		want   string
	}{
		{
			name:  "brand-and-hints-default",
			width: 2,
			want: "--\n" +
				"+  Eitri - your terminal coding agent\n" +
				"--\n" +
				"  k ctrl+, settings · /help for commands & keybindings\n",
		},
		{
			name:  "width-40",
			width: 40,
			want: strings.Repeat("-", 40) + "\n" +
				"+  Eitri - your terminal coding agent\n" +
				strings.Repeat("-", 40) + "\n" +
				"  k ctrl+, settings · /help for commands & keybindings\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idleWelcome(th, c.width); got != c.want {
				t.Errorf("idleWelcome() =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestRender_promptView(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()

	cases := []struct {
		name string
		want string
	}{
		{
			name: "full-prompt",
			want: "run paused at the max-turns cap\n\n" +
				"  Continue the run with more turns?\n" +
				"  y continue . n stop . esc cancel\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := promptView(th); got != c.want {
				t.Errorf("promptView() =\n%q\nwant\n%q", got, c.want)
			}
		})
	}

}

func TestRender_thinkingHeader(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()

	cases := []struct {
		name      string
		reasoning string
		effort    string
		want      string
	}{
		{
			name:      "no-effort",
			reasoning: "hello world", // 11 runes → 2 tokens
			want:      "? 2 tok\n",
		},
		{
			name:      "with-effort",
			reasoning: "hello world",
			effort:    "high",
			want:      "? 2 tok . high\n",
		},
		{
			name:      "empty-reasoning",
			reasoning: "",
			effort:    "low",
			want:      "? 0 tok . low\n",
		},
		{
			name:      "thousand-tokens-formats-k",
			reasoning: strings.Repeat("a", 4000), // 4000 runes → 1000 tokens → "1.0k"
			want:      "? 1.0k tok\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := thinkingHeader(th, c.reasoning, c.effort); got != c.want {
				t.Errorf("thinkingHeader(%q, %q) =\n%q\nwant\n%q", c.reasoning, c.effort, got, c.want)
			}
		})
	}
}

func TestRender_bandHints(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")

	got := bandHints()
	want := "ctrl+, settings . ctrl+e expand/collapse . shift+enter newline"
	if got != want {
		t.Errorf("bandHints() = %q, want %q", got, want)
	}
}

func TestRender_idleWelcome_brandMark(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()
	got := idleWelcome(th, 2)

	for _, want := range []string{"+  Eitri", "--", "k ctrl+,"} {
		if !strings.Contains(got, want) {
			t.Errorf("idleWelcome() missing %q, got:\n%s", want, got)
		}
	}
}

func TestRender_idleWelcome_ruleWidth(t *testing.T) {
	t.Parallel()
	th := renderSurfaceTestTheme()

	cases := []int{80, 120, 40}
	for _, w := range cases {
		w := w
		t.Run(fmt.Sprintf("width/%d", w), func(t *testing.T) {
			got := idleWelcome(th, w)
			lines := strings.Split(got, "\n")
			if len(lines) < 3 {
				t.Fatalf("expected at least 3 lines, got %d", len(lines))
			}
			topRule := lines[0]
			botRule := lines[2]
			if lipgloss.Width(topRule) != w {
				t.Errorf("top rule width = %d, want %d", lipgloss.Width(topRule), w)
			}
			if lipgloss.Width(botRule) != w {
				t.Errorf("bottom rule width = %d, want %d", lipgloss.Width(botRule), w)
			}
		})
	}
}

func TestRender_idleWelcome_ruleWidthASCII(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()
	got := idleWelcome(th, 40)
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}
	topRule := lines[0]
	want := strings.Repeat("-", 40)
	if topRule != want {
		t.Errorf("top rule = %q, want %q", topRule, want)
	}
}

func TestHelpView_glyphs(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	for _, want := range []string{"# COMMANDS", "# KEYBINDINGS", "# CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing Markdown header %q", want)
		}
	}
	for _, want := range []string{"`/settings`", "`/login`", "`/help`"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing command code span %q", want)
		}
	}
	for _, want := range []string{"c COMPOSER", "n NAVIGATION", "p PANES", "a ACTIONS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing keybinding category %q", want)
		}
	}
}
