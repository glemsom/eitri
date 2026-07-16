package api

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	htmlnode "golang.org/x/net/html"

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

func enhanceRenderedHTML(fragment string) string {
	doc, err := htmlnode.Parse(strings.NewReader("<!doctype html><html><body><div id=\"eitri-markdown-root\">" + fragment + "</div></body></html>"))
	if err != nil {
		return fragment
	}

	root := findElementByID(doc, "eitri-markdown-root")
	if root == nil {
		return fragment
	}

	transformTree(root)

	var out strings.Builder
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := htmlnode.Render(&out, child); err != nil {
			return fragment
		}
	}
	return out.String()
}

func transformTree(node *htmlnode.Node) {
	if node == nil || isLiteralContainer(node) {
		return
	}

	for child := node.FirstChild; child != nil; {
		next := child.NextSibling

		if child.Type == htmlnode.ElementNode {
			switch child.Data {
			case "p":
				if transformBlockMath(child) {
					child = next
					continue
				}
			case "pre":
				transformPreBlock(child)
				child = next
				continue
			}
		}

		if child.Type == htmlnode.TextNode && !isLiteralContainer(node) {
			transformInlineMath(node, child)
		} else {
			transformTree(child)
		}

		child = next
	}
}

func transformBlockMath(p *htmlnode.Node) bool {
	if p == nil || p.Type != htmlnode.ElementNode || p.Data != "p" {
		return false
	}
	if p.FirstChild == nil || p.FirstChild != p.LastChild || p.FirstChild.Type != htmlnode.TextNode {
		return false
	}

	text := strings.TrimSpace(p.FirstChild.Data)
	latex, ok := unwrapBlockMath(text)
	if !ok {
		return false
	}

	block := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "div"}
	setAttr(block, "class", "math-block")
	setAttr(block, "data-latex", latex)
	block.AppendChild(&htmlnode.Node{Type: htmlnode.TextNode, Data: fmt.Sprintf("$$%s$$", latex)})
	replaceNode(p, block)
	return true
}

func transformInlineMath(parent, textNode *htmlnode.Node) {
	segments, ok := splitInlineMath(textNode.Data)
	if !ok {
		return
	}

	for _, seg := range segments {
		if seg.math {
			span := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "span"}
			setAttr(span, "class", "math-inline")
			setAttr(span, "data-latex", seg.value)
			span.AppendChild(&htmlnode.Node{Type: htmlnode.TextNode, Data: "$" + seg.value + "$"})
			parent.InsertBefore(span, textNode)
			continue
		}
		if seg.value == "" {
			continue
		}
		parent.InsertBefore(&htmlnode.Node{Type: htmlnode.TextNode, Data: seg.value}, textNode)
	}
	parent.RemoveChild(textNode)
}

func transformPreBlock(pre *htmlnode.Node) {
	if pre == nil || pre.Type != htmlnode.ElementNode || pre.Data != "pre" {
		return
	}
	code := firstElementChild(pre)
	if code == nil || code.Data != "code" {
		return
	}

	language := strings.TrimPrefix(getLanguageClass(code), "language-")
	codeText := extractText(code)
	if language == "mermaid" {
		transformMermaidBlock(pre, codeText)
		return
	}

	appendClass(pre, "code-block")
	setAttr(pre, "data-code-enhanced", "true")
	setAttr(pre, "data-cb-initialized", "server")

	lineCount := countLines(codeText)
	setAttr(pre, "data-line-count", fmt.Sprintf("%d", lineCount))
	if lineCount > 500 {
		appendClass(pre, "code-collapsed")
	}

	for _, button := range buildCodeButtons(lineCount) {
		pre.InsertBefore(button, pre.FirstChild)
	}
}

func transformMermaidBlock(pre *htmlnode.Node, code string) {
	mermaid := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "pre"}
	setAttr(mermaid, "class", "mermaid")
	mermaid.AppendChild(&htmlnode.Node{Type: htmlnode.TextNode, Data: strings.TrimSpace(code)})
	replaceNode(pre, mermaid)
}

func buildCodeButtons(lineCount int) []*htmlnode.Node {
	buttons := []*htmlnode.Node{
		newButton("code-btn copy-btn", "copy", "Copy", "Copy code"),
		newButton("code-btn wrap-btn", "wrap", "Wrap", "Toggle line wrap"),
	}
	if lineCount > 500 {
		buttons = append(buttons, newButton("code-btn show-all-btn", "show-all", fmt.Sprintf("Show all (%d lines)", lineCount), "Show full content"))
	}
	return buttons
}

