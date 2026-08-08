package assets

import (
	"strings"
	"testing"
)

// Tests for the settings-page collapsible sections (issue #1257).
//
// The sections are native <details>/<summary> elements. Chrome hides closed
// <details> content through a UA rule that an author rule on a child (the
// .form-group { display: flex } layout rule) can override, so a closed section
// still rendered its body. The embedded stylesheet must therefore carry an
// explicit author rule that forces non-summary children of a closed
// .settings-details to display:none. This guard asserts that rule exists in
// the aggregate that ships to the browser (the rendered-box-model guarantee is
// covered by the browser E2E suite).

func TestSettingsCollapsedSectionsHideContent(t *testing.T) {
	css := readEmbeddedCSSFile(t)

	var matches []cssRule
	for _, r := range parseCSSRules(css) {
		if strings.Contains(r.selector, ".settings-details:not([open])") {
			matches = append(matches, r)
		}
	}
	if len(matches) == 0 {
		t.Fatal("eitri.css must contain a rule hiding the content of a closed .settings-details section (issue #1257)")
	}
	for _, r := range matches {
		if strings.Contains(r.body, "display: none") {
			return
		}
	}
	t.Fatalf("eitri.css rule(s) %v for closed .settings-details never declare display: none — section bodies stay visible in Chrome", func() []string {
		var sels []string
		for _, r := range matches {
			sels = append(sels, r.selector)
		}
		return sels
	}())
}
