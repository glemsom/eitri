package tools

import (
	"strings"
	"testing"
)

// TestEditDescriptionGuidance locks in the provider-facing edit description
// (issue #285): the same string every wire dialect sends to the model. It
// must state the exact-match and exactly-once uniqueness contract, that a
// zero/multi match is a hard error with no silent partial application,
// recommend widening old_string with surrounding context when the target
// appears multiple times, require basing old_string on a fresh read, and
// state the writable-root constraint. A future edit that drops any of this
// guidance fails the test.
func TestEditDescriptionGuidance(t *testing.T) {
	desc := (&editTool{}).Description()
	folded := strings.ToLower(desc)
	for _, want := range []string{
		"occur exactly once",  // old_string must occur exactly once
		"widen",               // widen old_string with surrounding context
		"surrounding context", // context = enclosing signature / neighbouring line
		"fresh read",          // base old_string on a fresh read of the file
		"writable root",       // target inside a writable root
		"hard error",          // zero/multi matches are a hard error
		"no silent partial",   // never partially apply
	} {
		if !strings.Contains(folded, want) {
			t.Fatalf("edit description missing %q: %s", want, desc)
		}
	}
}
