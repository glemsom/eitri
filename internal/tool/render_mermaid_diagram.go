package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

type renderMermaidArgs struct {
	Code string `json:"code" jsonschema:"Mermaid diagram code (graph TD; A-->B; etc.)"`
}

// RenderMermaidDiagramTool implements ToolHandler for rendering Mermaid diagrams.
type RenderMermaidDiagramTool struct {
	schema litellm.Schema
}

// NewRenderMermaidDiagram creates a new RenderMermaidDiagramTool.
func NewRenderMermaidDiagram() *RenderMermaidDiagramTool {
	return &RenderMermaidDiagramTool{
		schema: SchemaOf[renderMermaidArgs](),
	}
}

func (t *RenderMermaidDiagramTool) Name() string {
	return "render_mermaid_diagram"
}

func (t *RenderMermaidDiagramTool) Description() string {
	return "Render a Mermaid diagram visible to the user in the chat UI. Provide diagram code like 'graph TD; A-->B;'. The rendering happens client-side in the browser. Use for visual diagrams over ASCII art."
}

func (t *RenderMermaidDiagramTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *RenderMermaidDiagramTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed renderMermaidArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("render_mermaid_diagram: invalid args: %w", err), false
	}

	if parsed.Code == "" {
		return textBlocks("Error: 'code' field is required and must be non-empty"), nil, true
	}

	return textBlocks(fmt.Sprintf("Rendered MermaidDiagram with code:\n%s", parsed.Code)), nil, false
}
