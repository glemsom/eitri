package tui

import (
	"strings"
	"testing"

	"github.com/glemsom/eitri/internal/config"
)

// Live-body tests lock the two deliberately-different streaming contracts:
//
//   - The streaming *answer* renders through the cheap ANSI word-wrap
//     (renderCheapLiveBody): it must never leak raw markdown, must still read
//     naturally (bold/italic/code/link emphasis), and must never produce an
//     overlarge line on a long unbroken token.
//   - The streaming *reasoning* body is a plain, style-free "background thought"
//     (renderLiveThoughtBody): the pane dims it, so the body must carry no SGR of
//     its own; the text — markdown syntax included — passes through verbatim and
//     parses only at commit.
//
// Committed, error, and stopped panes always keep the full glamour render.

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

func TestCheapLiveBody_WordWrapNoMidWordBreak(t *testing.T) {
	width := 40
	// Space-separated text must break at word boundaries, never mid-word.
	body := renderCheapLiveBody("The client connection with the server", width)
	for _, ln := range strings.Split(body, "\n") {
		// No line should contain a fragment like "cl\nient" or "th\ne".
		if strings.Contains(ln, "cl") && strings.Contains(ln, "ient") && len(ln) > width {
			// The old Hardwrap path broke mid-word; Wordwrap should not.
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

// TestLiveThoughtBody_PlainAndStyleFree locks the streaming reasoning contract:
// the body is the raw text (markdown syntax included, not parsed) with no SGR of
// any kind, so the reasoning pane's own dim/italic is never reset mid-line.
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

// TestLiveThoughtBody_WordWrapNoMidWordBreak proves space-separated text wraps
// at word boundaries, never mid-word.
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

// TestLiveThoughtBody_HardWrapLongToken proves the plain path still bounds line
// width on a long unbroken token (URL, code run) that would otherwise blow out.
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

	// The streaming reasoning pane is a plain dim thought: no emphasis SGR, the
	// raw text (markdown included) passes through.
	liveThought := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneStreamingThinking, th)
	if hasSGRBold(liveThought) {
		t.Errorf("streaming reasoning pane must not emit body emphasis SGR, got %q", ansiStrip(liveThought))
	}
	if !strings.Contains(ansiStrip(liveThought), "**bold**") {
		t.Errorf("streaming reasoning pane must pass raw text through, got %q", ansiStrip(liveThought))
	}

	// The streaming answer pane still uses cheap emphasis: raw markdown is stripped.
	cheapAnswer := renderPaneBodyFresh(text, 80, config.DefaultTheme, mdPaneStreaming, th)
	if strings.Contains(ansiStrip(cheapAnswer), "**") {
		t.Errorf("streaming answer pane leaked raw markdown: %q", ansiStrip(cheapAnswer))
	}
	if !hasSGRBold(cheapAnswer) {
		t.Errorf("streaming answer pane must keep cheap emphasis, got %q", ansiStrip(cheapAnswer))
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

// TestStreamingCommittedNoLeadingBlankLine locks the issue-63 contract: the
// committed pane body must not start with an empty bordered line (a `│` with
// no text after it), and the live body must match in that respect.
func TestStreamingCommittedNoLeadingBlankLine(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)
	width := 40

	// Glamour can emit a leading newline; trimBody must strip it so the pane
	// does not render an empty bordered line before the text.
	committedReason := renderPaneBodyFresh("reasoning text here", width, config.DefaultTheme, mdPaneThinking, th)
	for _, line := range strings.Split(committedReason, "\n") {
		stripped := ansiStrip(line)
		if stripped == "|" || stripped == "│" {
			t.Errorf("committed reasoning has an empty bordered line: %q", committedReason)
		}
	}

	liveReason := renderPaneBodyFresh("reasoning text here", width, config.DefaultTheme, mdPaneStreamingThinking, th)
	for _, line := range strings.Split(liveReason, "\n") {
		stripped := ansiStrip(line)
		if stripped == "|" || stripped == "│" {
			t.Errorf("live reasoning has an empty bordered line: %q", liveReason)
		}
	}

	committedAnswer := renderPaneBodyFresh("answer text here", width, config.DefaultTheme, mdPaneAgent, th)
	for _, line := range strings.Split(committedAnswer, "\n") {
		stripped := ansiStrip(line)
		if stripped == "|" || stripped == "│" {
			t.Errorf("committed answer has an empty bordered line: %q", committedAnswer)
		}
	}

	liveAnswer := renderPaneBodyFresh("answer text here", width, config.DefaultTheme, mdPaneStreaming, th)
	for _, line := range strings.Split(liveAnswer, "\n") {
		stripped := ansiStrip(line)
		if stripped == "|" || stripped == "│" {
			t.Errorf("live answer has an empty bordered line: %q", liveAnswer)
		}
	}
}

// TestCommittedParityCheapRendererNeverLeaksIntoCommitted locks scratch issue
// 03's byte-parity contract: a committed turn must render byte-identical to the
// glamour path whether or not the cheap live renderers exist. The live panes are
// registered for exactly the two streaming pane ids; every committed, error, and
// stopped pane id must render through glamour (renderPaneBodyFresh's default
// branch), so a committed answer with the same text must not differ from its own
// direct glamour render. The comparison is byte-for-byte on the pane-wrapped
// body, the same bytes the transcript paints.
func TestCommittedParityCheapRendererNeverLeaksIntoCommitted(t *testing.T) {
	t.Setenv("EITRI_ASCII_GLYPHS", "1")
	th := themeFor(config.DefaultTheme)

	sample := "A **bold** lead, a `code` span, and\n\n- a bullet list\n\nanother paragraph with *em* text."
	const width = 80

	md, err := RenderMarkdown(sample, width, config.DefaultTheme)
	if err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}

	// The committed pane must equal a direct glamour render exactly. The fresh
	// render path is the same function the live renderer dispatches through, so
	// this also pins that the cheap branches run ONLY for the streaming ids: every
	// other pane goes through RenderMarkdown and must come out identical to full
	// glamour, byte for byte.
	for _, paneID := range []liveMarkdownPaneID{mdPaneThinking, mdPaneAgent, mdPaneError, mdPaneStopped} {
		committed := renderPaneBodyFresh(sample, width, config.DefaultTheme, paneID, th)

		want := th.paneStyleFor(paneID).Render(trimBody(md))
		if committed != want {
			t.Errorf("pane %d: committed render diverged from glamour\n got %q\nwant %q", paneID, committed, want)
		}
	}

	// Both streaming panes must NOT route through glamour: their bodies carry
	// distinct bytes, so a regression that widens a cheap branch to a committed
	// pane would fail the parity loop above.
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
