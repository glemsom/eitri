package api

import (
	"strings"

	htmlnode "golang.org/x/net/html"
)

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
		// Skip whitespace-only text nodes — these are artifacts from goldmark's
		// output formatting (newlines between block elements) and with
		// white-space: pre-wrap on user messages they'd create visible double
		// newlines on top of paragraph margins.
		if child.Type == htmlnode.TextNode && strings.TrimSpace(child.Data) == "" {
			continue
		}
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
			case "a":
				sanitizeLink(child)
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

// sanitizeLink removes the <a> element if its href uses a disallowed URL scheme
// (javascript:, data:, file:, vbscript:), replacing it with its text content.
// Also handles empty href (goldmark strips dangerous schemes but leaves an empty
// <a> element). Only http:, https:, and mailto: are allowed. This matches the
// client-side lightweightMarkdown() behaviour and prevents XSS via injected links.
func sanitizeLink(node *htmlnode.Node) {
	if node == nil || node.Type != htmlnode.ElementNode {
		return
	}

	var href string
	for _, attr := range node.Attr {
		if attr.Key == "href" {
			href = attr.Val
			break
		}
	}

	// Empty href: goldmark strips dangerous schemes but still creates <a>.
	// Convert to plain text.
	if href == "" {
		replaceLinkWithText(node)
		return
	}

	// Scheme is everything before :
	// Check for dangerous schemes: javascript:, data:, file:, vbscript:
	schemeEnd := strings.Index(href, ":")
	if schemeEnd == -1 {
		return // no scheme, leave as-is (likely a relative path)
	}
	scheme := strings.ToLower(href[:schemeEnd])
	switch scheme {
	case "http", "https", "mailto":
		// Allowed — leave the link intact
		return
	default:
		replaceLinkWithText(node)
	}
}

// replaceLinkWithText replaces an <a> element with its text content.
func replaceLinkWithText(node *htmlnode.Node) {
	parent := node.Parent
	if parent == nil {
		return
	}
	text := extractText(node)
	if text == "" {
		parent.RemoveChild(node)
		return
	}
	textNode := &htmlnode.Node{
		Type: htmlnode.TextNode,
		Data: text,
	}
	parent.InsertBefore(textNode, node)
	parent.RemoveChild(node)
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

func newButton(className, action, text, ariaLabel string) *htmlnode.Node {
	button := &htmlnode.Node{Type: htmlnode.ElementNode, Data: "button"}
	setAttr(button, "type", "button")
	setAttr(button, "class", className)
	setAttr(button, "data-code-action", action)
	setAttr(button, "aria-label", ariaLabel)
	button.AppendChild(&htmlnode.Node{Type: htmlnode.TextNode, Data: text})
	return button
}
