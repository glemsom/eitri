package assets

import (
	"strings"
	"testing"
)

// Tests for header chrome polish (issue #1180).
//
// These assert spec-derived outcomes at the embedded-asset seam: roomier
// header padding, the brand title at 1.2rem SemiBold, the workspace indicator
// restyled as an interactive chip, and the stream status rendered as a tinted
// pill rather than raw colored text. They complement the token/color/symmetry
// lints in css_test.go, which still guarantee every color flows from a
// semantic token (a tint is derived via color-mix over the status token, so no
// new literals are introduced).

// TestHeaderPolishPadding verifies the header uses the roomier padding
// requested by the issue (0.75rem 1.5rem instead of 0.5rem 1rem).
func TestHeaderPolishPadding(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	for _, r := range parseCSSRules(css) {
		if strings.TrimSpace(r.selector) != "header" {
			continue
		}
		if !strings.Contains(r.body, "padding: 0.75rem 1.5rem") {
			t.Errorf("header rule missing padding: 0.75rem 1.5rem\n  %s", r.body)
		}
		return
	}
	t.Error("no top-level `header` rule found in eitri.css")
}

// TestHeaderPolishBrandTitle verifies the brand h1 is bumped to 1.2rem at
// SemiBold weight while retaining the existing negative letter-spacing.
func TestHeaderPolishBrandTitle(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	for _, r := range parseCSSRules(css) {
		if strings.TrimSpace(r.selector) != "header h1" {
			continue
		}
		if !strings.Contains(r.body, "font-size: 1.2rem") {
			t.Errorf("header h1 not bumped to font-size: 1.2rem\n  %s", r.body)
		}
		if !strings.Contains(r.body, "font-weight: 600") {
			t.Errorf("header h1 missing SemiBold font-weight: 600\n  %s", r.body)
		}
		if !strings.Contains(r.body, "letter-spacing: -0.02em") {
			t.Errorf("header h1 missing negative letter-spacing -0.02em\n  %s", r.body)
		}
		return
	}
	t.Error("no top-level `header h1` rule found in eitri.css")
}

// TestHeaderPolishWorkspaceChip verifies the workspace indicator reads as an
// interactive chip: a background tint, border, radius, padding, pointer cursor
// and truncation. Only the single canonical definition is counted, matching
// TestEmbeddedCSSWorkspaceIndicatorSingleDefinition.
func TestHeaderPolishWorkspaceChip(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	for _, r := range parseCSSRules(css) {
		if strings.TrimSpace(r.selector) != ".workspace-indicator" {
			continue
		}
		body := r.body
		for _, want := range []string{
			"background: var(--surface-alt)",
			"border: 1px solid var(--border)",
			"border-radius: 6px",
			"padding:",
			"cursor: pointer",
			"overflow: hidden",
			"text-overflow: ellipsis",
		} {
			if !strings.Contains(body, want) {
				t.Errorf(".workspace-indicator chip missing %q\n  %s", want, body)
			}
		}
		return
	}
	t.Error("no top-level .workspace-indicator rule found in eitri.css")
}

// TestHeaderPolishStatusPill verifies the stream status renders as a pill with
// a subtle background tint matching the status color via color-mix over the
// semantic status token — never a raw color literal.
func TestHeaderPolishStatusPill(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	for _, r := range parseCSSRules(css) {
		if strings.TrimSpace(r.selector) != ".stream-status-text" {
			continue
		}
		for _, want := range []string{
			"border-radius: 999px",
			"padding:",
		} {
			if !strings.Contains(r.body, want) {
				t.Errorf(".stream-status-text pill missing %q\n  %s", want, r.body)
			}
		}
		return
	}
	t.Error("no top-level .stream-status-text rule found in eitri.css")

	// Exactly one status-tinted pill rule per stream status class, derived
	// from color-mix (never a literal color).
	tinted := 0
	for _, r := range parseCSSRules(css) {
		sel := strings.TrimSpace(r.selector)
		if strings.HasPrefix(sel, ".stream-status-text.") {
			for _, part := range []string{"background: color-mix", "border-color: color-mix"} {
				if !strings.Contains(r.body, part) {
					t.Errorf("%s pill missing %q\n  %s", sel, part, r.body)
				}
			}
			tinted++
		}
	}
	if tinted < 6 {
		t.Errorf("expected at least 6 tinted status pill rules, found %d", tinted)
	}
}

// TestHeaderPolishMobileTruncation verifies the narrow-viewport layout keeps
// the header in a single row and lets the workspace chip truncate rather than
// overflow.
func TestHeaderPolishMobileTruncation(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	mobile := mediaBlockRules(css, "(max-width: 768px)")
	if mobile == nil {
		t.Fatal("no (max-width: 768px) media block found")
	}
	found := false
	for _, r := range mobile {
		for _, part := range strings.Split(r.selector, ",") {
			p := strings.TrimSpace(part)
			if p == "#workspace-indicator" {
				if !strings.Contains(r.body, "overflow: hidden") ||
					!strings.Contains(r.body, "text-overflow: ellipsis") {
					t.Errorf("mobile #workspace-indicator missing truncation\n  %s", r.body)
				}
				if !strings.Contains(r.body, "max-width:") {
					t.Errorf("mobile #workspace-indicator missing max-width\n  %s", r.body)
				}
				found = true
			}
			if p == "header" && !strings.Contains(r.body, "gap:") {
				t.Errorf("mobile header missing tightened gap for single-row fit\n  %s", r.body)
			}
		}
	}
	if !found {
		t.Error("no mobile #workspace-indicator rule found for truncation")
	}
}
