package tools

import (
	"strings"
	"testing"
)

func TestEditDescriptionGuidance(t *testing.T) {
	t.Parallel()
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
