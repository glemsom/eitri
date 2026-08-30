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
