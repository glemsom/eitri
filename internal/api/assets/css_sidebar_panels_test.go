package assets

// CSS tests for the sidebar panel structure and session item depth redesign
// (issue #1181). These assert the spec's acceptance criteria at the embedded
// asset seam: four distinct section zones (sessions, tools, thinking, context),
// sentence-case SemiBold section labels, a left-accent hover border on session
// items, a larger readable thinking panel, and a thicker rounded context bar.

import (
	"strings"
	"testing"
)

// TestEmbeddedCSSSidebarPanelsFourDistinct asserts all four sidebar sections —
// sessions, tools, thinking AND context — carry distinct background tints so
// the sidebar reads as four zones instead of one undifferentiated column.
// (issue #1181)
func TestEmbeddedCSSSidebarPanelsFourDistinct(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	seen := map[string]string{}
	for _, r := range parseCSSRules(css) {
		name := ""
		switch {
		case strings.Contains(r.selector, "#session-panel"):
			name = "sessions"
		case strings.Contains(r.selector, "#tool-activity"):
			name = "tools"
		case strings.Contains(r.selector, "#thinking-panel"):
			name = "thinking"
		case strings.Contains(r.selector, "#context-panel"):
			name = "context"
		}
		if name == "" {
			continue
		}
		if v, ok := cssPropertyValue(r.body, "background"); ok {
			seen[name] = v
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected a tint for each of sessions/tools/thinking/context panels, got %d backgrounds: %v",
			len(seen), seen)
	}
	vals := make([]string, 0, 4)
	for _, v := range seen {
		vals = append(vals, v)
	}
	for i := 0; i < len(vals); i++ {
		for j := i + 1; j < len(vals); j++ {
			if vals[i] == vals[j] {
				t.Errorf("sidebar panels not visually differentiated: %s reused for two panels", vals[i])
			}
		}
	}
}

// TestEmbeddedCSSSidebarLabelSentenceCase asserts section labels (SESSIONS,
// TOOLS, CONTEXT, THINKING) switch from uppercase to sentence case, using the
// body font (--font-ui) at SemiBold weight rather than uppercase monospace.
// (issue #1181)
func TestEmbeddedCSSSidebarLabelSentenceCase(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".sidebar-header")
	if body == "" {
		t.Fatal("missing .sidebar-header rule")
	}
	if strings.Contains(body, "text-transform: uppercase") {
		t.Errorf(".sidebar-header still uses text-transform: uppercase; must be sentence case:\n%s", body)
	}
	if !strings.Contains(body, "font-weight: 600") {
		t.Errorf(".sidebar-header must use SemiBold (600) weight:\n%s", body)
	}
	// Body font via the UI font token, not a monospace dump.
	if sv, ok := cssPropertyValue(body, "font-family"); ok && strings.Contains(sv, "--font-code") {
		t.Errorf(".sidebar-header uses --font-code; must be the body font (--font-ui):\n%s", body)
	}
	// Explicit body-font declaration so the panel headers read as labels.
	if !strings.Contains(body, "var(--font-ui)") {
		t.Errorf(".sidebar-header must declare the body font (var(--font-ui)) at SemiBold:\n%s", body)
	}
}

// TestEmbeddedCSSSessionItemHoverAccent asserts session items gain a
// left-border accent on hover (not just a background change). (issue #1181)
func TestEmbeddedCSSSessionItemHoverAccent(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".session-item:hover")
	if body == "" {
		t.Fatal("missing .session-item:hover rule")
	}
	if !strings.Contains(body, "border-left") || !strings.Contains(body, "var(--accent)") {
		t.Errorf(".session-item:hover must add a left accent border, got:\n%s", body)
	}
}

// TestEmbeddedCSSThinkingPanelReadable asserts the thinking panel uses a
// bumped 0.82rem font, relaxed 1.5 line-height, and a faint code-block
// background tint so it is comfortable to read. (issue #1181)
func TestEmbeddedCSSThinkingPanelReadable(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".thinking-content")
	if body == "" {
		t.Fatal("missing .thinking-content rule")
	}
	if !strings.Contains(body, "font-size: 0.82rem") {
		t.Errorf(".thinking-content font must be bumped to 0.82rem, got:\n%s", body)
	}
	if !strings.Contains(body, "line-height: 1.5") {
		t.Errorf(".thinking-content must use a relaxed line-height 1.5:\n%s", body)
	}
	if !strings.Contains(body, "background: var(--turn-context-bg)") {
		t.Errorf(".thinking-content must keep the faint code-block background tint:\n%s", body)
	}
}

// TestEmbeddedCSSContextBarThickRounded asserts the context panel progress bar
// is thickened to 10-12px with fully rounded ends. (issue #1181)
func TestEmbeddedCSSContextBarThickRounded(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".context-bar")
	if body == "" {
		t.Fatal("missing .context-bar rule")
	}
	// Height must sit in the 10-12px band.
	switch {
	case strings.Contains(body, "height: 10px"), strings.Contains(body, "height: 11px"), strings.Contains(body, "height: 12px"):
	default:
		t.Errorf(".context-bar height must be 10-12px (thicker), got:\n%s", body)
	}
	// Fully rounded ends (pill), not a small 4px corner.
	if !strings.Contains(body, "border-radius: 999px") &&
		!strings.Contains(body, "border-radius: 1rem") {
		t.Errorf(".context-bar must use fully rounded (pill) ends, got:\n%s", body)
	}
}

// TestEmbeddedCSSContextBarFillRounded asserts the context fill bar shares the
// fully rounded ends so the thickened bar renders as a pill. (issue #1181)
func TestEmbeddedCSSContextBarFillRounded(t *testing.T) {
	css := readEmbeddedCSSFile(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".context-bar-fill")
	if body == "" {
		t.Fatal("missing .context-bar-fill rule")
	}
	if !strings.Contains(body, "border-radius: 999px") &&
		!strings.Contains(body, "border-radius: 1rem") {
		t.Errorf(".context-bar-fill must match the pill radius of the bar, got:\n%s", body)
	}
}
