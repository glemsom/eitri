package api

import (
	"fmt"
	"strings"

	htmlnode "golang.org/x/net/html"
)

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
