package assets

import (
	"io"
	"strings"
	"testing"
)

func TestEmbeddedCSSBalanced(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	open := strings.Count(css, "{")
	close := strings.Count(css, "}")
	if open != close {
		t.Fatalf("unbalanced braces in embedded eitri.css: %d { vs %d }", open, close)
	}
	t.Logf("eitri.css: %d bytes, %d opening braces, %d closing braces — balanced", len(css), open, close)
}

func TestEmbeddedCSSContainsCriticalSelectors(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	// Walk characters tracking brace depth to extract only top-level selectors
	// (the text immediately before each '{' at depth 0).
	type selRange struct {
		start int
		end   int // position of '{'
	}
	var topLevelSelectors []selRange
	depth := 0
	selStart := -1

	for i, ch := range css {
		if ch == '{' {
			if depth == 0 && selStart >= 0 {
				topLevelSelectors = append(topLevelSelectors, selRange{start: selStart, end: i})
				selStart = -1
			}
			depth++
		} else if ch == '}' {
			depth--
			if depth < 0 {
				t.Fatalf("unexpected closing brace at position %d", i)
			}
		} else if depth == 0 && selStart < 0 {
			// Not inside a rule block — start tracking next selector
			if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' {
				selStart = i
			}
		}
	}

	// Extract selector text and strip comments
	var selectors []string
	for _, sr := range topLevelSelectors {
		raw := css[sr.start:sr.end]
		raw = stripCSSComments(raw)
		raw = strings.TrimSpace(raw)
		if raw != "" {
			selectors = append(selectors, raw)
		}
	}

	// Critical selectors that must exist for core UI features to work
	critical := []string{
		".context-idle",
		".context-idle.open",
		".context-expanded",
		".context-expanded.open",
		".context-compact",
		"eitri-context",
		".context-category-bar-fill",
		".sidebar-panel",
		"#session-tabs",
		".btn-primary",
		".btn-danger",
		".btn-secondary",
		".tool-entry",
		".mermaid-diagram",
		".compact-btn",
		".report-view",
		".report-header",
		".report-timeline",
		".turn-card",
		".tool-call-card",
		".termination-chip",
	}

	for _, sel := range critical {
		found := false
		for _, s := range selectors {
			// Split multi-selector groups on comma and check each
			for _, part := range strings.Split(s, ",") {
				part = strings.TrimSpace(part)
				if part == sel {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("missing critical CSS selector %q in embedded eitri.css", sel)
		}
	}
	t.Logf("checked %d critical selectors — all present", len(critical))
}

// TestEmbeddedCSSSelfHostedFonts verifies Inter and JetBrains Mono are declared
// via local @font-face rules (served from /static/fonts/*, embedded in the
// binary) with font-display: swap, and that eitri.css never references an
// external font CDN. (issue #970)
func TestEmbeddedCSSSelfHostedFonts(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	for _, tc := range []struct {
		family string
		subset string
		file   string
	}{
		{"Inter", "latin", "Inter-latin.woff2"},
		{"Inter", "latin-ext", "Inter-latin-ext.woff2"},
		{"JetBrains Mono", "latin", "JetBrainsMono-latin.woff2"},
		{"JetBrains Mono", "latin-ext", "JetBrainsMono-latin-ext.woff2"},
	} {
		if !strings.Contains(css, "@font-face") {
			t.Fatalf("eitri.css contains no @font-face rules")
		}
		if !strings.Contains(css, "font-family: '"+tc.family+"'") {
			t.Errorf("missing @font-face for %q", tc.family)
		}
		// Local file reference resolving under /static/fonts/.
		if !strings.Contains(css, `url("fonts/`+tc.file+`")`) {
			t.Errorf("missing local src url for %q subset %q (%s)", tc.family, tc.subset, tc.file)
		}
		// Each declared file must exist in the embedded fonts directory.
		if _, err := Files.Open("fonts/" + tc.file); err != nil {
			t.Errorf("embedded font file fonts/%s not found: %v", tc.file, err)
		}
	}

	// font-display: swap must be applied so text renders before fonts load.
	if !strings.Contains(css, "font-display: swap") {
		t.Errorf("eitri.css @font-face rules are missing font-display: swap")
	}

	// No external font CDN references may remain.
	for _, cdn := range []string{"fonts.googleapis.com", "fonts.gstatic.com"} {
		if strings.Contains(css, cdn) {
			t.Errorf("eitri.css must not reference external font CDN %q (self-hosted only)", cdn)
		}
	}
}

func stripCSSComments(s string) string {
	for {
		start := strings.Index(s, "/*")
		if start < 0 {
			break
		}
		end := strings.Index(s[start+2:], "*/")
		if end < 0 {
			break // unclosed comment — ignore rest
		}
		s = s[:start] + s[start+2+end+2:]
	}
	return s
}
