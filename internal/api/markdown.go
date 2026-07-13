package api

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// renderMarkdownToHTML converts Markdown text to HTML using goldmark.
// Supports tables, fenced code blocks, strikethrough, autolinks, task lists.
// Renders <think>...</think> tags as collapsible <details> elements.
func renderMarkdownToHTML(md string) string {
	if md == "" {
		return ""
	}

	// Pre-process: convert <think>...</think> to collapsible details
	md = processThinkTags(md)

	mdParser := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,           // tables, fenced code, autolinks, strikethrough
			extension.TaskList,      // task list items
			extension.Typographer,   // smart quotes, dashes
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(), // heading anchors
		),
		goldmark.WithRendererOptions(
			html.WithUnsafe(), // allow raw HTML (for <details>, <think>)
		),
	)

	var buf bytes.Buffer
	if err := mdParser.Convert([]byte(md), &buf); err != nil {
		return "<p>Error rendering markdown</p>"
	}

	return buf.String()
}

// processThinkTags converts <think>...</think> to <details><summary>Thinking</summary>...</details>.
func processThinkTags(md string) string {
	var result strings.Builder
	remaining := md

	for {
		start := strings.Index(remaining, "<think>")
		if start == -1 {
			result.WriteString(remaining)
			break
		}

		result.WriteString(remaining[:start])
		afterStart := remaining[start+7:]

		end := strings.Index(afterStart, "</think>")
		if end == -1 {
			// No closing tag, leave as-is
			result.WriteString(remaining[start:])
			break
		}

		thinkContent := afterStart[:end]
		result.WriteString("<details class=\"think-details\">\n<summary>Thinking...</summary>\n")
		result.WriteString(thinkContent)
		result.WriteString("\n</details>\n")

		remaining = afterStart[end+8:]
	}

	return result.String()
}
