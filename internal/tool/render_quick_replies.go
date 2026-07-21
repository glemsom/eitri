package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/litellm"
)

type renderQuickRepliesArgs struct {
	Options []string `json:"options" jsonschema:"List of suggestion button labels"`
}

// RenderQuickRepliesTool implements ToolHandler for rendering QuickReplies chips.
type RenderQuickRepliesTool struct {
	schema litellm.Schema
}

// NewRenderQuickReplies creates a new RenderQuickRepliesTool.
func NewRenderQuickReplies() *RenderQuickRepliesTool {
	return &RenderQuickRepliesTool{
		schema: SchemaOf[renderQuickRepliesArgs](),
	}
}

func (t *RenderQuickRepliesTool) Name() string {
	return "render_quick_replies"
}

func (t *RenderQuickRepliesTool) Description() string {
	return "Render suggestion chips (quick replies) visible to the user in the chat UI. Provide option labels like ['Yes', 'No', 'Maybe']. The rendering happens client-side as clickable buttons. Ideal for multi-choice questions where you want the user to pick one option — pass the choices as the options array and the user's click will be sent back as a chat message."
}

func (t *RenderQuickRepliesTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *RenderQuickRepliesTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed renderQuickRepliesArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("render_quick_replies: invalid args: %w", err), false
	}

	if len(parsed.Options) == 0 {
		return textBlocks("Error: 'options' must be a non-empty array of strings"), nil, true
	}

	return textBlocks(fmt.Sprintf("Rendered QuickReplies with options: %s", strings.Join(parsed.Options, ", "))), nil, false
}
