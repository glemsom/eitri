package templates

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func renderComponentToHTML(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf strings.Builder
	err := c.Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("failed to render component: %v", err)
	}
	return buf.String()
}

func TestToolCard_DoneWithShortOutput_OpenByDefault(t *testing.T) {
	html := renderComponentToHTML(t, ToolCard("key1", "read", "", "short output", "done", "↑ 1.2s"))

	if !hasOpenAttr(html) {
		t.Errorf("short output tool card should have open attribute, got:\n%s", html)
	}
}

func TestToolCard_DoneWithLongOutput_CollapsedByDefault(t *testing.T) {
	lines := make([]string, 35)
	for i := 0; i < 35; i++ {
		lines[i] = "line"
	}
	longOutput := strings.Join(lines, "\n")

	html := renderComponentToHTML(t, ToolCard("key1", "read", "", longOutput, "done", "↑ 2.1s"))

	if hasOpenAttr(html) {
		t.Errorf("long output tool card should NOT have open attribute, got:\n%s", html)
	}
}

func TestToolCard_DoneWithEmptyOutput_OpenByDefault(t *testing.T) {
	html := renderComponentToHTML(t, ToolCard("key1", "read", "", "", "done", "↑ 0.1s"))

	if !hasOpenAttr(html) {
		t.Errorf("empty output tool card should have open attribute, got:\n%s", html)
	}
}

func TestToolCard_DoneWithExactly29Lines_OpenByDefault(t *testing.T) {
	lines := make([]string, 29)
	for i := 0; i < 29; i++ {
		lines[i] = "line"
	}
	output := strings.Join(lines, "\n")

	html := renderComponentToHTML(t, ToolCard("key1", "read", "", output, "done", "↑ 1.0s"))

	if !hasOpenAttr(html) {
		t.Errorf("29-line output should have open attribute (under 30 threshold), got:\n%s", html)
	}
}

func TestToolCard_DoneWithExactly30Lines_Collapsed(t *testing.T) {
	lines := make([]string, 30)
	for i := 0; i < 30; i++ {
		lines[i] = "line"
	}
	output := strings.Join(lines, "\n")

	html := renderComponentToHTML(t, ToolCard("key1", "read", "", output, "done", "↑ 1.0s"))

	if hasOpenAttr(html) {
		t.Errorf("30-line output should NOT have open attribute, got:\n%s", html)
	}
}

func TestToolCard_Running_NoDetailsElement(t *testing.T) {
	html := renderComponentToHTML(t, ToolCard("key1", "read", "arg1", "some output", "running", ""))

	if strings.Contains(html, "<details") {
		t.Errorf("running tool card should use <div> not <details>, got:\n%s", html)
	}
}


// hasOpenAttr checks if a <details> element rendered with the open attribute.
func hasOpenAttr(html string) bool {
	idx := strings.Index(html, "<details")
	if idx < 0 {
		return false
	}
	close := strings.Index(html[idx:], ">")
	if close < 0 {
		return false
	}
	tag := html[idx : idx+close]
	return strings.Contains(tag, " open")
}
