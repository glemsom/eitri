package api

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

// renderMarkdownToHTML converts Markdown text to HTML using goldmark.
// Supports tables, fenced code blocks, strikethrough, autolinks, task lists,
// footnotes, safe <think> collapsibles, math wrappers, and enhanced code blocks.
func renderMarkdownToHTML(md string) string {
	if md == "" {
		return ""
	}

	html := renderMarkdownWithThinkSections(md)
	if html == "" {
		return ""
	}

	return enhanceRenderedHTML(html)
}

func renderMarkdownWithThinkSections(md string) string {
	var result strings.Builder
	remaining := md

	for {
		// Find the next fenced code block boundary first
		fenceStart := findFenceStart(remaining)
		thinkStart := strings.Index(remaining, "<think>")

		// No think tag left — render remainder
		if thinkStart == -1 {
			result.WriteString(renderMarkdownFragment(remaining))
			break
		}

		// If a code fence precedes the think tag, render everything up to
		// the end of the code fence first, then continue scanning
		if fenceStart != -1 && fenceStart < thinkStart {
			fenceEnd := findFenceEnd(remaining, fenceStart)
			if fenceEnd == -1 {
				result.WriteString(renderMarkdownFragment(remaining))
				break
			}
			result.WriteString(renderMarkdownFragment(remaining[:fenceEnd]))
			remaining = remaining[fenceEnd:]
			continue
		}

		// No code fence before think tag — handle the think block
		result.WriteString(renderMarkdownFragment(remaining[:thinkStart]))
		afterStart := remaining[thinkStart+len("<think>"):]
		end := strings.Index(afterStart, "</think>")
		if end == -1 {
			result.WriteString(renderMarkdownFragment(remaining[thinkStart:]))
			break
		}

		thinkContent := strings.TrimSpace(afterStart[:end])
		result.WriteString(`<details class="think-details"><summary>Thinking...</summary>`)
		if thinkContent != "" {
			result.WriteString(renderMarkdownWithThinkSections(thinkContent))
		}
		result.WriteString(`</details>`)

		remaining = afterStart[end+len("</think>"):]
	}

	return result.String()
}

// findFenceStart finds the start position of the next fenced code block
// (``` or ~~~) that is not inside an inline code span.
func findFenceStart(s string) int {
	for i := 0; i < len(s); i++ {
		// Skip inline code spans
		if s[i] == '`' && i+1 < len(s) && s[i+1] != '`' && s[i+1] != '\n' {
			end := findInlineBacktickEnd(s, i)
			if end != -1 {
				i = end + 1
				continue
			}
		}
		// Check for triple backtick
		if i+2 < len(s) && s[i] == '`' && s[i+1] == '`' && s[i+2] == '`' {
			return i
		}
		// Check for triple tilde
		if i+2 < len(s) && s[i] == '~' && s[i+1] == '~' && s[i+2] == '~' {
			return i
		}
	}
	return -1
}

// findInlineBacktickEnd finds the matching closing backtick for an inline
// code span starting at pos. Assumes s[pos] == '`'.
func findInlineBacktickEnd(s string, pos int) int {
	for j := pos + 1; j < len(s); j++ {
		if s[j] == '`' {
			return j
		}
	}
	return -1
}

// findFenceEnd finds the end position (exclusive) of a fenced code block
// that starts at fenceStart. It searches for the matching closing fence
// on its own line. Returns -1 if not found.
func findFenceEnd(s string, fenceStart int) int {
	// Determine the fence character and its required length
	// Find the end of the opening fence line
	fenceChar := s[fenceStart]
	newlinePos := strings.Index(s[fenceStart:], "\n")
	if newlinePos == -1 {
		return -1
	}
	searchStart := fenceStart + newlinePos + 1

	// Search for closing fence (matching or longer fence on its own line)
	for j := searchStart; j < len(s); j++ {
		if s[j] == '\n' {
			continue
		}
		if j+2 < len(s) && s[j] == fenceChar && s[j+1] == fenceChar && s[j+2] == fenceChar {
			// Found a potential closing fence — find end of its line
			closeEnd := strings.Index(s[j:], "\n")
			if closeEnd == -1 {
				return len(s)
			}
			return j + closeEnd + 1
		}
		// Skip to next line
		nextNewline := strings.Index(s[j:], "\n")
		if nextNewline == -1 {
			return -1
		}
		j += nextNewline
	}
	return -1
}

func renderMarkdownFragment(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	mdParser := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.TaskList,
			extension.Typographer,
			extension.Footnote,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)

	var buf bytes.Buffer
	if err := mdParser.Convert([]byte(md), &buf); err != nil {
		return "<p>Error rendering markdown</p>"
	}

	return buf.String()
}
