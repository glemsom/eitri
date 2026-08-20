package tui

import (
	"testing"
	"time"
)

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

func TestRender_busyLine(t *testing.T) {
	idxCases := []int{0, 1, len(busySpinnerFrames) - 1, len(busySpinnerFrames), len(busySpinnerFrames) + 1}
	for _, idx := range idxCases {
		i := idx % len(busySpinnerFrames)
		want := string(busySpinnerFrames[i]) + " Answering"
		if got := busyLine(idx, PhaseAnswering); got != want {
			t.Errorf("busyLine(%d) = %q, want %q", idx, got, want)
		}
	}

	t.Run("reduced-motion", func(t *testing.T) {
		t.Setenv("EITRI_NO_MOTION", "1")
		if got := busyLine(0, PhaseWorking); got != "… thinking" {
			t.Errorf("reduced-motion busyLine = %q, want %q", got, "… thinking")
		}
	})
}

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
