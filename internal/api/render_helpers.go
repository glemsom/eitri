package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/glemsom/eitri/internal/api/templates"
	"github.com/glemsom/eitri/internal/session"
)

// hasMermaidComponent checks if a message has a MermaidDiagram component registered.
func hasMermaidComponent(components []session.ComponentData) bool {
	for _, c := range components {
		if c.Name == "MermaidDiagram" {
			return true
		}
	}
	return false
}

// stripMermaidCodeBlocks removes mermaid fenced code blocks from markdown text.
// Used to prevent duplicate diagram rendering when a MermaidDiagram component already exists.
func stripMermaidCodeBlocks(md string) string {
	if !strings.Contains(md, "```mermaid") {
		return md
	}
	var buf strings.Builder
	lines := strings.Split(md, "\n")
	skip := false
	lastEmpty := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !skip && strings.HasPrefix(trimmed, "```mermaid") {
			suffix := strings.TrimPrefix(trimmed, "```mermaid")
			if suffix == "" || suffix[0] == ' ' {
				skip = true
				continue
			}
		}
		if skip && strings.HasPrefix(trimmed, "```") {
			trail := strings.TrimLeft(trimmed[3:], " \t")
			if trail == "" {
				skip = false
				continue
			}
		}
		if skip {
			continue
		}
		isEmpty := line == ""
		if isEmpty && lastEmpty {
			continue
		}
		if buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(line)
		lastEmpty = isEmpty
	}
	return buf.String()
}

func renderSessionForPage(sess *session.UISession) *session.UISession {
	if sess == nil {
		return nil
	}

	ctx := context.Background()
	rendered := *sess
	rendered.ActiveSkills = append([]string(nil), sess.ActiveSkills...)
	rendered.Messages = make([]session.Message, len(sess.Messages))
	for i, msg := range sess.Messages {
		rendered.Messages[i] = msg
		if msg.Role == "assistant" {
			content := msg.Content
			if hasMermaidComponent(msg.Components) {
				content = stripMermaidCodeBlocks(content)
			}
			contentHTML := renderMarkdownToHTML(content)
			componentsHTML := renderComponentsToHTML(ctx, sess.ID, msg.Components)
			if componentsHTML != "" {
				contentHTML += "\n" + componentsHTML
			}
			rendered.Messages[i].Content = contentHTML
		} else {
			rendered.Messages[i].Content = renderMarkdownToHTML(msg.Content)
		}
	}
	return &rendered
}

// renderComponentsToHTML renders stored component data into HTML strings.
// Components are rendered using the same templates as the SSE render endpoint.
func renderComponentsToHTML(ctx context.Context, sessionID string, components []session.ComponentData) string {
	if len(components) == 0 {
		return ""
	}
	var html strings.Builder
	for _, comp := range components {
		switch comp.Name {
		case "MermaidDiagram":
			code, _ := comp.Data["code"].(string)
			compTempl := templates.MermaidDiagram(code)
			_ = compTempl.Render(ctx, &html)
		case "QuickReplies":
			// QuickReplies are now stored inline on the message, not as a component.
			// Skip rendering here — inserted by AssistantBubble.
		case "DiffCard":
			oldCode, _ := comp.Data["old"].(string)
			newCode, _ := comp.Data["new"].(string)
			lang, _ := comp.Data["lang"].(string)
			compTempl := templates.DiffCard(oldCode, newCode, lang)
			_ = compTempl.Render(ctx, &html)
		case "FileEditCard":
			path, _ := comp.Data["path"].(string)
			mode, _ := comp.Data["mode"].(string)
			oldContent, _ := comp.Data["old"].(string)
			newContent, _ := comp.Data["new"].(string)
			bytesWritten, _ := comp.Data["bytes_written"].(int)
			compTempl := templates.FileEditCard(path, mode, oldContent, newContent, bytesWritten, nil)
			_ = compTempl.Render(ctx, &html)
		}
	}
	return html.String()
}

// renderInlineComponentsToHTML renders only components that belong inside the
// assistant bubble content (not tool cards). FileEditCard is excluded because
// it is rendered as a standalone tool card replacement.
func renderInlineComponentsToHTML(ctx context.Context, sessionID string, components []session.ComponentData) string {
	if len(components) == 0 {
		return ""
	}
	var html strings.Builder
	for _, comp := range components {
		switch comp.Name {
		case "MermaidDiagram":
			code, _ := comp.Data["code"].(string)
			compTempl := templates.MermaidDiagram(code)
			_ = compTempl.Render(ctx, &html)
		case "DiffCard":
			oldCode, _ := comp.Data["old"].(string)
			newCode, _ := comp.Data["new"].(string)
			lang, _ := comp.Data["lang"].(string)
			compTempl := templates.DiffCard(oldCode, newCode, lang)
			_ = compTempl.Render(ctx, &html)
		// FileEditCard is excluded — rendered as a tool card.
		// QuickReplies is excluded — rendered by AssistantBubble.
		}
	}
	return html.String()
}

// mustJSON marshals v to JSON bytes, logging and returning nil on error.
func mustJSON(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		slog.Warn("json marshal error", slog.Any("error", err))
		return nil
	}
	return b
}
