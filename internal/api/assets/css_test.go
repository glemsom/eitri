package assets

import (
	"io"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// hexColorRe matches hex and rgba() color literals in eitri.css. The design
// token system (issue #1068) requires every color to flow from a semantic
// token declared in a :root block, so these literals may only appear there.
var hexColorRe = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|rgba\([^)]*\)`)

// varTokenRe matches var(--name) custom-property references.
var varTokenRe = regexp.MustCompile(`var\(--([a-zA-Z0-9-]+)`)

// cssRule is one parsed rule block from eitri.css. Nested rules (media
// queries, keyframe steps) are parsed as their own entries.
type cssRule struct {
	selector string // trimmed selector text (comments stripped)
	body     string // raw text between the outer braces (comments stripped)
	start    int    // byte offset of the selector start, for line math
}

// parseCSSRules recursively parses all rule blocks in css, including nested
// ones (e.g. the `:root` and Prism overrides inside the light-theme @media
// block, keyframe steps inside @keyframes).
func parseCSSRules(css string) []cssRule {
	return parseCSSBlock(css, 0, len(css))
}

func parseCSSBlock(css string, start, end int) []cssRule {
	var rules []cssRule
	i := start
	for i < end {
		brace := strings.IndexByte(css[i:end], '{')
		if brace < 0 {
			break
		}
		brace += i
		sel := strings.TrimSpace(stripCSSComments(css[i:brace]))
		depth := 1
		j := brace + 1
		for j < end && depth > 0 {
			switch css[j] {
			case '{':
				depth++
			case '}':
				depth--
			}
			j++
		}
		if sel != "" {
			rules = append(rules, cssRule{
				selector: sel,
				body:     stripCSSComments(css[brace+1 : j-1]),
				start:    i,
			})
		}
		// Recurse into the block to capture nested rules.
		rules = append(rules, parseCSSBlock(css, brace+1, j-1)...)
		i = j
	}
	return rules
}

// cssLine returns the 1-based line number for a byte offset in css.
func cssLine(css string, offset int) int {
	return strings.Count(css[:offset], "\n") + 1
}

// tokenRoots returns the parsed :root blocks (dark top-level and the nested
// light-theme override inside @media (prefers-color-scheme: light)).
func tokenRoots(css string) []cssRule {
	var roots []cssRule
	for _, r := range parseCSSRules(css) {
		if r.selector == ":root" {
			roots = append(roots, r)
		}
	}
	return roots
}

// tokenNames extracts the custom-property names declared in a token root body.
func tokenNames(body string) []string {
	var names []string
	for _, decl := range strings.Split(body, ";") {
		decl = strings.TrimSpace(decl)
		if !strings.HasPrefix(decl, "--") {
			continue
		}
		colon := strings.IndexByte(decl, ':')
		if colon < 0 {
			continue
		}
		names = append(names, strings.TrimSpace(decl[:colon]))
	}
	sort.Strings(names)
	return names
}

// isPrismSyntaxRule reports whether a rule belongs to the Prism syntax
// highlighting palette (the light-mode overrides of vendored prism.min.css).
// This is the only surface exempt from the token-only color rule: it is a
// fixed syntax palette, not a theme color, and dark mode inherits the vendored
// default untouched.
func isPrismSyntaxRule(sel string) bool {
	return strings.Contains(sel, `class*="language-"`) || strings.Contains(sel, ".token.")
}

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
		".context-category.context-sub",
		".context-category.context-sub .context-sub-value",
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

// TestEmbeddedCSSAllTokensDefined verifies every var(--token) referenced by
// eitri.css is declared in a :root token block. Undefined custom properties
// silently resolve to nothing (or a stale fallback), which is how persona
// cards, the sessions table, form errors, and typing dots lost their intended
// colors. (issue #1068)
func TestEmbeddedCSSAllTokensDefined(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	defined := map[string]bool{}
	for _, r := range tokenRoots(css) {
		for _, name := range tokenNames(r.body) {
			defined[strings.TrimPrefix(name, "--")] = true
		}
	}

	referenced := map[string]bool{}
	for _, m := range varTokenRe.FindAllStringSubmatch(css, -1) {
		referenced[m[1]] = true
	}

	// --composer-bottom is set dynamically by eitri-composer.js at runtime
	// (it tracks the composer's height while the textarea grows) and carries a
	// var(--composer-height) fallback chain at its use site.
	dynamic := map[string]bool{"composer-bottom": true}

	var missing []string
	for name := range referenced {
		if defined[name] || dynamic[name] {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("var(--%s) referenced but never declared in a :root token block",
			strings.Join(missing, "), var(--"))
	}
	t.Logf("checked %d referenced tokens against %d declared in :root blocks",
		len(referenced), len(defined))
}

// TestEmbeddedCSSNoBareHexOutsideTokenRoot is the CI lint rule for the design
// token system: bare hex/rgba colors may only appear inside the token root
// declarations. Any color added to a component rule outside the roots fails
// this test, preventing hardcoded colors from leaking back in. (issue #1068)
func TestEmbeddedCSSNoBareHexOutsideTokenRoot(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	offenders := 0
	for _, r := range parseCSSRules(css) {
		if r.selector == ":root" {
			continue // token root — this is where color literals live
		}
		if strings.HasPrefix(r.selector, "@") {
			// At-rules (@media, @keyframes) are wrappers: their nested rules
			// are scanned individually by the parser, so scanning the wrapper
			// body would double-report every nested color.
			continue
		}
		if isPrismSyntaxRule(r.selector) {
			continue // fixed syntax palette — see isPrismSyntaxRule
		}
		for _, m := range hexColorRe.FindAllString(r.body, -1) {
			offenders++
			t.Errorf("%s (line %d): bare color %s outside token root declarations",
				r.selector, cssLine(css, r.start), m)
		}
	}
	if offenders > 0 {
		t.Fatalf("eitri.css has %d bare color(s) outside the token root — promote them to semantic tokens", offenders)
	}
	t.Log("no bare hex/rgba colors outside the token root declarations")
}

// TestEmbeddedCSSTokenRootSymmetry verifies the light-theme token root declares
// exactly the same custom properties as the dark theme root. A token missing
// from the light block silently inherits its dark value, which is how
// dark-theme colors leak into light mode (user-message tint, context-warning
// tint, header bar). (issue #1068)
func TestEmbeddedCSSTokenRootSymmetry(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	var dark, light []string
	for _, r := range tokenRoots(css) {
		if strings.Contains(css[:r.start], "prefers-color-scheme: light") {
			light = tokenNames(r.body)
		} else {
			dark = tokenNames(r.body)
		}
	}
	if len(dark) == 0 || len(light) == 0 {
		t.Fatalf("expected dark and light token roots, got dark=%d light=%d", len(dark), len(light))
	}

	var onlyDark, onlyLight []string
	di, li := 0, 0
	for di < len(dark) || li < len(light) {
		switch {
		case li >= len(light):
			onlyDark = append(onlyDark, dark[di])
			di++
		case di >= len(dark):
			onlyLight = append(onlyLight, light[li])
			li++
		case dark[di] == light[li]:
			di++
			li++
		case dark[di] < light[li]:
			onlyDark = append(onlyDark, dark[di])
			di++
		default:
			onlyLight = append(onlyLight, light[li])
			li++
		}
	}
	if len(onlyDark) > 0 || len(onlyLight) > 0 {
		if len(onlyDark) > 0 {
			t.Errorf("tokens declared in dark :root but missing from light theme: %s",
				strings.Join(onlyDark, ", "))
		}
		if len(onlyLight) > 0 {
			t.Errorf("tokens declared in light theme but missing from dark :root: %s",
				strings.Join(onlyLight, ", "))
		}
		t.Fatalf("light theme token root is not symmetric with dark — dark colors may leak into light mode")
	}
	t.Logf("dark and light token roots both declare %d tokens", len(dark))
}

// TestEmbeddedCSSTokensAllUsed verifies every token declared in a :root block
// is actually referenced somewhere in the stylesheet. Declared-but-unused
// tokens are dead weight and drift from the theme blocks. (issue #1072)
func TestEmbeddedCSSTokensAllUsed(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	referenced := map[string]bool{}
	for _, m := range varTokenRe.FindAllStringSubmatch(css, -1) {
		referenced[m[1]] = true
	}

	var unused []string
	seen := map[string]bool{}
	for _, r := range tokenRoots(css) {
		for _, name := range tokenNames(r.body) {
			if seen[name] {
				continue
			}
			seen[name] = true
			if !referenced[strings.TrimPrefix(name, "--")] {
				unused = append(unused, name)
			}
		}
	}
	sort.Strings(unused)
	if len(unused) > 0 {
		t.Errorf("tokens declared in :root but never referenced by eitri.css: %s",
			strings.Join(unused, ", "))
	}
	t.Logf("all %d declared tokens are referenced", len(referenced))
}

// TestEmbeddedCSSNoOrphanSelectors verifies that no rule targets classes that
// no template, JS, or Go code emits. Orphaned selectors are dead CSS that
// drift out of sync with the UI. (issue #1072)
func TestEmbeddedCSSNoOrphanSelectors(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	// Classes with zero hits in templates/JS/Go. Prism syntax-highlighting
	// selectors (token.*, class*="language-") are exempt: they style markup
	// emitted at runtime by the vendored highlighter.
	orphans := []string{
		"tool-card", "tool-card-header", "tool-args", "tool-cards-container",
		"tool-call-container", "tool-status", "sidebar-footer",
		"settings-header-bar", "skill-scope-icon",
		"session-workspace", "session-workspace-path", "session-workspace-btn",
	}

	var found []string
	for _, r := range parseCSSRules(css) {
		if strings.HasPrefix(r.selector, "@") {
			continue // media/at-rule wrappers carry no selectors of their own
		}
		for _, part := range strings.Split(r.selector, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, ".token.") || strings.Contains(part, `class*="language-"`) {
				continue // Prism palette — see isPrismSyntaxRule
			}
			for _, cls := range orphans {
				// Match the class as a whole token, not as a prefix of
				// another class (e.g. tool-status vs tool-status-label).
				if regexp.MustCompile(`(^|[\s.>~+])\.` + regexp.QuoteMeta(cls) + `($|[\s.:#[,\]>~+])`).MatchString(part) {
					found = append(found, part)
				}
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("orphaned selectors still present in eitri.css:\n  %s",
			strings.Join(found, "\n  "))
	}
	t.Log("no orphaned class selectors remain")
}

