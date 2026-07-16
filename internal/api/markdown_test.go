package api

import (
	"strings"
	"testing"
)

func TestRenderMarkdownToHTML_DisablesRawHTML(t *testing.T) {
	html := renderMarkdownToHTML("Hello <script>alert(1)</script> <b>world</b>")

	if strings.Contains(html, "<script>") || strings.Contains(html, "</script>") {
		t.Fatalf("raw script tag rendered: %s", html)
	}
	if strings.Contains(html, "<b>world</b>") {
		t.Fatalf("raw HTML bold tag rendered: %s", html)
	}
	if !strings.Contains(html, "alert(1)") || !strings.Contains(html, "world") {
		t.Fatalf("sanitized raw HTML text missing: %s", html)
	}
}

func TestRenderMarkdownToHTML_RendersThinkSectionsSafely(t *testing.T) {
	html := renderMarkdownToHTML("Before\n<think>hidden **reasoning** <script>alert(1)</script></think>\nAfter")

	for _, want := range []string{
		`<details class="think-details">`,
		`<summary>Thinking...</summary>`,
		`<strong>reasoning</strong>`,
		`alert(1)`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestRenderMarkdownToHTML_MultipleThinkBlocks(t *testing.T) {
	input := "Before\n<think>first reasoning</think>\nMiddle\n<think>second reasoning</think>\nAfter"
	html := renderMarkdownToHTML(input)

	for _, want := range []string{
		`<details class="think-details">`,
		`<summary>Thinking...</summary>`,
		"first reasoning",
		"second reasoning",
		"<p>Before</p>",
		"<p>Middle</p>",
		"<p>After</p>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}

	if strings.Count(html, "<details") != 2 {
		t.Fatalf("expected 2 <details>, got %d in %s", strings.Count(html, "<details"), html)
	}
}

func TestRenderMarkdownToHTML_EmptyThinkBlock(t *testing.T) {
	input := "Before<think></think>After"
	html := renderMarkdownToHTML(input)

	if !strings.Contains(html, `<details class="think-details">`) {
		t.Fatalf("missing think-details for empty think block in %s", html)
	}
	if !strings.Contains(html, `<summary>Thinking...</summary>`) {
		t.Fatalf("missing Thinking summary for empty think block in %s", html)
	}
}

func TestRenderMarkdownToHTML_ThinkInsideCodeBlock(t *testing.T) {
	input := "```\n<think>this is literal</think>\n```"
	html := renderMarkdownToHTML(input)

	if strings.Contains(html, `<details class="think-details">`) {
		t.Fatalf("think inside code block should not create details element: %s", html)
	}
	if strings.Contains(html, `<summary>Thinking...</summary>`) {
		t.Fatalf("think inside code block should not create Thinking summary: %s", html)
	}

	if !strings.Contains(html, "&lt;think&gt;this is literal&lt;/think&gt;") {
		t.Fatalf("literal think content missing from code block in %s", html)
	}
}

func TestRenderMarkdownToHTML_EnhancesCodeMathAndMermaid(t *testing.T) {
	input := strings.Join([]string{
		"Inline math $a+b$.",
		"",
		"$$c=d$$",
		"",
		"```go",
		"fmt.Println(\"hi\")",
		"```",
		"",
		"```mermaid",
		"graph TD; A-->B;",
		"```",
	}, "\n")

	html := renderMarkdownToHTML(input)

	for _, want := range []string{
		`<span class="math-inline" data-latex="a+b">$a+b$</span>`,
		`<div class="math-block" data-latex="c=d">$$c=d$$</div>`,
		`class="code-btn copy-btn"`,
		`class="code-btn wrap-btn"`,
		`class="language-go"`,
		`<pre class="mermaid">graph TD; A--&gt;B;`,
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}
