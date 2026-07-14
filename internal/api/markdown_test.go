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
