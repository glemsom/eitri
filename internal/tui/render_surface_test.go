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
// to any particular palette (issue #210).
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
// live *Model (issue #210).
func TestRender_idleWelcome(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := renderSurfaceTestTheme()

	cases := []struct {
		name string
		want string
	}{
		{
			name: "brand-and-hints",
			want: "Eitri - your terminal coding agent\n" +
				"  ask me to fix a bug, refactor code, explain a system, or run the tests\n" +
				"  ctrl+s settings · / for commands\n",
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
// title, the question, and the y/n/esc binding row (issue #210).
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

// TestRender_bandHints table-tests the status-strip keybinding hint sets by the
// value inputs it takes — vim-normal and review-open — proving bandHints is a
// value-only (vimNormal, reviewOpen bool) → string function that never reads a
// live *Model (issue #210). Covers normal, vim, and review-open hint sets.
func TestRender_bandHints(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")

	cases := []struct {
		name       string
		vimNormal  bool
		reviewOpen bool
		want       string
	}{
		{
			name:       "normal",
			vimNormal:  false,
			reviewOpen: false,
			want:       "ctrl+s settings . ctrl+d review . ctrl+o copy",
		},
		{
			name:       "vim-normal",
			vimNormal:  true,
			reviewOpen: false,
			want:       "h j k l move . w b word . 0 $ line . i insert . esc exit",
		},
		{
			name:       "review-open",
			vimNormal:  false,
			reviewOpen: true,
			want:       "enter diff . o browser . ctrl+d close",
		},
		{
			name:       "vim-and-review",
			vimNormal:  true,
			reviewOpen: true,
			want:       "h j k l move . w b word . 0 $ line . i insert . esc exit",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bandHints(c.vimNormal, c.reviewOpen)
			if got != c.want {
				t.Errorf("bandHints(%v, %v) = %q, want %q", c.vimNormal, c.reviewOpen, got, c.want)
			}
		})
	}
}
