package assets

import (
	"io"
	"regexp"
	"strings"
	"testing"
)

// Tests for interactivity-state and responsive-depth work (issue #1116).
//
// These assert spec-derived outcomes at the embedded-asset seam: every
// interactive control must expose hover, press (active) and visible focus
// feedback drawn from the canonical focus-ring token, and the narrow-viewport
// layouts for the sessions table, tool-activity sidebar and report page must
// exist. They complement the token/color/symmetry lints in css_test.go.

func readEmbeddedCSSFile(t *testing.T) string {
	t.Helper()
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	return string(data)
}

// tokenRootsBodies returns the raw bodies of all :root rules (dark top-level
// plus the nested light-theme override).
func tokenRootsBodies(css string) []string {
	var bodies []string
	for _, r := range parseCSSRules(css) {
		if r.selector == ":root" {
			bodies = append(bodies, r.body)
		}
	}
	return bodies
}

// bodyDeclares reports whether a token-root body declares name.
func bodyDeclares(body, name string) bool {
	for _, decl := range strings.Split(body, ";") {
		decl = strings.TrimSpace(decl)
		colon := strings.IndexByte(decl, ':')
		if colon > 0 && strings.TrimSpace(decl[:colon]) == name {
			return true
		}
	}
	return false
}

// stateful returns a compact representation of every selector state rule for
// later checks: a slice of rule selectors, each with the pseudo-classes in use.
type stateRule struct {
	selector string
	hovers   bool
	active   bool
	focus    bool
}

// classifyStateRules parses all rule selectors (top-level and nested) and
// records whether each contains :hover, :active and focus (:focus-visible /
// :focus) pseudo-classes.
func classifyStateRules(css string) []stateRule {
	var out []stateRule
	for _, r := range parseCSSRules(css) {
		if strings.HasPrefix(r.selector, "@") {
			continue // wrappers scanned individually already by parser
		}
		out = append(out, stateRule{
			selector: r.selector,
			hovers:   strings.Contains(r.selector, ":hover"),
			active:   strings.Contains(r.selector, ":active"),
			focus:    strings.Contains(r.selector, ":focus-visible") || strings.Contains(r.selector, ":focus"),
		})
	}
	return out
}

// TestInteractivityFocusRingToken verifies the canonical focus-visible ring
// token is declared in every token root and consumed by at least one rule.
func TestInteractivityFocusRingToken(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	roots := tokenRootsBodies(css)
	if len(roots) < 2 {
		t.Fatalf("expected dark + light token roots, found %d", len(roots))
	}
	for i, body := range roots {
		if !bodyDeclares(body, "--focus-ring") {
			t.Errorf("token root %d is missing --focus-ring", i)
		}
	}
	if !strings.Contains(css, "var(--focus-ring)") {
		t.Errorf("no rule consumes var(--focus-ring)")
	}
}

// TestInteractivityStatesEverywhere checks the primary interactive controls
// expose all three states: hover, press (:active) and visible focus
// (:focus-visible / :focus). Slices of selectors are matched by suffix so
// label/compound selectors (e.g. .session-item-link:hover) count.
func TestInteractivityStatesEverywhere(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	rules := classifyStateRules(css)

	interactive := []struct {
		name      string
		anchor    string
		hoverSfx  string // slice to look for on a hovered selector
		activeSfx string
		focusSfx  string
	}{
		{"session-item", ".session-item", ":hover", ":active", ":focus"},
		{"tool-entry", ".tool-entry", ":hover", ":active", ":focus"},
		{"nav-link", ".nav-link", ":hover", ":active", ":focus"},
		{"quick-reply-chip", "quick-reply-chip", ":hover", ":active", ":focus"},
		{"completion-item", "completion-item", ":hover", ":active", ":focus"},
		{"persona-option", "persona-option", ":hover", ":active", ":focus"},
		{"tool-call-summary", "tool-call-summary", ":hover", ":active", ":focus"},
		{"run-select", "run-select", ":hover", ":active", ":focus"},
	}

	has := func(anchor, suffix string) bool {
		for _, r := range rules {
			if strings.Contains(r.selector, anchor) {
				if suffix == ":hover" && r.hovers {
					return true
				}
				if suffix == ":active" && r.active {
					return true
				}
				if suffix == ":focus" && r.focus {
					return true
				}
			}
		}
		return false
	}

	for _, it := range interactive {
		if !has(it.anchor, ":hover") {
			t.Errorf("%s: missing :hover state", it.name)
		}
		if !has(it.anchor, ":active") {
			t.Errorf("%s: missing :active (pressed) state", it.name)
		}
		if !has(it.anchor, ":focus") {
			t.Errorf("%s: missing :focus / :focus-visible state", it.name)
		}
	}
}

// mediaQuerySelectors returns the selector groups declared inside every
// @media block whose query contains "max-width" (narrow viewports). CSS
// comments are stripped and multi-selector groups are split on commas so the
// caller can match individual selectors.
func mediaQuerySelectors(css string) []string {
	var all []string
	rx := regexp.MustCompile(`@media\s*\(([^)]*max-width[^)]*)\)\s*\{`)
	for _, m := range rx.FindAllStringIndex(css, -1) {
		open := m[1] - 1 // position of '{'
		// scan balanced braces from open
		depth := 0
		j := open
		for j < len(css) {
			switch css[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					region := stripCSSComments(css[open+1 : j])
					sub := regexp.MustCompile(`(?m)^\s*([^{};]+)\{`)
					for _, sm := range sub.FindAllStringSubmatch(region, -1) {
						grp := strings.TrimSpace(sm[1])
						if grp == "" || strings.HasPrefix(grp, "@") {
							continue
						}
						for _, part := range strings.Split(grp, ",") {
							part = strings.TrimSpace(part)
							if part != "" {
								all = append(all, part)
							}
						}
					}
					goto next
				}
			}
			j++
		}
	next:
	}
	return all
}

// TestResponsiveNarrowViewport verifies narrow-viewport layout overrides exist
// for the sessions table, tool-activity sidebar and report page.
func TestResponsiveNarrowViewport(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	selectors := mediaQuerySelectors(css)
	if len(selectors) == 0 {
		t.Fatalf("no @media(max-width:...) blocks found in eitri.css")
	}

	cases := []struct {
		name     string
		selector string
	}{
		{"sessions-table", ".sessions-table"},
		{"tool-activity", "#tool-activity"},
		{"report-view", ".report-view"},
	}

	present := map[string]bool{}
	for _, s := range selectors {
		present[s] = true
	}
	for _, cur := range cases {
		if !present[cur.selector] {
			t.Errorf("narrow-viewport layout missing override for %s (selector %s)", cur.name, cur.selector)
		}
	}
}
