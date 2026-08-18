package tui

import (
	"testing"
	"time"
)

// TestRender_formatElapsed table-tests the tool-timer vocabulary: seconds under
// a minute, minutes+seconds under an hour, hours+minutes beyond.
func TestRender_formatElapsed(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{1 * time.Second, "1s"},
		{59 * time.Second, "59s"},
		{90 * time.Second, "1m 30s"},
		{3599 * time.Second, "59m 59s"},
		{3600 * time.Second, "1h 00m"},
		{3723 * time.Second, "1h 02m"},
	}
	for _, c := range cases {
		if got := formatElapsed(c.d); got != c.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestRender_busyLine table-tests the in-progress working indicator: the animated
// braille spinner frame cycled by index when motion is enabled, and the static
// "… thinking" line under reduced motion. The index wraps by modulo over the
// frame set so the spinner loops.
func TestRender_busyLine(t *testing.T) {
	// Animated path: no EITRI_NO_MOTION, UTF-8 locale (the default in tests).
	idxCases := []int{0, 1, len(busySpinnerFrames) - 1, len(busySpinnerFrames), len(busySpinnerFrames) + 1}
	for _, idx := range idxCases {
		i := idx % len(busySpinnerFrames)
		want := string(busySpinnerFrames[i]) + " working"
		if got := busyLine(idx); got != want {
			t.Errorf("busyLine(%d) = %q, want %q", idx, got, want)
		}
	}

	t.Run("reduced-motion", func(t *testing.T) {
		t.Setenv("EITRI_NO_MOTION", "1")
		if got := busyLine(0); got != "… thinking" {
			t.Errorf("reduced-motion busyLine = %q, want %q", got, "… thinking")
		}
	})
}

// TestRender_plural table-tests the plural suffix: "" for one, "s" otherwise.
func TestRender_plural(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, ""}, {0, "s"}, {2, "s"}, {100, "s"},
	}
	for _, c := range cases {
		if got := plural(c.n); got != c.want {
			t.Errorf("plural(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestRender_truncateWidth table-tests the width-aware truncation: the longest
// rune prefix of width at most w (the caller appends the ellipsis).
func TestRender_truncateWidth(t *testing.T) {
	cases := []struct {
		s    string
		w    int
		want string
	}{
		{"", 5, ""},
		{"abc", 0, ""},
		{"abc", -1, ""},
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"héllo", 3, "hél"},
	}
	for _, c := range cases {
		if got := truncateWidth(c.s, c.w); got != c.want {
			t.Errorf("truncateWidth(%q, %d) = %q, want %q", c.s, c.w, got, c.want)
		}
	}
}

// TestRender_tokenEstimate table-tests the ~4 chars/token yardstick.
func TestRender_tokenEstimate(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abcd", 1},     // 4 chars / 4
		{"abcdefgh", 2}, // 8 chars / 4
		{"é", 0},        // rune-counted: 1 / 4 = 0
	}
	for _, c := range cases {
		if got := tokenEstimate(c.s); got != c.want {
			t.Errorf("tokenEstimate(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// TestRender_lineCount table-tests the row-count derivation: the number of
// newline-separated lines, where a trailing newline adds no extra row.
func TestRender_lineCount(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"a\nb\nc\n", 3},
	}
	for _, c := range cases {
		if got := lineCount(c.s); got != c.want {
			t.Errorf("lineCount(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// TestRender_clipReviewRegion is obsolete with the modal review panel (issue
// #276): the height-clipped review region it clipped is gone, so the helper
// and its table test are deleted rather than re-homed — tall card diffs now
// clip against the native history viewport's own height clamp.

// TestRender_bottomSlice table-tests the bottom-anchored slice: newest lines
// kept, head dropped when the history overflows the viewport.
func TestRender_bottomSlice(t *testing.T) {
	cases := []struct {
		name    string
		content string
		vh      int
		want    string
	}{
		{"fits", "a\nb", 3, "a\nb"},
		{"overflows", "a\nb\nc\nd", 2, "c\nd"},
		{"negative", "a\nb", -1, ""},
	}
	for _, c := range cases {
		if got := bottomSlice(c.content, c.vh); got != c.want {
			t.Errorf("%s: bottomSlice(%q,%d) = %q, want %q", c.name, c.content, c.vh, got, c.want)
		}
	}
}

// TestRender_readRangeHint table-tests the read range extraction: both
// start_line and end_line must be present as positive integers.
func TestRender_readRangeHint(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"valid", `{"start_line":1,"end_line":40}`, "1-40"},
		{"only-start", `{"start_line":1}`, ""},
		{"fractional", `{"start_line":1.5,"end_line":40}`, ""},
		{"non-positive", `{"start_line":0,"end_line":40}`, ""},
		{"malformed", "not-json", ""},
		{"omitted", `{}`, ""},
	}
	for _, c := range cases {
		if got := readRangeHint(c.args); got != c.want {
			t.Errorf("%s: readRangeHint(%q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

// TestRender_toolArgsHint table-tests the display-arg extraction: path for file
// tools, command for bash, url for web, else the trimmed raw string.
func TestRender_toolArgsHint(t *testing.T) {
	cases := []struct {
		name string
		args string
		want string
	}{
		{"path", `{"path":"internal/main.go"}`, "internal/main.go"},
		{"command", `{"command":"go test"}`, "go test"},
		{"url", `{"url":"https://x.dev"}`, "https://x.dev"},
		{"empty-object", `{}`, ""},
		{"raw", `hello world`, "hello world"},
		{"raw-object", `{"value":1}`, ""},
	}
	for _, c := range cases {
		if got := toolArgsHint(c.args); got != c.want {
			t.Errorf("%s: toolArgsHint(%q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}