// TestEmbeddedCSSNoDuplicateMediaBlocks verifies each @media query condition
// appears exactly once — duplicate blocks with the same condition must be
// merged into one. (issue #1072)
func TestEmbeddedCSSNoDuplicateMediaBlocks(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	counts := map[string]int{}
	for _, m := range regexp.MustCompile(`@media\s+([^{]+)\{`).FindAllStringSubmatch(css, -1) {
		counts[strings.TrimSpace(m[1])]++
	}
	for cond, n := range counts {
		if n > 1 {
			t.Errorf("@media (%s) appears %d times — merge into a single block", cond, n)
		}
	}
	t.Logf("checked %d distinct @media conditions — no duplicates", len(counts))
}

// TestEmbeddedCSSWorkspaceIndicatorSingleDefinition verifies .workspace-indicator
// is defined exactly once with a single hover color. (issue #1072)
func TestEmbeddedCSSWorkspaceIndicatorSingleDefinition(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	var defs []string
	for _, r := range parseCSSRules(css) {
		if strings.HasPrefix(r.selector, "@") {
			continue
		}
		for _, part := range strings.Split(r.selector, ",") {
			if strings.TrimSpace(part) == ".workspace-indicator" ||
				strings.TrimSpace(part) == ".workspace-indicator:hover" {
				defs = append(defs, part+" { "+r.body+" }")
			}
		}
	}
	if len(defs) != 2 {
		t.Errorf("expected exactly one .workspace-indicator and one :hover rule, got %d:\n  %s",
			len(defs), strings.Join(defs, "\n  "))
	}
	t.Logf("%d workspace-indicator rule(s) found", len(defs))
}

