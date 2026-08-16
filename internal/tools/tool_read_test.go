package tools

import (
	"strings"
	"testing"
)

// TestReadDescriptionGuidance locks in the provider-facing read description:
// the same string every wire dialect sends to the model. It must state that
// the ENTIRE file (whole-file default, no error), recommend explicit 1-based
// line ranges for large files, mention line-numbered output and the explicit
// "+N more" truncation marker, and never regress to the cost-blind
// "optionally limited" phrasing. A future edit that drops any of this guidance
// fails the test.
func TestReadDescriptionGuidance(t *testing.T) {
	desc := (&readTool{}).Description()
	folded := strings.ToLower(desc)
	for _, want := range []string{
		"entire file",   // whole-file default when limits are omitted/null
		"1-based",       // explicit line ranges are 1-based
		"large files",   // prefer explicit line ranges for large files
		"line-numbered", // output is line-numbered
	} {
		if !strings.Contains(folded, want) {
			t.Fatalf("read description missing %q: %s", want, desc)
		}
	}
	if !strings.Contains(desc, "+N more") { // marker is case-sensitive in the spec
		t.Fatalf("read description missing %q: %s", "+N more", desc)
	}
	if strings.Contains(folded, "optionally limited") {
		t.Fatalf("read description regressed to cost-blind phrasing: %s", desc)
	}
}
