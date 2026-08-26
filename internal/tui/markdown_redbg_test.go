package tui

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestRenderUserPromptCard_noStrayBackgroundColors asserts the rendered user
// bubble contains no background SGRs other than the bubble tint itself.
// Glamour's inline `code` style emits `48;5;236` (dark) on top of the bubble
// fill; without rewriting it the cell shows a dark patch inside the card and,
// combined with the red foreground remap of `38;5;203` to `th.error`, the
// user perceives the inline code chip as a "red background" — the bug we are
// fixing.
func TestRenderUserPromptCard_noStrayBackgroundColors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
	}{
		{"single inline code", "use `@` here"},
		{"multiple inline codes", "see `model.go` and `internal/tui` for the seam"},
		{"prompt from screenshot", "I've scoped `bubbles`; the `slashCandidates` precedent (`model.go`, `model_slash_parse_test.go`).\n\nWhat `@` and `internal/tui/model.go` mean here?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			th := themeFor("dark")
			md, err := RenderMarkdown(tc.input, 100, "dark")
			if err != nil {
				t.Fatalf("RenderMarkdown: %v", err)
			}
			card := renderUserPromptCard(th, md, 104)
			_ = os.WriteFile("/tmp/eitri_redbg2.ansi", []byte(card), 0o644)

			bubbleR, bubbleG, bubbleB, _ := th.bubble.RGBA()
			bubbleSGR := string(bubbleBgSGR(th))

			// 1) no 48;5; (256-color bg) must survive — glamour's inline-code
			//    dark gray leak. The 48;5; may appear as a later param inside
			//    a multi-param SGR (e.g. \x1b[38;2;...;48;5;236m), so match it
			//    anywhere in the SGR.
			if got := regexp.MustCompile(`\x1b\[[0-9;]*48;5;[0-9]+`).FindAllString(card, -1); len(got) > 0 {
				t.Errorf("rendered card contains stray 48;5; background SGRs: %v", got)
			}
			// 2) no 48;2;R;G;B that isn't the bubble tint.
			bgRe := regexp.MustCompile(`\x1b\[[0-9;]*48;2;(\d+);(\d+);(\d+)`)
			for _, m := range bgRe.FindAllStringSubmatch(card, -1) {
				if m[1] != strconv.Itoa(int(bubbleR>>8)) || m[2] != strconv.Itoa(int(bubbleG>>8)) || m[3] != strconv.Itoa(int(bubbleB>>8)) {
					t.Errorf("rendered card contains stray 48;2; background SGR: %q (want bubble %q)", m[0], bubbleSGR)
				}
			}
		})
	}
}
