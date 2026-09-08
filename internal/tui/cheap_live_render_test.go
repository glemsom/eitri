package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// cheap body tests lock scratch issue 02's contract: a streaming reasoning or
// answer block renders its body with the cheap ANSI word-wrap, and that path
// must (a) never leak raw markdown, (b) still read naturally (bold/italic/code/
// link emphasis), (c) never produce an overlarge line on a long unbroken token,
// and (d) never be reached for committed/error/stopped panes, which keep the
// full glamour render.

func TestCheapLiveBody_InlineEmphasisAndStrip(t *testing.T) {
	body := simplifyMarkdownEmphasis("pre **bold** and *em* and `code` [label](https://x) and ![img](y) post")
	if !hasSGRBold(body) {
		t.Errorf("streaming body must render **bold** as real SGR, got %q", body)
	}
	if !strings.Contains(body, "\x1b[3m") {
		t.Errorf("streaming body must render *em* as real italic SGR, got %q", body)
	}
	for _, leak := range []string{"**", "*em*", "[label]", "](https://x)", "![img]", "(y)"} {
		if strings.Contains(body, leak) {
			t.Errorf("raw markdown %q leaked into streaming body: %q", leak, body)
		}
	}
}

func TestCheapLiveBody_FencePreserved(t *testing.T) {
	in := "intro\n```\ncode **not bold**\n```\noutro"
	body := simplifyMarkdownEmphasis(in)
	if !strings.Contains(body, "code **not bold**") {
		t.Errorf("fenced code must render verbatim, got %q", body)
	}
	if strings.Contains(body, "```") {
		t.Errorf("fence markers must be dropped, got %q", body)
	}
	// Inline code outside a fence is emphasized, not fenced.
}

func TestCheapLiveBody_HardWrapLongToken(t *testing.T) {
	long := strings.Repeat("a", 500)
	width := 40
	body := renderCheapLiveBody("word "+long+" tail", width)
	for _, ln := range strings.Split(body, "\n") {
		if w := len(ln); w > width {
			t.Fatalf("cheap hard-wrap produced an overlarge line %d > %d: %q", w, width, body)
		}
	}
	if !strings.Contains(body, "word") || !strings.Contains(body, "tail") {
		t.Fatalf("hard-wrap dropped content: %q", body)
	}
}

func TestRendererSwitchesToCheapOnlyForStreamingPanes(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	text := "**bold** bullet - item"

	// The streaming reasoning pane must go through the cheap path.
	cheap := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneStreamingThinking, th)
	plain := ansiStrip(cheap)
	if strings.Contains(plain, "**") {
		t.Errorf("streaming pane leaked raw markdown via cheap path: %q", plain)
	}
	// The streaming answer pane likewise uses cheap emphasis.
	cheapAnswer := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneStreaming, th)
	if strings.Contains(ansiStrip(cheapAnswer), "**") {
		t.Errorf("streaming answer pane leaked raw markdown: %q", ansiStrip(cheapAnswer))
	}

	// Committed reasoning pane must still pass through glamour: markdown emphasis
	// (bold) and list bullets survive.
	committed := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneThinking, th)
	if !hasSGRBold(committed) {
		t.Errorf("committed reasoning pane must keep glamour emphasis, got: %q", ansiStrip(committed))
	}
	if !hasBullet(ansiStrip(committed)) {
		t.Errorf("committed reasoning pane must keep glamour list bullets, got: %q", ansiStrip(committed))
	}
}
