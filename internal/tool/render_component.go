package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

type renderComponentArgs struct {
	Name string                 `json:"name" jsonschema:"Component name: MermaidDiagram, QuickReplies, or DiffCard"`
	Data map[string]interface{} `json:"data" jsonschema:"Component data"`
}

// RenderComponentTool implements ToolHandler for rendering UI components.
type RenderComponentTool struct {
	schema litellm.Schema
}

// NewRenderComponent creates a new RenderComponentTool.
func NewRenderComponent() *RenderComponentTool {
	return &RenderComponentTool{
		schema: SchemaOf[renderComponentArgs](),
	}
}

func (t *RenderComponentTool) Name() string {
	return "render_component"
}

func (t *RenderComponentTool) Description() string {
	return "Render a rich UI component in the chat. Supported components: MermaidDiagram (diagrams), QuickReplies (suggestion chips), DiffCard (code diffs). Use this for visual output instead of plain text when possible."
}

func (t *RenderComponentTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *RenderComponentTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed renderComponentArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("render_component: invalid args: %w", err), false
	}

	if parsed.Name == "" {
		return textBlocks("Error: component name is required"), nil, true
	}

	switch parsed.Name {
	case "MermaidDiagram":
		if parsed.Data == nil {
			return textBlocks("Error: MermaidDiagram requires 'code' field in data"), nil, true
		}
		if _, ok := parsed.Data["code"]; !ok {
			return textBlocks("Error: MermaidDiagram requires 'code' field in data"), nil, true
		}
	case "QuickReplies":
		if parsed.Data == nil {
			return textBlocks("Error: QuickReplies requires 'options' field in data"), nil, true
		}
		options, ok := parsed.Data["options"]
		if !ok {
			return textBlocks("Error: QuickReplies requires 'options' field in data"), nil, true
		}
		optsArr, ok := options.([]interface{})
		if !ok || len(optsArr) == 0 {
			return textBlocks("Error: QuickReplies options must be a non-empty array of strings"), nil, true
		}
	case "DiffCard":
		if parsed.Data == nil {
			return textBlocks("Error: DiffCard requires 'old' and 'new' fields in data"), nil, true
		}
		if _, ok := parsed.Data["old"]; !ok {
			return textBlocks("Error: DiffCard requires 'old' field in data"), nil, true
		}
		if _, ok := parsed.Data["new"]; !ok {
			return textBlocks("Error: DiffCard requires 'new' field in data"), nil, true
		}
	default:
		return textBlocks(fmt.Sprintf("Error: unknown component %q; supported: MermaidDiagram, QuickReplies, DiffCard", parsed.Name)), nil, true
	}

	return textBlocks(fmt.Sprintf("[Component rendered: %s]", parsed.Name)), nil, false
}
