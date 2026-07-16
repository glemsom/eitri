package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/litellm"

	"github.com/glemsom/eitri/internal/fileutil"
)

type fileViewerArgs struct {
	Mode            string `json:"mode,omitempty" jsonschema:"Mode: \"read\" (default) or \"list\""`
	Path            string `json:"path" jsonschema:"File or directory path relative to workspace root or an absolute path within the workspace"`
	FilePath        string `json:"file_path,omitempty" jsonschema:"Alias for path (backward compatibility)"`
	Offset          int    `json:"offset,omitempty" jsonschema:"1-indexed line offset to start reading from (default: 1)"`
	Limit           *int   `json:"limit,omitempty" jsonschema:"Maximum number of lines to return (omit for default 100, 0 for unlimited)"`
	IncludeLineInfo bool   `json:"include_line_info,omitempty" jsonschema:"Prefix each line with LINE:HASH for use as anchors in file_editor (default: false)"`
}

// FileViewerTool implements ToolHandler for reading files and listing directories.
type FileViewerTool struct {
	workspace string
	skillDirs []string
	schema    litellm.Schema
}

// NewFileViewer creates a new FileViewerTool.
func NewFileViewer(workspace string, skillDirs []string) *FileViewerTool {
	return &FileViewerTool{
		workspace: workspace,
		skillDirs: skillDirs,
		schema:    SchemaOf[fileViewerArgs](),
	}
}

func (t *FileViewerTool) Name() string {
	return "file_viewer"
}

func (t *FileViewerTool) Description() string {
	return `Read file contents from workspace or active skill directories.

Supports multiple modes:

- "read" (default): Read a file with optional line offset and limit. Only UTF-8 text files.
  Parameter name is "path" (NOT "file_path").
  Set include_line_info=true to get line-number + content-hash prefixes
  (e.g. "15:a1b2c3 | text") for use as anchors in file_editor. Use offset and limit for
  progressive reading when total_lines > limit.
  Default limit is 100 lines; set limit=0 for unlimited.

- "list": List directory contents. Returns sorted filenames and subdirectory names.
  Directories end with "/". Does not recurse.

When reading files for editing, prefer include_line_info=true so you can use line-hash anchors
for surgical edits. Then read the next chunk via offset + limit using next_offset from the response.`
}

func (t *FileViewerTool) JSONSchema() litellm.Schema {
	return t.schema
}

func (t *FileViewerTool) Call(ctx context.Context, args json.RawMessage) ([]litellm.Block, error, bool) {
	var parsed fileViewerArgs
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil, fmt.Errorf("file_viewer: invalid args: %w", err), false
	}

	// Accept "file_path" as alias for "path"
	if parsed.Path == "" && parsed.FilePath != "" {
		parsed.Path = parsed.FilePath
	}

	if parsed.Path == "" {
		return textBlocks("Error: path is required"), nil, true
	}

	// Validate path against workspace and skill dirs
	absPath, err := fileutil.ValidatePathWithAllowed(parsed.Path, t.workspace, t.skillDirs)
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	if parsed.Mode == "list" {
		dr, err := fileutil.ListDirectory(absPath)
		if err != nil {
			return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
		}
		output := fmt.Sprintf("Directory listing: %s\n\n", parsed.Path)
		for _, entry := range dr.Entries {
			output += entry + "\n"
		}
		return textBlocks(output), nil, false
	}

	limit := 100
	if parsed.Limit != nil {
		limit = *parsed.Limit
	}

	var vr fileutil.FileViewerResult
	if parsed.IncludeLineInfo {
		vr, err = fileutil.ReadFileWithLineInfo(absPath, parsed.Offset, limit)
	} else {
		vr, err = fileutil.ReadFile(absPath, parsed.Offset, limit)
	}
	if err != nil {
		return textBlocks(fmt.Sprintf("Error: %v", err)), nil, true
	}

	output := vr.Content
	if vr.Truncated {
		output += fmt.Sprintf("\n... [showing lines %d-%d of %d; use offset=%d to continue]",
			parsed.Offset+1, parsed.Offset+len(vr.Content), vr.TotalLines, vr.NextOffset)
	}

	return textBlocks(output), nil, false
}
