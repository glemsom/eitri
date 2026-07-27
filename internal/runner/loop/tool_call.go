package loop

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/tool"
)

// blocksToText extracts text content from a slice of voocel/litellm blocks.
func blocksToText(blocks []litellm.Block) string {
	var b strings.Builder
	for _, block := range blocks {
		switch v := block.(type) {
		case litellm.TextBlock:
			b.WriteString(v.Text)
		case litellm.ToolResultBlock:
			b.WriteString(blocksToText(v.Content))
		default:
			b.WriteString(fmt.Sprintf("%v", block))
		}
	}
	return b.String()
}

// toolResultHasError checks if the first block is a ToolResultBlock with IsError=true.
func toolResultHasError(blocks []litellm.Block) bool {
	if len(blocks) == 0 {
		return false
	}
	if tr, ok := blocks[0].(litellm.ToolResultBlock); ok {
		return tr.IsError
	}
	return false
}

// componentToolMap maps tool names to component names for component emission.
var componentToolMap = map[string]string{
	"render_mermaid_diagram": "MermaidDiagram",
	"render_quick_replies":   "QuickReplies",
}

// screenshotFilenameRE extracts the screenshot filename from the browser tool's result text.
// The browser tool returns text like "Screenshot saved to browser-screenshot-12345.png".
var screenshotFilenameRE = regexp.MustCompile(`Screenshot saved to (browser-screenshot-\d+\.png)`)

// emitComponentForTool emits a component event based on the tool name and args.
// Supported tools: render_mermaid_diagram, render_quick_replies.
// QuickReplies does NOT emit a component SSE event (chips are stored inline on the message).
// Returns (componentName, data, ok) for the caller to also persist the component.
func emitComponentForTool(w *runstate.Writer, toolName string, args json.RawMessage, blocks []litellm.Block) (string, map[string]any, bool) {
	componentName, ok := componentToolMap[toolName]
	if !ok {
		return "", nil, false
	}

	data := make(map[string]any)

	switch componentName {
	case "MermaidDiagram":
		var parsed struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil || parsed.Code == "" {
			return "", nil, false
		}
		data["code"] = parsed.Code

	case "QuickReplies":
		var parsed struct {
			Options []string `json:"options"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil || len(parsed.Options) == 0 {
			return "", nil, false
		}
		data["options"] = parsed.Options
		// QuickReplies renders inline — no separate SSE component event
		return componentName, data, true

	default:
		return "", nil, false
	}

	w.Component(map[string]any{
		"kind": "component",
		"name": componentName,
		"data": data,
	})
	return componentName, data, true
}

// hasImageBlock checks if the result blocks contain an ImageBlock (indicating a screenshot).
func hasImageBlock(blocks []litellm.Block) bool {
	for _, block := range blocks {
		if _, ok := block.(litellm.ImageBlock); ok {
			return true
		}
	}
	return false
}

// emitScreenshotComponent checks if the tool result is a successful browser screenshot
// and emits a Screenshot component event. Returns (componentName, data, ok).
func emitScreenshotComponent(w *runstate.Writer, toolName string, blocks []litellm.Block, sessionID string) (string, map[string]any, bool) {
	if toolName != "browser" {
		return "", nil, false
	}

	// Only emit if the result contains an ImageBlock (successful screenshot)
	if !hasImageBlock(blocks) {
		return "", nil, false
	}

	// Extract the filename from the result text
	resultText := blocksToText(blocks)
	matches := screenshotFilenameRE.FindStringSubmatch(resultText)
	if len(matches) < 2 {
		return "", nil, false
	}
	filename := matches[1]
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	data := map[string]any{
		"session_id": sessionID,
		"filename":   filename,
		"timestamp":  timestamp,
	}

	w.Component(map[string]any{
		"kind": "component",
		"name": "Screenshot",
		"data": data,
	})

	return "Screenshot", data, true
}

// addReadToolAllowedPath looks up the ReadTool in the registry and appends a path
// to its temporary allowed paths list. Used by the agent loop when a user approves
// a blocked read path so the tool can re-execute without another confirmation.
func addReadToolAllowedPath(tools *tool.Registry, path string) {
	h := tools.Lookup("read")
	if h == nil {
		return
	}
	rt, ok := h.(*tool.ReadTool)
	if !ok {
		return
	}
	rt.AppendAllowedPaths(path)
}
