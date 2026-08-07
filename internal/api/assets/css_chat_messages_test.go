package assets

// CSS tests for the chat message redesign (issue #1179). These assert the
// spec's acceptance criteria at the embedded-asset seam: assistant bubbles are
// full-width flat rows with a thin accent bar (no card shadow/background),
// user bubbles are right-aligned accent-tinted bubbles whose avatar anchors the
// right edge, and the message list gains vertical breathing room.

import (
	"io"
	"strings"
	"testing"
)

func readEmbeddedCSS(t *testing.T) string {
	t.Helper()
	f, err := Files.Open("eitri.css")
	if err != nil {
		t.Fatalf("open eitri.css: %v", err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	return string(data)
}

// TestEmbeddedCSSAssistantMessageFlat asserts assistant messages drop the
// card shadow and background, becoming a full-width row with only a thin
// (2px) left accent border. (issue #1179)
func TestEmbeddedCSSAssistantMessageFlat(t *testing.T) {
	css := readEmbeddedCSS(t)
	rules := parseCSSRules(css)

	body := ruleBodyExact(rules, ".message-assistant")
	if body == "" {
		t.Fatal("missing .message-assistant rule")
	}
	if strings.Contains(body, "box-shadow") && !strings.Contains(body, "box-shadow: none") {
		t.Errorf(".message-assistant retains a box-shadow; want none for a flat cardless row:\n%s", body)
	}
	if strings.Contains(body, "background") && !strings.Contains(body, "background: none") {
		t.Errorf(".message-assistant retains a background; want none (sits on page background):\n%s", body)
	}
	if !strings.Contains(body, "border-left: 2px solid var(--accent)") {
		t.Errorf(".message-assistant must use a thin 2px left accent border, got:\n%s", body)
	}
	if !strings.Contains(body, "align-self: stretch") {
		t.Errorf(".message-assistant must be full-width (align-self: stretch):\n%s", body)
	}
}

// TestEmbeddedCSSUserMessageBubble asserts user messages are right-aligned
// bubbles tinted at ~12% accent opacity. (issue #1179)
func TestEmbeddedCSSUserMessageBubble(t *testing.T) {
	css := readEmbeddedCSS(t)
	rules := parseCSSRules(css)

	body := ruleBodyExact(rules, ".message-user")
	if body == "" {
		t.Fatal("missing .message-user rule")
	}
	if !strings.Contains(body, "align-self: flex-end") {
		t.Errorf(".message-user must right-align (align-self: flex-end):\n%s", body)
	}
	if !strings.Contains(body, "12%") || !strings.Contains(body, "var(--accent)") {
		t.Errorf(".message-user background must tint with accent at ~12%% opacity:\n%s", body)
	}
}

// TestEmbeddedCSSMessageGap asserts the message list gap is 1rem for visual
// breathing room. (issue #1179)
func TestEmbeddedCSSMessageGap(t *testing.T) {
	css := readEmbeddedCSS(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".messages")
	if body == "" {
		t.Fatal("missing .messages rule")
	}
	if !strings.Contains(body, "gap: 1rem") {
		t.Errorf(".messages gap must be 1rem for breathing room:\n%s", body)
	}
}

// TestEmbeddedCSSAssistantTextWrapBalance asserts assistant content paragraphs
// balance text to avoid orphaned words. (issue #1179)
func TestEmbeddedCSSAssistantTextWrapBalance(t *testing.T) {
	css := readEmbeddedCSS(t)
	rules := parseCSSRules(css)
	body := ruleBodyExact(rules, ".message-assistant .message-content p")
	if body == "" {
		t.Fatal("missing .message-assistant .message-content p rule")
	}
	if !strings.Contains(body, "text-wrap: balance") {
		t.Errorf("assistant paragraphs must use text-wrap: balance:\n%s", body)
	}
}
