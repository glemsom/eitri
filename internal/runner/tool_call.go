package runner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/llm"
	"github.com/glemsom/eitri/internal/runstate"
	"github.com/glemsom/eitri/internal/tool"
)

// toolLister is the interface for listing tool definitions, used by toolDefsFromRegistry.
// *tool.Registry satisfies this interface.
type toolLister interface {
	LitellmTools() []litellm.Tool
}

// toolDefsFromRegistry converts tool definitions from a tool lister to internal ToolDefs.
func toolDefsFromRegistry(reg toolLister) []llm.ToolDef {
	vooTools := reg.LitellmTools()
	defs := make([]llm.ToolDef, len(vooTools))
	for i, t := range vooTools {
		defs[i] = llm.ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  json.RawMessage(t.Parameters),
		}
	}
	return defs
}

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
	"edit":                   "FileEditCard",
}

// emitComponentForTool emits a component event based on the tool name and args.
// Supported tools: render_mermaid_diagram, render_quick_replies, edit.
// The edit tool emits a FileEditCard using old_text/new_text/path from args.
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

	case "FileEditCard":
		var parsed struct {
			Path    string `json:"path"`
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if err := json.Unmarshal(args, &parsed); err != nil {
			return "", nil, false
		}
		if parsed.Path == "" {
			return "", nil, false
		}
		if parsed.OldText == "" && parsed.NewText == "" {
			return "", nil, false
		}
		fullOld := parsed.OldText
		fullNew := parsed.NewText
		data["path"] = parsed.Path
		data["mode"] = "overwrite"
		data["old"] = fullOld
		data["new"] = fullNew
		data["bytes_written"] = len(parsed.NewText)
		// Note: dirs_created always empty for edit tool

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
