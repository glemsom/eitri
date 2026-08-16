package tools

import (
	"strings"
	"testing"
)

// TestBashDescriptionGuidance locks in the provider-facing bash description:
// it must advertise, in the model's own terms, that output is
// combined stdout+stderr passed through a deterministic compressor (ANSI
// stripped, repeated consecutive lines collapsed, progress/redraw frames
// collapsed), that listings longer than a bounded line budget are truncated
// with an explicit "+N more" marker and never silently, and that re-running a
// command is the recovery path. A future edit that drops any of this guidance
// fails the test.
func TestBashDescriptionGuidance(t *testing.T) {
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
