package tools

import (
	"strings"
	"testing"
)

// TestHTMLToMarkdownRendersBlocks locks the representative block coverage the
// converter is expected to render: headings, paragraphs, bold/italic, links,
// code fences, and lists — the shapes web content needs.
func TestHTMLToMarkdownRendersBlocks(t *testing.T) {
	html := `<html><body>` +
		`<h1>Heading</h1>` +
		`<p>Text with <strong>bold</strong> and <em>it</em> and <a href="https://x.io">link</a>.</p>` +
		`<pre><code>fmt.Println("hi")</code></pre>` +
		`<ul><li>alpha</li><li>beta</li></ul>` +
		`</body></html>`
	out, err := htmlToMarkdown(strings.NewReader(html))
	if err != nil {
		t.Fatalf("htmlToMarkdown error = %v, want nil", err)
	}
	for _, want := range []string{
		"# Heading",
		"**bold**",
		"*it*",
		"[link](https://x.io)",
		`fmt.Println("hi")`,
		"- alpha",
		"- beta",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown %q missing %q", out, want)
		}
	}
}

// TestHTMLToMarkdownDropsChrome verifies script/style/nav bodies never reach the
// output — untrusted web chrome stays out.
func TestHTMLToMarkdownDropsChrome(t *testing.T) {
	html := `<html><head><style>.x{}</style></head><body><nav><a>hidden</a></nav><script>evil()</script><p>keep</p></body></html>`
	out, err := htmlToMarkdown(strings.NewReader(html))
	if err != nil {
		t.Fatalf("htmlToMarkdown error = %v, want nil", err)
	}
	if !strings.Contains(out, "keep") {
		t.Fatalf("markdown %q missing body content 'keep'", out)
	}
	for _, banned := range []string{"evil()", ".x{", "</script>", "</style>"} {
		if strings.Contains(out, banned) {
			t.Fatalf("markdown %q leaked chrome fragment %q", out, banned)
		}
	}
}
