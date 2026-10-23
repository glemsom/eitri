package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

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
}

func TestCheapLiveBody_WordWrapNoMidWordBreak(t *testing.T) {
	width := 40
	body := renderCheapLiveBody("The client connection with the server", width)
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, "cl") && strings.Contains(ln, "ient") && len(ln) > width {
			t.Fatalf("word-wrap broke mid-word: %q", body)
		}
	}
	if !strings.Contains(body, "client") {
		t.Fatalf("word-wrap dropped content: %q", body)
	}
}

func TestCheapLiveBody_HardWrapLongToken(t *testing.T) {
	long := strings.Repeat("a", 500)
	width := 40
	body := renderCheapLiveBody("word "+long+" tail", width)
	for _, ln := range strings.Split(body, "\n") {
		if w := len(ln); w > width {
			t.Fatalf("cheap wrap produced an overlarge line %d > %d: %q", w, width, body)
		}
	}
	if !strings.Contains(body, "word") || !strings.Contains(body, "tail") {
		t.Fatalf("wrap dropped content: %q", body)
	}
}

func TestLiveThoughtBody_PlainAndStyleFree(t *testing.T) {
	in := "pre **bold** and `code` and ## heading"
	body := renderLiveThoughtBody(in, 200)
	if strings.Contains(body, "\x1b") {
		t.Errorf("live reasoning body must carry no SGR at all, got %q", body)
	}
	if !strings.Contains(body, "**bold**") || !strings.Contains(body, "## heading") {
		t.Errorf("live reasoning body must pass the raw text through verbatim, got %q", body)
	}
}

func TestLiveThoughtBody_WordWrapNoMidWordBreak(t *testing.T) {
	width := 40
	body := renderLiveThoughtBody("The client connection with the server", width)
	for _, ln := range strings.Split(body, "\n") {
		if strings.Contains(ln, "cl") && strings.Contains(ln, "ient") && len(ln) > width {
			t.Fatalf("word-wrap broke mid-word: %q", body)
		}
	}
	if !strings.Contains(body, "client") {
		t.Fatalf("word-wrap dropped content: %q", body)
	}
}

func TestLiveThoughtBody_HardWrapLongToken(t *testing.T) {
	long := strings.Repeat("a", 500)
	width := 40
	body := renderLiveThoughtBody("word "+long+" tail", width)
	for _, ln := range strings.Split(body, "\n") {
		if w := len(ln); w > width {
			t.Fatalf("live thought wrap produced an overlarge line %d > %d: %q", w, width, body)
		}
	}
	if !strings.Contains(body, "word") || !strings.Contains(body, "tail") {
		t.Fatalf("wrap dropped content: %q", body)
	}
}

func TestRendererSwitchesToCheapOnlyForStreamingPanes(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	text := "**bold** bullet - item"

	liveThought := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneStreamingThinking, th)
	if hasSGRBold(liveThought) {
		t.Errorf("streaming reasoning pane must not emit body emphasis SGR, got %q", ansiStrip(liveThought))
	}
	if !strings.Contains(ansiStrip(liveThought), "**bold**") {
		t.Errorf("streaming reasoning pane must pass raw text through, got %q", ansiStrip(liveThought))
	}

	cheapAnswer := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneStreaming, th)
	if strings.Contains(ansiStrip(cheapAnswer), "**") {
		t.Errorf("streaming answer pane leaked raw markdown: %q", ansiStrip(cheapAnswer))
	}
	if !hasSGRBold(cheapAnswer) {
		t.Errorf("streaming answer pane must keep cheap emphasis, got %q", ansiStrip(cheapAnswer))
	}

	committed := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneThinking, th)
	if !hasSGRBold(committed) {
		t.Errorf("committed reasoning pane must keep glamour emphasis, got: %q", ansiStrip(committed))
	}
	if !hasBullet(ansiStrip(committed)) {
		t.Errorf("committed reasoning pane must keep glamour list bullets, got: %q", ansiStrip(committed))
	}
}

func TestStreamingCommittedNoLeadingBlankLine(t *testing.T) {
	th := themeFor(config.DefaultTheme)
	width := 40

	cases := []struct {
		name   string
		paneID liveMarkdownPaneID
		text   string
	}{
		{"committed reasoning", mdPaneThinking, "reasoning text here"},
		{"live reasoning", mdPaneStreamingThinking, "reasoning text here"},
		{"committed answer", mdPaneAgent, "answer text here"},
		{"live answer", mdPaneStreaming, "answer text here"},
	}

	for _, c := range cases {
		body := renderPaneBodyFresh(c.text, width, config.DefaultTheme, c.paneID, th)
		for _, line := range strings.Split(body, "\n") {
			stripped := ansiStrip(line)
			if stripped == "|" || stripped == "│" {
				t.Errorf("%s has an empty bordered line: %q", c.name, body)
			}
		}
	}
}

func TestCommittedParityCheapRendererNeverLeaksIntoCommitted(t *testing.T) {
	th := themeFor(config.DefaultTheme)

	sample := "A **bold** lead, a `code` span, and\n\n- a bullet list\n\nanother paragraph with *em* text."
	const width = 80

	md, err := RenderMarkdown(sample, width, config.DefaultTheme)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	for _, paneID := range []liveMarkdownPaneID{mdPaneThinking, mdPaneAgent, mdPaneError, mdPaneStopped} {
		committed := renderPaneBodyFresh(sample, width, config.DefaultTheme, paneID, th)

		want := th.paneStyleFor(paneID).Render(trimBody(md))
		if committed != want {
			t.Errorf("pane %d: committed render diverged from glamour\n got %q\nwant %q", paneID, committed, want)
		}
	}

	liveThought := renderPaneBodyFresh(sample, width, config.DefaultTheme, mdPaneStreamingThinking, th)
	glam := th.paneStyleFor(mdPaneStreamingThinking).Render(trimBody(md))
	if liveThought == glam {
		t.Errorf("streaming reasoning pane rendered through glamour; plain live body lost")
	}
	if hasSGRBold(liveThought) {
		t.Errorf("streaming reasoning pane must not emit body emphasis SGR, got %q", ansiStrip(liveThought))
	}

	liveAnswer := renderPaneBodyFresh(sample, width, config.DefaultTheme, mdPaneStreaming, th)
	if liveAnswer == th.paneStyleFor(mdPaneStreaming).Render(trimBody(md)) {
		t.Errorf("streaming answer pane rendered through glamour; cheap live renderer lost")
	}
	if !hasSGRBold(liveAnswer) {
		t.Errorf("cheap streaming answer pane must still render bold as SGR, got %q", ansiStrip(liveAnswer))
	}
}
