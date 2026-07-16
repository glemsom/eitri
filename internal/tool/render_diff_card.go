package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"
)

type renderDiffCardArgs struct {
	Old  string `json:"old" jsonschema:"Original content (before the change)"`
	New  string `json:"new" jsonschema:"New content (after the change)"`
	Lang string `json:"lang,omitempty" jsonschema:"Optional language for syntax highlighting (e.g. 'go', 'python')"`
}

// RenderDiffCardTool implements ToolHandler for rendering DiffCard components.
type RenderDiffCardTool struct {
	schema litellm.Schema
}

// NewRenderDiffCard creates a new RenderDiffCardTool.
func NewRenderDiffCard() *RenderDiffCardTool {
	return &RenderDiffCardTool{
		schema: SchemaOf[renderDiffCardArgs](),
	}
}

func (t *RenderDiffCardTool) Name() string {
	return "render_diff_card"
}

func (t *RenderDiffCardTool) Description() string {
	return "Render a diff card (side-by-side / unified toggle) in the chat. Provide old and new content to compare. Use this to show code changes or file edits visually."
}

func (t *RenderDiffCardTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *RenderDiffCardTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed renderDiffCardArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("render_diff_card: invalid args: %w", err), false
	}

	if parsed.Old == "" && parsed.New == "" {
		return textBlocks("Error: at least one of 'old' or 'new' must be non-empty"), nil, true
	}

	result := fmt.Sprintf("Rendered DiffCard")
	hasContent := false
	if parsed.Old != "" {
		result += fmt.Sprintf("\nOLD:\n%s", parsed.Old)
		hasContent = true
	}
	if parsed.New != "" {
		if hasContent {
			result += "\n"
		}
		result += fmt.Sprintf("NEW:\n%s", parsed.New)
	}

	return textBlocks(result), nil, false
}