// mediaBlockRules returns the rules nested inside the first @media block whose
// condition text exactly matches cond, or nil if no such block exists.
func mediaBlockRules(css, cond string) []cssRule {
	for _, r := range parseCSSRules(css) {
		if strings.TrimSpace(r.selector) == "@media "+cond {
			return parseCSSBlock(r.body, 0, len(r.body))
		}
	}
	return nil
}

// ruleBody returns the body of the first rule in rules whose selector group
// contains exactly sel, or "" if absent.
func ruleBody(rules []cssRule, sel string) string {
	for _, r := range rules {
		for _, part := range strings.Split(r.selector, ",") {
			if strings.TrimSpace(part) == sel {
				return r.body
			}
		}
	}
	return ""
}

// TestEmbeddedCSSPrefersReducedMotion verifies the stylesheet honours
// prefers-reduced-motion: reduce by collapsing every decorative/infinite
// animation — the box-shadow glow pulses, face breathing, typing dots,
// spinners, opacity pulses, and the undo countdown width animation — to a
// static render instead of looping. (issue #1075)
func TestEmbeddedCSSPrefersReducedMotion(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	rules := mediaBlockRules(css, "(prefers-reduced-motion: reduce)")
	if rules == nil {
		t.Fatal("missing @media (prefers-reduced-motion: reduce) block")
	}

	// Selectors whose infinite/decorative animation must be disabled. The
	// avatar glow states also get a static box-shadow so stream status stays
	// visible without pulsing.
	collapsed := []string{
		`.streaming-message .message-avatar-container[data-stream-status="connecting"]`,
		`.streaming-message .message-avatar-container[data-stream-status="tool-running"]`,
		`.streaming-message .message-avatar-container[data-stream-status="streaming"]`,
		`.streaming-message .message-avatar-container[data-stream-status="streaming"] .message-avatar`,
		`.streaming-message .message-avatar-container[data-stream-status="tool-running"] .message-avatar`,
		`.header-face-container[data-stream-status="streaming"] .header-face`,
		`.header-face-container[data-stream-status="tool-running"] .header-face`,
		`.typing-dots span`,
		`.model-refresh-spinner`,
		`.test-connection-pending`,
		`.undo-toast-bar`,
	}
	for _, sel := range collapsed {
		body := ruleBody(rules, sel)
		if body == "" {
			t.Errorf("reduced-motion block missing rule for %s", sel)
			continue
		}
		if !strings.Contains(body, "animation: none") {
			t.Errorf("%s must collapse to animation: none under reduced motion, got: %s", sel, body)
		}
	}
	t.Logf("reduced-motion block disables %d infinite/decorative animations", len(collapsed))
}