func newButton(className, action, text, ariaLabel string) *htmlnode.Node {
	button := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "button"}
	setAttr(button, "type", "button")
	setAttr(button, "class", className)
	setAttr(button, "data-code-action", action)
	setAttr(button, "aria-label", ariaLabel)
	button.AppendChild(&htmlnode.Node{Type: htmlnode.TextNode, Data: text})
	return button
}

func unwrapBlockMath(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "$$") || !strings.HasSuffix(trimmed, "$$") || len(trimmed) < 4 {
		return "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "$$"), "$$"))
	if inner == "" || strings.Contains(inner, "<") || strings.Contains(inner, ">") {
		return "", false
	}
	return inner, true
}

type inlineMathSegment struct {
	value string
	math  bool
}

func splitInlineMath(text string) ([]inlineMathSegment, bool) {
	var segments []inlineMathSegment
	var plain strings.Builder
	changed := false

	flushPlain := func() {
		if plain.Len() == 0 {
			return
		}
		segments = append(segments, inlineMathSegment{value: plain.String()})
		plain.Reset()
	}

	for i := 0; i < len(text); {
		if text[i] == '\\' && i+1 < len(text) {
			plain.WriteByte(text[i])
			i++
			plain.WriteByte(text[i])
			i++
			continue
		}
		if text[i] != '$' || (i+1 < len(text) && text[i+1] == '$') {
			plain.WriteByte(text[i])
			i++
			continue
		}

		end := findInlineMathEnd(text, i+1)
		if end == -1 {
			plain.WriteByte(text[i])
			i++
			continue
		}

		latex := text[i+1 : end]
		if !validInlineMath(latex) {
			plain.WriteString(text[i : end+1])
			i = end + 1
			continue
		}

		flushPlain()
		segments = append(segments, inlineMathSegment{value: latex, math: true})
		changed = true
		i = end + 1
	}
	flushPlain()

	if !changed {
		return nil, false
	}
	return segments, true
}

func findInlineMathEnd(text string, start int) int {
	for i := start; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '$' {
			return i
		}
	}
	return -1
}

func validInlineMath(latex string) bool {
	trimmed := strings.TrimSpace(latex)
	if trimmed == "" || strings.Contains(trimmed, "\n") {
		return false
	}
	runes := []rune(trimmed)
	if unicode.IsSpace(runes[0]) || unicode.IsSpace(runes[len(runes)-1]) {
		return false
	}
	return true
}

func firstElementChild(node *htmlnode.Node) *htmlnode.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == htmlnode.ElementNode {
			return child
		}
	}
	return nil
}

func findElementByID(node *htmlnode.Node, id string) *htmlnode.Node {
	if node == nil {
		return nil
	}
	if node.Type == htmlnode.ElementNode {
		for _, attr := range node.Attr {
			if attr.Key == "id" && attr.Val == id {
				return node
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElementByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

func getLanguageClass(node *htmlnode.Node) string {
	for _, attr := range node.Attr {
		if attr.Key != "class" {
			continue
		}
		for _, className := range strings.Fields(attr.Val) {
			if strings.HasPrefix(className, "language-") {
				return className
			}
		}
	}
	return ""
}

func extractText(node *htmlnode.Node) string {
	var result strings.Builder
	var walk func(*htmlnode.Node)
	walk = func(n *htmlnode.Node) {
		if n.Type == htmlnode.TextNode {
			result.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return result.String()
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}

func isLiteralContainer(node *htmlnode.Node) bool {
	if node == nil || node.Type != htmlnode.ElementNode {
		return false
	}
	switch node.Data {
	case "pre", "code", "script", "style", "textarea":
		return true
	default:
		return false
	}
}

func replaceNode(oldNode, newNode *htmlnode.Node) {
	if oldNode == nil || oldNode.Parent == nil || newNode == nil {
		return
	}
	oldNode.Parent.InsertBefore(newNode, oldNode)
	oldNode.Parent.RemoveChild(oldNode)
}

func setAttr(node *htmlnode.Node, key, value string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, htmlnode.Attribute{Key: key, Val: value})
}

func appendClass(node *htmlnode.Node, className string) {
	if className == "" {
		return
	}
	for i := range node.Attr {
		if node.Attr[i].Key != "class" {
			continue
		}
		classes := strings.Fields(node.Attr[i].Val)
		for _, existing := range classes {
			if existing == className {
				return
			}
		}
		node.Attr[i].Val = strings.TrimSpace(node.Attr[i].Val + " " + className)
		return
	}
	setAttr(node, "class", className)
}
