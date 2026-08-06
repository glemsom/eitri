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

func TestRenderMarkdownToHTML_TaskListCheckboxLabels(t *testing.T) {
	html := renderMarkdownToHTML("- [ ] item one\n- [x] item two")

	// Every task-list checkbox must be wrapped in a <label> with its text so
	// screen readers announce "item one, checkbox, not checked" instead of an
	// unlabelled checkbox (issue #1071).
	if !strings.Contains(html, "<label><input") {
		t.Fatalf("task checkbox missing <label> wrapper: %s", html)
	}
	if !strings.Contains(html, `type="checkbox"`) {
		t.Fatalf("task checkbox missing in output: %s", html)
	}
	if !strings.Contains(html, "item one") || !strings.Contains(html, "item two") {
		t.Fatalf("task item text missing from output: %s", html)
	}
	if strings.Contains(html, `<input`+" "+`disabled=""`+` type="checkbox"/> item one</li>`) {
		t.Fatalf("checkbox must be label-associated, not a bare sibling of the text: %s", html)
	}
	// The label text must sit inside the label (accessible name), not outside it.
	if !strings.Contains(html, "item one</label>") || !strings.Contains(html, "item two</label>") {
		t.Fatalf("task item text must live inside the label element: %s", html)
	}
}

func TestRenderMarkdownToHTML_TaskListWithFormatting(t *testing.T) {
	// Formatting inside task items (inline code, bold) must survive the label wrap.
	html := renderMarkdownToHTML("- [ ] run `go test`\n- [x] **done**")

	for _, want := range []string{
		`<label><input`,
		"<code>go test</code>",
		"<strong>done</strong>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestRenderMarkdownToHTML_NonTaskListUntouched(t *testing.T) {
	// Regular (non-task) lists must not gain labels.
	html := renderMarkdownToHTML("- plain item\n- another")

	if strings.Contains(html, "<label") {
		t.Fatalf("plain list items must not be wrapped in labels: %s", html)
	}
	if !strings.Contains(html, "<li>plain item</li>") {
		t.Fatalf("plain list item missing: %s", html)
	}
}

func TestStripMermaidCodeBlocks(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		output string
	}{
		{
			name:   "no mermaid block",
			input:  "Hello world\n\nSome text",
			output: "Hello world\n\nSome text",
		},
		{
			name:   "mermaid block stripped",
			input:  "Before\n\n```mermaid\ngraph TD; A-->B;\n```\n\nAfter",
			output: "Before\n\nAfter",
		},
		{
			name:   "mermaid block at start",
			input:  "```mermaid\ngraph TD; A-->B;\n```\n\nAfter",
			output: "After",
		},
		{
			name:   "only mermaid block",
			input:  "```mermaid\ngraph TD; A-->B;\n```",
			output: "",
		},
		{
			name:   "non-mermaid code block preserved",
			input:  "Before\n\n```go\nfmt.Println(\"hi\")\n```\n\n```mermaid\ngraph TD; A-->B;\n```\n\nAfter",
			output: "Before\n\n```go\nfmt.Println(\"hi\")\n```\n\nAfter",
		},
		{
			name:   "multiple mermaid blocks stripped",
			input:  "A\n\n```mermaid\ngraph1\n```\n\nB\n\n```mermaid\ngraph2\n```\n\nC",
			output: "A\n\nB\n\nC",
		},
		{
			name:   "mermaid with trailing spaces on delimiter",
			input:  "```mermaid  \ngraph TD; A-->B;\n```\nAfter",
			output: "After",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripMermaidCodeBlocks(tc.input)
			if got != tc.output {
				t.Errorf("stripMermaidCodeBlocks(%q)\n  got:  %q\n  want: %q", tc.input, got, tc.output)
			}
		})
	}
}

// TestRenderMarkdownToHTML_ExternalLinkTarget verifies that http/https/mailto
// links in the committed (server-rendered) message open in a new tab with
// rel="noopener", matching the client-side streaming renderer (issue #1121).
// This keeps the streamed and committed views identical so tests that assert on
// the finished render pass deterministically.
func TestRenderMarkdownToHTML_ExternalLinkTarget(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantEnds bool // output should contain an <a> with target+rel
	}{
		{name: "http link", input: "Check [example](http://example.com) here", wantEnds: true},
		{name: "https link", input: "Check [example](https://example.com) details", wantEnds: true},
		{name: "mailto link", input: "Email [me](mailto:user@example.com) now", wantEnds: true},
		{name: "javascript link stripped", input: "Click [here](javascript:alert(1)) for more", wantEnds: false},
		{name: "data link stripped", input: "Check [bad](data:text/html,<b>XSS</b>) here", wantEnds: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderMarkdownToHTML(tc.input)
			if tc.wantEnds {
				if !strings.Contains(got, `target="_blank"`) || !strings.Contains(got, `rel="noopener"`) {
					t.Errorf("expected link to carry target=\"_blank\" rel=\"noopener\", got: %s", got)
				}
			} else if strings.Contains(got, "<a") {
				t.Errorf("expected dangerous link to be stripped to plain text, got: %s", got)
			}
		})
	}

	// Concretely verify the produced anchor for an http link.
	got := renderMarkdownToHTML("Check [example](http://example.com) here")
	if !strings.Contains(got, `<a href="http://example.com" target="_blank" rel="noopener">`) {
		t.Errorf("http link should render with target+rel, got: %s", got)
	}
}
