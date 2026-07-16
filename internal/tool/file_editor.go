package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"

	eitriagent "github.com/glemsom/eitri/internal/agent"
)

type fileEditorArgs struct {
	Path    string `json:"path" jsonschema:"File path relative to workspace root"`
	Content string `json:"content,omitempty" jsonschema:"File content (UTF-8 text) — for create, overwrite, insert modes"`
	Mode    string `json:"mode" jsonschema:"'create', 'overwrite', 'edit', or 'insert'"`
	Old     string `json:"old,omitempty" jsonschema:"Exact text to find (for edit mode only)"`
	New     string `json:"new,omitempty" jsonschema:"Replacement text (for edit mode only)"`
	Anchor  string `json:"anchor,omitempty" jsonschema:"LINE:HASH anchor (for edit or insert mode). Get from file_viewer with include_line_info=true"`
}

// FileEditorTool implements ToolHandler for creating and editing files.
type FileEditorTool struct {
	workspace string
	schema    litellm.Schema
}

// NewFileEditor creates a new FileEditorTool.
func NewFileEditor(workspace string) *FileEditorTool {
	return &FileEditorTool{
		workspace: workspace,
		schema:    SchemaOf[fileEditorArgs](),
	}
}

func (t *FileEditorTool) Name() string {
	return "file_editor"
}

func (t *FileEditorTool) Description() string {
	return `Create or edit files in workspace. Supports multiple modes:

- "create": Create a new file. Fails if file already exists. Creates parent directories automatically.
- "overwrite": Replace entire existing file content. Captures old content for diff display.
- "edit": Surgical text replacement. Provide old text to find and new text to replace it with.
  Optionally provide an anchor ("LINE:HASH" from file_viewer with include_line_info=true) to
  restrict the search to the anchored line. Use this for small, targeted changes instead of overwrite.
  Fails with match count if old text matches 0 or multiple times.
- "insert": Insert new content after a specific line identified by a line-hash anchor.
  Anchor must be in "LINE:HASH" format (e.g. "15:a1b2c3") obtained from file_viewer with
  include_line_info=true. Use this to add new code at precise locations.

Best practice: First read the file with file_viewer(include_line_info=true), then use the returned
line-hash anchors for edit or insert operations. This avoids accidents from duplicate strings.`
}

func (t *FileEditorTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *FileEditorTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed fileEditorArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("file_editor: invalid args: %w", err), false
	}

	if parsed.Path == "" {
		return textBlocks("Error: path is required"), nil, true
	}

	absPath, err := eitriagent.ValidateWorkspacePath(parsed.Path, t.workspace)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	switch parsed.Mode {
	case "edit":
		if parsed.Old == "" {
			return textBlocks("Error: edit mode requires 'old' text"), nil, true
		}
		er, err := eitriagent.EditFile(absPath, parsed.Old, parsed.New, parsed.Anchor)
		if err != nil {
			return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
		}
		return textBlocks(formatEditResult(parsed.Path, parsed.Mode, er.Old, er.New)), nil, false

	case "insert":
		if parsed.Content == "" {
			return textBlocks("Error: insert mode requires 'content'"), nil, true
		}
		if parsed.Anchor == "" {
			return textBlocks("Error: insert mode requires 'anchor'"), nil, true
		}
		er, err := eitriagent.InsertLine(absPath, parsed.Anchor, parsed.Content)
		if err != nil {
			return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
		}
		return textBlocks(formatInsertResult(parsed.Path, er.BytesWritten)), nil, false

	default:
		// create / overwrite
		if parsed.Content == "" {
			return textBlocks("Error: content is required for create/overwrite mode"), nil, true
		}
		er, err := eitriagent.WriteFile(absPath, parsed.Content, parsed.Mode)
		if err != nil {
			return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
		}

		var output string
		switch er.Mode {
		case "create":
			output = formatCreateResult(parsed.Path, er.BytesWritten, er.DirsCreated)
		case "overwrite":
			output = formatOverwriteResult(parsed.Path, er.BytesWritten, er.OldContent, er.NewContent)
		}
		return textBlocks(output), nil, false
	}
}

func formatCreateResult(path string, bytesWritten int, dirsCreated []string) string {
	output := fmt.Sprintf("Created file: %s (%d bytes)", path, bytesWritten)
	if len(dirsCreated) > 0 {
		output += "\nCreated directories:"
		for _, d := range dirsCreated {
			output += "\n  " + d
		}
	}
	return output
}

func formatOverwriteResult(path string, bytesWritten int, oldContent, newContent string) string {
	return fmt.Sprintf("Overwritten file: %s (%d bytes written)", path, bytesWritten)
}

func formatEditResult(path, mode, old, new string) string {
	return fmt.Sprintf("Edited file: %s\nReplaced: %q → %q", path, old, new)
}

func formatInsertResult(path string, bytesWritten int) string {
	return fmt.Sprintf("Inserted content into: %s (%d bytes)", path, bytesWritten)
}
