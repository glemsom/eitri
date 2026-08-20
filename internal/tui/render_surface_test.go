package tui

import (
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
		name string
		want string
	}{
		{
			name: "brand-and-hints",
			want: "--\n" +
				"+ Eitri - your terminal coding agent\n" +
				"--\n" +
				"  > ask me to fix a bug, refactor code, explain a system, or run the tests\n" +
				"  k ctrl+s settings · /help for commands & keybindings\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idleWelcome(th); got != c.want {
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
	want := "ctrl+s settings . ctrl+o copy . ctrl+e expand . shift+enter newline"
	if got != want {
		t.Errorf("bandHints() = %q, want %q", got, want)
	}
}

func TestRender_idleWelcome_brandMark(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()
	got := idleWelcome(th)

	for _, want := range []string{"+ Eitri", "--", "> ask me", "k ctrl+s"} {
		if !strings.Contains(got, want) {
			t.Errorf("idleWelcome() missing %q, got:\n%s", want, got)
		}
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
	for _, want := range []string{"`/settings`", "`/copy`", "`/login`", "`/help`"} {
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
