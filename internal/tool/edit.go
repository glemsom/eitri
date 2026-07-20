package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

type editArgs struct {
	Path    string `json:"path" jsonschema:"File path relative to workspace root"`
	OldText string `json:"old_text" jsonschema:"Exact text block to find (use surrounding lines as context anchors for uniqueness)"`
	NewText string `json:"new_text" jsonschema:"Replacement text for the matched block"`
}

// EditTool implements ToolHandler for precise search-and-replace on existing files.
type EditTool struct {
	workspace string
	schema    litellm.Schema
}

// NewEditTool creates a new EditTool.
func NewEditTool(workspace string) *EditTool {
	return &EditTool{
		workspace: workspace,
		schema:    SchemaOf[editArgs](),
	}
}

func (t *EditTool) Name() string {
	return "edit"
}

func (t *EditTool) Description() string {
	return "Precise search-and-replace on an existing file. old_text must uniquely match one location. Always include surrounding context lines for uniqueness. Shows diff in UI. For new files use write instead."
}

func (t *EditTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *EditTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed editArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("edit: invalid args: %w", err), false
	}

	if parsed.Path == "" {
		return textBlocks("Error: path is required"), nil, true
	}

	absPath, err := fileutil.ValidateWorkspacePath(parsed.Path, t.workspace)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	// Read file
	data, err := os.ReadFile(absPath)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: cannot read file: %v", err)), nil, true
	}

	oldContent := string(data)

	// Count matches
	count := strings.Count(oldContent, parsed.OldText)
	if count == 0 {
		// Provide nearby content hint: show first lines so LLM can self-correct
		lines := strings.SplitN(oldContent, "\n", 6)
		trunc := lines
		if len(lines) > 5 {
			trunc = lines[:5]
		}
		prefix := strings.Join(trunc, "\n")
		if len(lines) > 5 {
			prefix += "..."
		}
		return textBlocks(fmt.Sprintf("Error: text %q not found in file. File starts with:\n%s", parsed.OldText, prefix)), nil, true
	}
	if count > 1 {
		return textBlocks(fmt.Sprintf("Error: text %q matched %d times in file, expected exactly 1 match. Include more surrounding context in 'old_text' to make it unique.", parsed.OldText, count)), nil, true
	}

	// Perform replacement
	newContent := strings.Replace(oldContent, parsed.OldText, parsed.NewText, 1)

	if err := os.WriteFile(absPath, []byte(newContent), 0644); err != nil {
		return textBlocks(fmt.Sprintf("Error: failed to write file: %v", err)), nil, true
	}

	return []litellm.Block{
		litellm.TextBlock{Text: fmt.Sprintf("FULL_OLD_CONTENT:%s", oldContent)},
		litellm.TextBlock{Text: fmt.Sprintf("FULL_NEW_CONTENT:%s", newContent)},
		litellm.TextBlock{Text: fmt.Sprintf("Edited file: %s", parsed.Path)},
	}, nil, false
}
