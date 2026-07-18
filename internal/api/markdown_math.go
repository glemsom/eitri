package api

import (
	"fmt"
	"strings"
	"unicode"

	htmlnode "golang.org/x/net/html"
)

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
