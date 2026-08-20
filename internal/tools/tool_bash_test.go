package tools

import (
	"strings"
	"testing"
)

func TestBashDescriptionGuidance(t *testing.T) {
	t.Parallel()
	desc := (&bashTool{}).Description()
	folded := strings.ToLower(desc)
	for _, want := range []string{
		"stdout",        // combined stream keeps stdout
		"stderr",        // ... and stderr
		"compress",      // output passes through a compressor
		"ansi",          // ANSI escape sequences stripped
		"collapsed",     // repeated consecutive lines / redraw frames collapse
		"re-run",        // re-running the command is the recovery path
		"recovery",      // ... recovery path for truncated listings
		"truncated",     // heavy listings are truncated
		"deterministic", // same command yields the same compressed form
	} {
		if !strings.Contains(folded, want) {
			t.Fatalf("bash description missing %q: %s", want, desc)
		}
	}
	if !strings.Contains(desc, "+N more") { // marker is case-sensitive in the spec
		t.Fatalf("bash description missing %q: %s", "+N more", desc)
	}
	if !strings.Contains(folded, "never silent") && !strings.Contains(folded, "not silent") {
		t.Fatalf("bash description must state truncation is never silent: %s", desc)
	}
}
