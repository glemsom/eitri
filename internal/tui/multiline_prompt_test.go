package tui

import (
	"strings"
	"testing"
)

func TestTranscriptUserPromptPreservesMultilineInput(t *testing.T) {
	tx := &Transcript{theme: themeFor("dark"), configTheme: "dark", width: 80}
	tx.appendUserMsg("first line\nsecond line")

	var hist strings.Builder
	tx.renderHistory(&hist, nil, nil)
	plain := ansiStrip(hist.String())

	first := strings.Index(plain, "first line")
	second := strings.Index(plain, "second line")
	if first < 0 || second < 0 || second <= first {
		t.Fatalf("multiline prompt rendered without both lines in order:\n%q\n%s", plain, plain)
	}
	between := plain[first+len("first line") : second]
	if !strings.Contains(between, "\n") {
		t.Fatalf("multiline prompt rendered both lines on the same row:\n%q\n%s", plain, plain)
	}
	if strings.Contains(plain, "first line second line") {
		t.Fatalf("multiline prompt collapsed newline to a space:\n%q\n%s", plain, plain)
	}
}

// TestRenderPromptMarkdownPreservesExactNewlines guards against glamour's
// per-paragraph padding leaking phantom blank rows into the prompt card: each
// line is rendered in isolation, so any blank glamour inserts above/below a
// part must be stripped before the parts are joined. The rendered plain text
// must contain exactly as many newlines as the user typed — never more.
func TestRenderPromptMarkdownPreservesExactNewlines(t *testing.T) {
	cases := []string{
		"single line",
		"first line\nsecond line",
		"a\n\n\nb",
		"\nleading and trailing\n",
	}
	for _, in := range cases {
		md, err := RenderPromptMarkdown(in, 80, "dark")
		if err != nil {
			t.Fatalf("RenderPromptMarkdown(%q): %v", in, err)
		}
		plain := ansiStrip(md)
		got := strings.Count(plain, "\n")
		want := strings.Count(in, "\n")
		if got != want {
			t.Errorf("prompt %q: rendered %d newlines, want %d\n%s", in, got, want, plain)
		}
	}
}

func TestRenderPromptMarkdownPreservesTaskListMarker(t *testing.T) {
	md, err := RenderPromptMarkdown("- [ ] Give me a a line of random text", 80, "dark")
	if err != nil {
		t.Fatalf("RenderPromptMarkdown: %v", err)
	}
	plain := ansiStrip(md)
	if !strings.Contains(plain, "- [ ] Give me a a line of random text") {
		t.Fatalf("prompt card must echo the user's literal task-list marker, got:\n%q\n%s", plain, plain)
	}
}
