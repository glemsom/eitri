package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// renderSurfaceTestTheme returns a Theme whose chrome styles are unstyled, so
// every helper's .Render call echoes its input verbatim. This fixes the value
// surface as a plain-text data-in → string-out mapping, letting the tests in
// this file assert the exact hint/header/welcome strings without coupling them
// to any particular palette.
//
// The tests force the ASCII glyph fallback via EITRI_ASCII_GLYPHS so the
// decorative separators (· vs ".") are deterministic regardless of locale.
func renderSurfaceTestTheme() Theme {
	var plain lipgloss.Style // zero-value style: Render passes text through
	return Theme{
		headerStyle:     plain,
		statusStyle:     plain,
		thinkingStyle:   plain,
		bandStatusStyle: plain,
	}
}

// TestRender_idleWelcome table-tests the empty-transcript welcome block: the
// brand line, the capability hint, and the keybinding strip, exactly as
// render.go concatenates them. It pins the value-only signature
// idleWelcome(th Theme) string and watches that the welcome never reaches a
// live *Model.
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

// TestRender_promptView table-tests the max-turns continuation prompt: the
// title, the question, and the y/n/esc binding row.
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

// TestRender_thinkingHeader table-tests the collapsed reasoning header: the
// glyph, token estimate, and optional effort tier suffix. It pins
// thinkingHeader(th Theme, reasoning, effort string) string and the
// effort-empty (suffix dropped) vs effort-set (suffix appended) branches.
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

// TestRender_bandHints pins the status-strip keybinding hint set: only the
// regular bindings are advertised — ctrl+s settings, ctrl+o copy, ctrl+e
// expand, shift+enter newline. The review-open hint set (enter diff / o
// browser / ctrl+d close) went with the modal review panel, and the released
// Ctrl+D key is deliberately never advertised.
func TestRender_bandHints(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")

	got := bandHints()
	want := "ctrl+s settings . ctrl+o copy . ctrl+e expand . shift+enter newline"
	if got != want {
		t.Errorf("bandHints() = %q, want %q", got, want)
	}
}

// TestRender_idleWelcome_brandMark asserts the welcome screen contains the
// ⚒ brand mark, horizontal rules, and emoji-decorated hint lines.
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

// TestHelpView_glyphs asserts the help view contains section emoji and rule
// separators.
func TestHelpView_glyphs(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	got := helpView()

	// Section headers include emoji prefixes.
	for _, want := range []string{"$ COMMANDS", "k KEYBINDINGS", "< CONCEPTS"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing section emoji %q", want)
		}
	}
	// Horizontal rule separators between sections.
	if !strings.Contains(got, "--") {
		t.Errorf("helpView() missing horizontal rule separators")
	}
	// Command row emoji prefixes.
	for _, want := range []string{"* /settings", "# /copy", "+ /login", "? /help"} {
		if !strings.Contains(got, want) {
			t.Errorf("helpView() missing command emoji %q", want)
		}
	}
}