// TestEmbeddedCSSPrefersReducedTransparency verifies the stylesheet honours
// prefers-reduced-transparency: reduce by replacing backdrop-filter glass
// panels (dropdowns, menus, modals, sticky footer) with solid fills and no
// backdrop blur. (issue #1075)
func TestEmbeddedCSSPrefersReducedTransparency(t *testing.T) {
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	css := string(data)

	rules := mediaBlockRules(css, "(prefers-reduced-transparency: reduce)")
	if rules == nil {
		t.Fatal("missing @media (prefers-reduced-transparency: reduce) block")
	}

	// Every backdrop-filter surface must lose the blur and gain an explicit
	// solid background. The overlay scrim keeps its dimming background (it is
	// not a glass panel) but must still drop the backdrop blur.
	glassPanels := []string{
		".dropdown-content",
		".persona-dropdown",
		".completion-menu",
		".confirmation-modal",
		".directory-browser-modal",
		".form-actions-sticky",
		".confirmation-overlay",
		".directory-browser-overlay",
	}
	for _, sel := range glassPanels {
		body := ruleBody(rules, sel)
		if body == "" {
			t.Errorf("reduced-transparency block missing rule for %s", sel)
			continue
		}
		if !strings.Contains(body, "backdrop-filter: none") {
			t.Errorf("%s must set backdrop-filter: none under reduced transparency, got: %s", sel, body)
		}
		if !strings.Contains(body, "background:") {
			t.Errorf("%s must declare a solid background under reduced transparency, got: %s", sel, body)
		}
	}
	t.Logf("reduced-transparency block de-glasses %d surfaces", len(glassPanels))
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
